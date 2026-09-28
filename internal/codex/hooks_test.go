package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// --- shared helpers ---

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// codexHomeIn points CODEX_HOME at home and returns the hooks.json path.
func codexHomeIn(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("CODEX_HOME", home)
	path := HooksPath()
	if path == "" {
		t.Fatal("HooksPath() returned an empty path")
	}
	return path
}

func readTestHooks(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal %s: %v (data: %s)", path, err, data)
	}
	return settings
}

// eventGroups returns settings["hooks"][event] as a slice, or nil.
func eventGroups(settings map[string]interface{}, event string) []interface{} {
	hooksRaw, _ := settings["hooks"].(map[string]interface{})
	if hooksRaw == nil {
		return nil
	}
	arr, _ := hooksRaw[event].([]interface{})
	return arr
}

// commandsFor collects every hook "command" string across event's matcher
// groups.
func commandsFor(settings map[string]interface{}, event string) []string {
	var out []string
	for _, m := range eventGroups(settings, event) {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		hooksList, _ := mm["hooks"].([]interface{})
		for _, h := range hooksList {
			hm, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); cmd != "" {
				out = append(out, cmd)
			}
		}
	}
	return out
}

func countMarked(cmds []string) int {
	n := 0
	for _, c := range cmds {
		if strings.Contains(c, hookMarker) {
			n++
		}
	}
	return n
}

// initGitRepo creates a temp git repo with one commit and returns its
// directory. It skips the test if git is not on PATH.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	return dir
}

// --- InstallHooks / UninstallHooks / HooksInstalled ---

func TestInstall_Fresh(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	settings := readTestHooks(t, path)
	for _, spec := range hookEvents {
		cmds := commandsFor(settings, spec.Event)
		if countMarked(cmds) != 1 {
			t.Errorf("event %s: got %d paircli hook commands, want 1 (%v)", spec.Event, countMarked(cmds), cmds)
			continue
		}
		if !strings.Contains(cmds[0], " hook codex "+spec.Event) {
			t.Errorf("event %s: command %q missing expected suffix", spec.Event, cmds[0])
		}

		groups := eventGroups(settings, spec.Event)
		mm := groups[len(groups)-1].(map[string]interface{})
		matcher, hasMatcher := mm["matcher"]
		if spec.Matcher == "" && hasMatcher {
			t.Errorf("event %s: unexpected matcher %v", spec.Event, matcher)
		}
		if spec.Matcher != "" && matcher != spec.Matcher {
			t.Errorf("event %s: matcher = %v, want %q", spec.Event, matcher, spec.Matcher)
		}

		hooksList := mm["hooks"].([]interface{})
		entry := hooksList[len(hooksList)-1].(map[string]interface{})
		if entry["type"] != "command" {
			t.Errorf("event %s: type = %v, want command", spec.Event, entry["type"])
		}
	}

	if !HooksInstalled() {
		t.Error("HooksInstalled() = false after a fresh InstallHooks")
	}
}

func TestInstall_PreservesExisting(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	writeFile(t, path, `{
  "foo": "bar",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "other-tool on-start"}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "other-tool pre-bash"}]}
    ]
  }
}`)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	settings := readTestHooks(t, path)
	if settings["foo"] != "bar" {
		t.Errorf("foo = %v, want bar", settings["foo"])
	}

	startCmds := commandsFor(settings, "SessionStart")
	foundOther := false
	for _, c := range startCmds {
		if c == "other-tool on-start" {
			foundOther = true
		}
	}
	if !foundOther {
		t.Errorf("SessionStart lost the pre-existing other-tool hook: %v", startCmds)
	}
	if countMarked(startCmds) != 1 {
		t.Errorf("SessionStart: got %d paircli hooks, want 1", countMarked(startCmds))
	}

	// An event paircli does not install at all must be untouched.
	preCmds := commandsFor(settings, "PreToolUse")
	if len(preCmds) != 1 || preCmds[0] != "other-tool pre-bash" {
		t.Errorf("PreToolUse hooks changed: %v", preCmds)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks (1st): %v", err)
	}
	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks (2nd): %v", err)
	}

	settings := readTestHooks(t, path)
	for _, spec := range hookEvents {
		cmds := commandsFor(settings, spec.Event)
		if countMarked(cmds) != 1 {
			t.Errorf("event %s: got %d paircli hooks after two installs, want 1 (%v)", spec.Event, countMarked(cmds), cmds)
		}
	}
}

