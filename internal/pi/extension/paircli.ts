// paircli-extension v1
//
// Records paircli hook data (git HEAD, branch, and working-tree line-hash
// snapshots) into the Pi session file as they happen, via
// pi.appendEntry("paircli", record). internal/pi.HookRecords reads these
// back out (see docs/plan/formats/pi.md "Hook records written by the
// paircli extension").
//
// This file must never throw, block a tool, or print anything: every
// handler body is wrapped in try/catch, and git/filesystem failures are
// recorded in the record's `error` field instead of surfacing.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { execFileSync } from "node:child_process";
import { readFileSync, statSync } from "node:fs";
import { join } from "node:path";

// gitStateChangeRe matches bash commands that change repo state -- the only
// commands worth recording a hook record for. Mirrors
// internal/claudecode/hooks.go's gitStateChangeRe and
// docs/plan/tasks/T21-pi-extension.md.
const gitStateChangeRe =
	/\bgit\s+(commit|merge|rebase|cherry-pick|am|revert|reset|checkout|switch|pull|stash)\b/;

// gitTimeoutMS bounds each git shellout below, matching Go's
// gitinfo.HeadAndBranch / hooklog.Snapshot timeouts.
const gitTimeoutMS = 2000;

// snapshotMaxFileBytes skips any dirty file larger than this.
const snapshotMaxFileBytes = 1 << 20; // 1 MB

// snapshotMaxFiles caps how many dirty files (sorted by path) snapshot() considers.
const snapshotMaxFiles = 200;

// snapshotSniffBytes is how much of a file's head snapshot() checks for a
// NUL byte to decide whether it is binary.
const snapshotSniffBytes = 8192;

interface HeadInfo {
	sha?: string;
	branch?: string;
	error?: string;
}

interface FileLinesRecord {
	path: string;
	hashes: string[];
}

// head runs `git -C cwd rev-parse HEAD` and `--abbrev-ref HEAD`, mirroring
// Go's internal/gitinfo.HeadAndBranch. Detached HEAD ("HEAD") becomes "".
// Failures are reported via the `error` field, never thrown.
function head(cwd: string): HeadInfo {
	try {
		const sha = execFileSync("git", ["-C", cwd, "rev-parse", "HEAD"], {
			timeout: gitTimeoutMS,
			encoding: "utf8",
			stdio: ["ignore", "pipe", "ignore"],
		}).trim();
		let branch = execFileSync(
			"git",
			["-C", cwd, "rev-parse", "--abbrev-ref", "HEAD"],
			{
				timeout: gitTimeoutMS,
				encoding: "utf8",
				stdio: ["ignore", "pipe", "ignore"],
			},
		).trim();
		if (branch === "HEAD") branch = "";
		return { sha, branch };
	} catch (err) {
		return { error: err instanceof Error ? err.message : String(err) };
	}
}

// splitLines splits file content into lines without trailing newline
// characters, mirroring Go's model.SplitLines exactly (including its
// "" -> [] special case).
function splitLines(content: string): string[] {
	if (content === "") return [];
	let c = content;
	if (c.endsWith("\n")) c = c.slice(0, -1);
	return c.split("\n").map((l) => (l.endsWith("\r") ? l.slice(0, -1) : l));
}

// FNV-1a 64-bit constants (see model.LineHash).
const fnvOffset = 0xcbf29ce484222325n;
const fnvPrime = 0x100000001b3n;
const mask64 = 0xffffffffffffffffn;

// lineHash must equal Go's model.LineHash bit-for-bit: FNV-1a 64 over the
// UTF-8 bytes of s with trailing spaces, tabs and \r removed, as 16
// lowercase hex characters.
export function lineHash(s: string): string {
	const normalized = s.replace(/[ \t\r]+$/, "");
	const bytes = Buffer.from(normalized, "utf8");
	let hash = fnvOffset;
	for (let i = 0; i < bytes.length; i++) {
		hash ^= BigInt(bytes[i]);
		hash = (hash * fnvPrime) & mask64;
	}
	return hash.toString(16).padStart(16, "0");
}

// dirtyPaths parses `git status --porcelain=v1 -z` output into the set of
// repo-relative paths worth snapshotting (skips pure deletions), mirroring
// Go's hooklog.dirtyPaths.
function dirtyPaths(out: string): string[] {
	const tokens = out.split("\0");
	const seen = new Set<string>();
	const paths: string[] = [];
	for (let i = 0; i < tokens.length; i++) {
		const tok = tokens[i];
		if (tok.length < 4) continue;
		const status = tok.slice(0, 2);
		const path = tok.slice(3);
		if (/[RC]/.test(status)) {
			// The next token is the rename/copy "from" path; skip it.
			i++;
		}
		if (!/[MARCU?]/.test(status)) continue; // pure deletion or unrecognized status
		if (seen.has(path)) continue;
		seen.add(path);
		paths.push(path);
	}
	return paths;
}