func TestInstall_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	writeFile(t, path, "{not valid json")

	if err := InstallHooks(); err == nil {
		t.Fatal("InstallHooks: want error for invalid existing hooks.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not valid json" {
		t.Errorf("hooks.json was modified despite the error: %q", data)
	}
	if _, err := os.Stat(path + ".paircli.bak"); !os.IsNotExist(err) {
		t.Error("backup created despite invalid JSON error")
	}
}

func TestInstall_Backup(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	original := `{"foo": "bar"}`
	writeFile(t, path, original)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	backupPath := path + ".paircli.bak"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}
	if string(data) != original {
		t.Errorf("backup = %q, want original %q", data, original)
	}

	// A second install must not overwrite the backup, even though the live
	// file has since changed.
	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks (2nd): %v", err)
	}
	data2, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("reading backup after 2nd install: %v", err)
	}
	if string(data2) != original {
		t.Errorf("backup changed after 2nd install: %q", data2)
	}
}

func TestUninstall(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	writeFile(t, path, `{
  "foo": "bar",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "other-tool on-start"}]}
    ]
  }
}`)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if !HooksInstalled() {
		t.Fatal("HooksInstalled() = false after InstallHooks")
	}

	if err := UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}
	if HooksInstalled() {
		t.Error("HooksInstalled() = true after UninstallHooks")
	}

	settings := readTestHooks(t, path)
	if settings["foo"] != "bar" {
		t.Errorf("foo = %v, want bar", settings["foo"])
	}
	startCmds := commandsFor(settings, "SessionStart")
	if len(startCmds) != 1 || startCmds[0] != "other-tool on-start" {
		t.Errorf("SessionStart after uninstall = %v, want just the pre-existing hook", startCmds)
	}
	for _, spec := range hookEvents {
		if spec.Event == "SessionStart" {
			continue
		}
		if groups := eventGroups(settings, spec.Event); len(groups) != 0 {
			t.Errorf("event %s: groups left after uninstall: %v", spec.Event, groups)
		}
	}
}

func TestUninstall_MissingFile(t *testing.T) {
	home := t.TempDir()
	codexHomeIn(t, home)

	if err := UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks on missing hooks.json: %v", err)
	}
}

func TestHooksInstalled(t *testing.T) {
	home := t.TempDir()
	path := codexHomeIn(t, home)

	if HooksInstalled() {
		t.Error("HooksInstalled() = true with no hooks.json")
	}

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if !HooksInstalled() {
		t.Fatal("HooksInstalled() = false after InstallHooks")
	}

	// Partially remove one event's hook to simulate a broken/partial install.
	settings := readTestHooks(t, path)
	hooksRaw := settings["hooks"].(map[string]interface{})
	delete(hooksRaw, "Stop")
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(out))

	if HooksInstalled() {
		t.Error("HooksInstalled() = true after removing one event's hook")
	}
}

func TestInstallMessage(t *testing.T) {
	msg := InstallMessage()
	if msg == "" {
		t.Fatal("InstallMessage() is empty")
	}
	if !strings.Contains(msg, "trust") || !strings.Contains(msg, "paircli doctor") {
		t.Errorf("InstallMessage() = %q, want it to mention trust and paircli doctor", msg)
	}
}

// --- Trust (read-only; paircli must never write config.toml) ---

func TestInstall_NeverWritesTrust(t *testing.T) {
	home := t.TempDir()
	codexHomeIn(t, home)
	configPath := filepath.Join(home, "config.toml")

	original := "# Codex config\napproval_policy = \"never\"\n\n" +
		"[hooks.state]\n\n" +
		"[hooks.state.\"" + HooksPath() + ":session_start:0:0\"]\n" +
		"trusted_hash = \"sha256:deadbeef\"\n"
	writeFile(t, configPath, original)

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	// Uninstall must not touch it either.
	if err := UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config.toml after install/uninstall: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("config.toml changed:\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestInstall_DoesNotCreateTrustFile(t *testing.T) {
	home := t.TempDir()
	codexHomeIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Error("InstallHooks created config.toml; paircli must never write trust state")
	}
}

// trustedKey builds a config.toml [hooks.state."…"] key for one of our hooks.
func trustedKey(event string, group, index int) string {
	return fmt.Sprintf("%s:%s:%d:%d", HooksPath(), eventSnakeCase[event], group, index)
}

func TestHookTrustState(t *testing.T) {
	home := t.TempDir()
	codexHomeIn(t, home)

	// A pre-existing other-tool hook at group 0 means our Stop hook lands at
	// group 1 — the coordinates Codex records in config.toml must match.
	writeFile(t, HooksPath(), `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "other-tool on-stop"}]}
    ]
  }
}`)
	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	// Exactly three of our eight hooks have been reviewed and trusted.
	trusted := []string{
		trustedKey("SessionStart", 0, 0),
		trustedKey("PostToolUse", 0, 0),
		trustedKey("Stop", 1, 0), // group 1: after the other tool's group 0
	}
	var b strings.Builder
	b.WriteString("[hooks.state]\n")
	for _, key := range trusted {
		fmt.Fprintf(&b, "\n[hooks.state.%q]\ntrusted_hash = \"sha256:abc\"\n", key)
	}
	// An untrusted/absent entry for a fourth event must not be counted.
	writeFile(t, filepath.Join(home, "config.toml"), b.String())

	gotTrusted, gotTotal := HookTrustState()
	if gotTotal != len(hookEvents) {
		t.Errorf("HookTrustState total = %d, want %d", gotTotal, len(hookEvents))
	}
	if gotTrusted != 3 {
		t.Errorf("HookTrustState trusted = %d, want 3", gotTrusted)
	}
}

func TestHookTrustState_NoHooks(t *testing.T) {
	home := t.TempDir()
	codexHomeIn(t, home)

	trusted, total := HookTrustState()
	if trusted != 0 || total != 0 {
		t.Errorf("HookTrustState() = (%d, %d), want (0, 0) with no hooks.json", trusted, total)
	}
}

func TestHookTrustState_NoConfigToml(t *testing.T) {
	home := t.TempDir()
	codexHomeIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	trusted, total := HookTrustState()
	if total != len(hookEvents) {
		t.Errorf("HookTrustState total = %d, want %d", total, len(hookEvents))
	}
	if trusted != 0 {
		t.Errorf("HookTrustState trusted = %d, want 0 with no config.toml", trusted)
	}
}

// --- RunHookEvent ---

func setupHookEnv(t *testing.T) (eventsDir, repoDir string) {
	t.Helper()
	eventsDir = t.TempDir()
	t.Setenv("PAIRCLI_EVENTS_DIR", eventsDir)
	t.Setenv("HOME", t.TempDir())
	repoDir = initGitRepo(t)
	return eventsDir, repoDir
}

func runHookEventJSON(t *testing.T, event string, payload map[string]interface{}) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunHookEvent(event, bytes.NewReader(data)); err != nil {
		t.Fatalf("RunHookEvent(%s): %v", event, err)
	}
}

func readRecords(t *testing.T, dir, sessionID string) []model.HookRecord {
	t.Helper()
	all, err := hooklog.Read(dir, model.HarnessCodex, time.Time{})
	if err != nil {
		t.Fatalf("hooklog.Read: %v", err)
	}
	return all[sessionID]
}

func TestRunHookEvent_SessionStart(t *testing.T) {
	dir, repo := setupHookEnv(t)
	runHookEventJSON(t, "SessionStart", map[string]interface{}{
		"session_id": "s1", "cwd": repo, "hook_event_name": "SessionStart", "source": "startup",
	})

	recs := readRecords(t, dir, "s1")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Harness != model.HarnessCodex {
		t.Errorf("Harness = %q, want codex", r.Harness)
	}
	if r.Event != "SessionStart" || r.Trigger != "session_start" || r.Reason != "startup" {
		t.Errorf("record = %+v", r)
	}
	if len(r.HeadSHA) != 40 {
		t.Errorf("HeadSHA = %q, want a 40-char sha", r.HeadSHA)
	}
	if r.Branch != "main" {
		t.Errorf("Branch = %q, want main", r.Branch)
	}
}

func TestRunHookEvent_UserPromptSubmit_Snapshot(t *testing.T) {
	dir, repo := setupHookEnv(t)
	writeFile(t, filepath.Join(repo, "dirty.go"), "package a\n")

	runHookEventJSON(t, "UserPromptSubmit", map[string]interface{}{
		"session_id": "s2", "cwd": repo, "hook_event_name": "UserPromptSubmit",
	})

	recs := readRecords(t, dir, "s2")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Trigger != "prompt" {
		t.Errorf("Trigger = %q, want prompt", r.Trigger)
	}
	if len(r.Snapshot) == 0 {
		t.Error("Snapshot is empty, want the dirty file captured")
	}
}

func TestRunHookEvent_PostToolUse_GitCommit(t *testing.T) {
	dir, repo := setupHookEnv(t)
	runHookEventJSON(t, "PostToolUse", map[string]interface{}{
		"session_id": "s3", "cwd": repo, "hook_event_name": "PostToolUse",
		"tool_name": "Bash", "tool_use_id": "tu1",
		"tool_input": map[string]interface{}{"command": "git commit -am 'wip'"},
	})

	recs := readRecords(t, dir, "s3")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Trigger != "commit" || r.ToolName != "Bash" || r.ToolUseID != "tu1" {
		t.Errorf("record = %+v", r)
	}
	if r.Command != "git commit -am 'wip'" {
		t.Errorf("Command = %q", r.Command)
	}
	if len(r.HeadSHA) != 40 {
		t.Errorf("HeadSHA = %q, want a 40-char sha", r.HeadSHA)
	}
}