// snapshot returns line hashes of the working tree's dirty files, mirroring
// Go's hooklog.Snapshot algorithm exactly: top level, `git status
// --porcelain=v1 -z --untracked-files=all`, skip deletions, files over 1 MB,
// and files with a NUL byte in their first 8 KB; cap at 200 files sorted by
// repo-relative path.
function snapshot(cwd: string): FileLinesRecord[] {
	const top = execFileSync("git", ["-C", cwd, "rev-parse", "--show-toplevel"], {
		timeout: gitTimeoutMS,
		encoding: "utf8",
		stdio: ["ignore", "pipe", "ignore"],
	}).trim();

	const out = execFileSync(
		"git",
		["-C", top, "status", "--porcelain=v1", "-z", "--untracked-files=all"],
		{ timeout: gitTimeoutMS, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
	);

	const paths = dirtyPaths(out).sort();
	const capped = paths.slice(0, snapshotMaxFiles);

	const result: FileLinesRecord[] = [];
	for (const p of capped) {
		const full = join(top, ...p.split("/"));
		let size: number;
		try {
			const st = statSync(full);
			if (!st.isFile()) continue;
			size = st.size;
		} catch {
			continue; // missing file
		}
		if (size > snapshotMaxFileBytes) continue;

		let data: Buffer;
		try {
			data = readFileSync(full);
		} catch {
			continue;
		}
		const head8k = data.subarray(0, snapshotSniffBytes);
		if (head8k.includes(0)) continue; // looks binary

		const lines = splitLines(data.toString("utf8"));
		result.push({ path: p, hashes: lines.map(lineHash) });
	}
	return result;
}

export default function (pi: ExtensionAPI) {
	try {
		// bashCommands remembers the command text of pending bash tool calls,
		// keyed by toolCallId, so tool_execution_end can tell whether it was a
		// git state change worth recording.
		const bashCommands = new Map<string, string>();

		const emit = (
			cwd: string,
			event: string,
			trigger: string,
			extra: Record<string, unknown> = {},
			priorError?: string,
		) => {
			try {
				const rec: Record<string, unknown> = {
					v: 1,
					harness: "pi",
					event,
					session_id: "",
					ts: new Date().toISOString(),
					cwd,
					trigger,
					...extra,
				};
				const h = head(cwd);
				if (h.error) {
					rec.error = h.error;
				} else {
					rec.head_sha = h.sha;
					rec.branch = h.branch;
					// Only surface the snapshot failure when git itself was fine:
					// one `error` field, and the head failure is the more useful one.
					if (priorError) rec.error = priorError;
				}
				pi.appendEntry("paircli", rec);
			} catch {
				// never throw from the extension
			}
		};

		const emitWithSnapshot = (cwd: string, event: string, trigger: string) => {
			try {
				let files: FileLinesRecord[] = [];
				let snapErr: string | undefined;
				try {
					files = snapshot(cwd);
				} catch (err) {
					snapErr = err instanceof Error ? err.message : String(err);
				}
				const extra: Record<string, unknown> = {};
				if (files.length > 0) extra.snapshot = files;
				emit(cwd, event, trigger, extra, snapErr);
			} catch {
				// never throw from the extension
			}
		};

		pi.on("session_start", (event, ctx) => {
			try {
				emit(ctx.cwd, "session_start", "session_start", { reason: event.reason });
			} catch {
				// never throw from the extension
			}
		});

		pi.on("tool_call", (event) => {
			try {
				if (event.toolName === "bash") {
					const command = (event.input as { command?: unknown }).command;
					if (typeof command === "string") {
						bashCommands.set(event.toolCallId, command);
					}
				}
			} catch {
				// never throw from the extension
			}
		});

		pi.on("tool_execution_end", (event, ctx) => {
			try {
				const cmd = bashCommands.get(event.toolCallId);
				bashCommands.delete(event.toolCallId);
				if (cmd && gitStateChangeRe.test(cmd)) {
					emit(ctx.cwd, "tool_execution_end", "commit", {
						command: cmd.slice(0, 300),
						tool_name: "bash",
						tool_use_id: event.toolCallId,
					});
				}
			} catch {
				// never throw from the extension
			}
		});

		pi.on("before_agent_start", (_event, ctx) => {
			try {
				emitWithSnapshot(ctx.cwd, "before_agent_start", "prompt");
			} catch {
				// never throw from the extension
			}
		});

		pi.on("agent_end", (_event, ctx) => {
			try {
				emitWithSnapshot(ctx.cwd, "agent_end", "stop");
			} catch {
				// never throw from the extension
			}
		});

		pi.on("session_shutdown", (event, ctx) => {
			try {
				emit(ctx.cwd, "session_shutdown", "session_end", { reason: event.reason });
			} catch {
				// never throw from the extension
			}
		});
	} catch {
		// never throw from the extension factory
	}
}