func TestRunHookEvent_PostToolUse_NonGitCommandWritesNothing(t *testing.T) {
	dir, repo := setupHookEnv(t)
	runHookEventJSON(t, "PostToolUse", map[string]interface{}{
		"session_id": "s4", "cwd": repo, "hook_event_name": "PostToolUse",
		"tool_name": "Bash", "tool_use_id": "tu2",
		"tool_input": map[string]interface{}{"command": "ls -la"},
	})

	if recs := readRecords(t, dir, "s4"); len(recs) != 0 {
		t.Fatalf("got %d records for a non-git command, want 0: %+v", len(recs), recs)
	}
}

func TestRunHookEvent_PermissionRequest(t *testing.T) {
	dir, _ := setupHookEnv(t)
	runHookEventJSON(t, "PermissionRequest", map[string]interface{}{
		"session_id": "s5", "hook_event_name": "PermissionRequest",
		"tool_name": "Bash", "tool_use_id": "tu3",
	})

	recs := readRecords(t, dir, "s5")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Event != "PermissionRequest" || r.ToolName != "Bash" || r.ToolUseID != "tu3" {
		t.Errorf("record = %+v", r)
	}
	if r.HeadSHA != "" {
		t.Errorf("HeadSHA = %q, want empty (no git call for permission events)", r.HeadSHA)
	}
}

func TestRunHookEvent_Interrupt(t *testing.T) {
	dir, _ := setupHookEnv(t)
	runHookEventJSON(t, "Interrupt", map[string]interface{}{
		"session_id": "s6", "hook_event_name": "Interrupt", "reason": "interrupted",
	})

	recs := readRecords(t, dir, "s6")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	if recs[0].Reason != "user" {
		t.Errorf("Reason = %q, want user", recs[0].Reason)
	}
}

func TestRunHookEvent_PreCompact(t *testing.T) {
	dir, _ := setupHookEnv(t)
	runHookEventJSON(t, "PreCompact", map[string]interface{}{
		"session_id": "s7", "hook_event_name": "PreCompact", "trigger": "auto",
	})

	recs := readRecords(t, dir, "s7")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	if recs[0].Reason != "auto" {
		t.Errorf("Reason = %q, want auto", recs[0].Reason)
	}
}

func TestRunHookEvent_Stop_Snapshot(t *testing.T) {
	dir, repo := setupHookEnv(t)
	writeFile(t, filepath.Join(repo, "dirty.go"), "package a\n")

	runHookEventJSON(t, "Stop", map[string]interface{}{
		"session_id": "s8", "cwd": repo, "hook_event_name": "Stop",
	})

	recs := readRecords(t, dir, "s8")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Trigger != "stop" {
		t.Errorf("Trigger = %q, want stop", r.Trigger)
	}
	if len(r.Snapshot) == 0 {
		t.Error("Snapshot is empty, want the dirty file captured")
	}
	if len(r.HeadSHA) != 40 {
		t.Errorf("HeadSHA = %q, want a 40-char sha", r.HeadSHA)
	}
}

func TestRunHookEvent_SessionEnd(t *testing.T) {
	dir, repo := setupHookEnv(t)
	runHookEventJSON(t, "SessionEnd", map[string]interface{}{
		"session_id": "s9", "cwd": repo, "hook_event_name": "SessionEnd", "reason": "logout",
	})

	recs := readRecords(t, dir, "s9")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	if recs[0].Trigger != "session_end" || recs[0].Reason != "logout" {
		t.Errorf("record = %+v", recs[0])
	}
}

func TestRunHookEvent_GarbageStdin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PAIRCLI_EVENTS_DIR", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	runErr := RunHookEvent("SessionStart", strings.NewReader("this is not json"))

	w.Close()
	os.Stdout = origStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if runErr != nil {
		t.Fatalf("RunHookEvent with garbage stdin returned an error: %v", runErr)
	}
	if buf.Len() != 0 {
		t.Errorf("stdout = %q, want empty", buf.String())
	}

	logPath := filepath.Join(home, ".paircli", "hook-errors.log")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading hook-errors.log: %v", err)
	}
	if len(logData) == 0 {
		t.Error("hook-errors.log is empty, want a logged decode error")
	}
}
