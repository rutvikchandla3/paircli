// Package codex also manages paircli's Codex CLI hook integration ("Path A"):
// installing/uninstalling hook commands in $CODEX_HOME/hooks.json and
// handling the events those hooks invoke (`paircli hook codex <Event>`).
// It deliberately never writes Codex's hook-trust approvals
// (config.toml's [hooks.state."…"] entries): only Codex itself, after the
// user reviews a new or changed hook, may do that. See
// docs/plan/tasks/T20-codex-hooks.md.
package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// hookMarker is the substring InstallHooks/UninstallHooks/HooksInstalled use
// to recognize a paircli-owned hook command, regardless of the absolute
// binary path in front of it.
const hookMarker = " hook codex "

// codexHomeEnv is the environment variable Codex CLI itself uses to relocate
// its config/state directory.
const codexHomeEnv = "CODEX_HOME"

// hookEventSpec is one Codex hook event paircli installs, with its optional
// matcher.
type hookEventSpec struct {
	Event   string
	Matcher string // "" = no matcher
}

// hookEvents is every event paircli installs a hook for (see
// docs/plan/tasks/T20-codex-hooks.md).
var hookEvents = []hookEventSpec{
	{Event: "SessionStart"},
	{Event: "UserPromptSubmit"},
	{Event: "PostToolUse", Matcher: "Bash"},
	{Event: "PermissionRequest"},
	{Event: "Interrupt"},
	{Event: "PreCompact"},
	{Event: "Stop"},
	{Event: "SessionEnd"},
}

// eventSnakeCase maps each installed event's Go/JSON (PascalCase) spelling to
// the snake_case spelling Codex uses inside config.toml's
// [hooks.state."<hooks.json>:<event>:<group>:<index>"] keys.
var eventSnakeCase = map[string]string{
	"SessionStart":      "session_start",
	"UserPromptSubmit":  "user_prompt_submit",
	"PostToolUse":       "post_tool_use",
	"PermissionRequest": "permission_request",
	"Interrupt":         "interrupt",
	"PreCompact":        "pre_compact",
	"Stop":              "stop",
	"SessionEnd":        "session_end",
}

// gitStateChangeRe matches Bash commands that change repo state, the only
// PostToolUse invocations worth recording a HookRecord for.
var gitStateChangeRe = regexp.MustCompile(`\bgit\s+(commit|merge|rebase|cherry-pick|am|revert|reset|checkout|switch|pull|stash)\b`)

// HooksPath returns $CODEX_HOME/hooks.json, or ~/.codex/hooks.json when
// CODEX_HOME is unset. Returns "" if the home directory cannot be resolved.
func HooksPath() string {
	if dir := os.Getenv(codexHomeEnv); dir != "" {
		return filepath.Join(dir, "hooks.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "hooks.json")
}

// configTomlPath returns config.toml next to HooksPath(), i.e. the file
// Codex stores hook-trust approvals in.
func configTomlPath() string {
	hp := HooksPath()
	if hp == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(hp), "config.toml")
}

// pairCLIBinary returns the command prefix InstallHooks writes into
// hooks.json: the absolute, symlink-resolved path to the running paircli
// binary, single-quoted if it contains whitespace.
func pairCLIBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if strings.ContainsAny(exe, " \t") {
		return "'" + exe + "'", nil
	}
	return exe, nil
}

// InstallHooks merges paircli's hook entries (see hookEvents) into
// $CODEX_HOME/hooks.json without disturbing unrelated existing keys or
// hooks, and without ever touching config.toml (Codex's hook-trust file):
// Codex itself must review and trust new/changed hooks before they run.
// On the first install it backs up the original file to
// hooks.json.paircli.bak (never overwriting an existing backup).
func InstallHooks() error {
	path := HooksPath()
	if path == "" {
		return fmt.Errorf("codex: could not resolve hooks.json path")
	}

	settings, raw, existed, err := readHooksFile(path)
	if err != nil {
		return err
	}
	if existed {
		if err := backupOnce(path, raw); err != nil {
			return err
		}
	}

	bin, err := pairCLIBinary()
	if err != nil {
		return err
	}

	hooksRaw, _ := settings["hooks"].(map[string]interface{})
	if hooksRaw == nil {
		hooksRaw = map[string]interface{}{}
	}
	for _, spec := range hookEvents {
		cmd := fmt.Sprintf("%s hook codex %s", bin, spec.Event)
		hooksRaw[spec.Event] = installEvent(hooksRaw[spec.Event], spec.Matcher, cmd)
	}
	settings["hooks"] = hooksRaw

	return writeHooksFile(path, settings)
}

// InstallMessage is printed by `paircli hook install codex` after a
// successful install: Codex withholds new hooks from running until the user
// reviews and trusts them inside Codex itself.
func InstallMessage() string {
	return "Codex needs you to review and trust new hooks before they run. Start codex and approve the paircli hooks when prompted, then run 'paircli doctor'."
}

// UninstallHooks removes paircli's hook entries from $CODEX_HOME/hooks.json,
// dropping matcher groups left with no hooks and events left with no matcher
// groups. Preserves everything else, including config.toml. A missing
// hooks.json is not an error.
func UninstallHooks() error {
	path := HooksPath()
	if path == "" {
		return fmt.Errorf("codex: could not resolve hooks.json path")
	}

	settings, _, existed, err := readHooksFile(path)
	if err != nil {
		return err
	}
	if !existed {
		return nil
	}

	hooksRaw, _ := settings["hooks"].(map[string]interface{})
	if hooksRaw == nil {
		return nil
	}
	for _, spec := range hookEvents {
		existing, ok := hooksRaw[spec.Event]
		if !ok {
			continue
		}
		remaining := removeOurHooks(existing)
		if len(remaining) == 0 {
			delete(hooksRaw, spec.Event)
		} else {
			hooksRaw[spec.Event] = remaining
		}
	}
	settings["hooks"] = hooksRaw

	return writeHooksFile(path, settings)
}

// HooksInstalled reports whether every event in hookEvents has a hook whose
// command contains " hook codex <Event>".
func HooksInstalled() bool {
	path := HooksPath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}
	hooksRaw, _ := settings["hooks"].(map[string]interface{})
	if hooksRaw == nil {
		return false
	}
	for _, spec := range hookEvents {
		want := " hook codex " + spec.Event
		if !eventHasHookCommand(hooksRaw[spec.Event], want) {
			return false
		}
	}
	return true
}

// hookPosition locates one of paircli's own hook commands inside hooks.json:
// its event name, and its (matcher group, hook) indices within that event's
// array — the same coordinates Codex uses in config.toml's hook-trust keys.
type hookPosition struct {
	Event string
	Group int
	Index int
}

// ourHookPositions scans the current hooks.json for entries whose command
// contains hookMarker, returning their positions sorted by (event, group,
// index). A missing hooks.json yields (nil, nil).
func ourHookPositions() ([]hookPosition, error) {
	path := HooksPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var positions []hookPosition
	for event, groups := range doc.Hooks {
		for g, group := range groups {
			for i, h := range group.Hooks {
				if strings.Contains(h.Command, hookMarker) {
					positions = append(positions, hookPosition{Event: event, Group: g, Index: i})
				}
			}
		}
	}
	sort.Slice(positions, func(a, b int) bool {
		if positions[a].Event != positions[b].Event {
			return positions[a].Event < positions[b].Event
		}
		if positions[a].Group != positions[b].Group {
			return positions[a].Group < positions[b].Group
		}
		return positions[a].Index < positions[b].Index
	})
	return positions, nil
}

// trustStateSectionRe matches a config.toml [hooks.state."<key>"] section
// header.
var trustStateSectionRe = regexp.MustCompile(`^\[hooks\.state\."([^"]*)"\]$`)

// trustedHashLineRe matches the trusted_hash key inside a hooks.state section.
var trustedHashLineRe = regexp.MustCompile(`^trusted_hash\s*=`)

// parseTrustedKeys does a read-only, line-based scan of config.toml,
// returning the set of "<hooks.json>:<event>:<group>:<index>" keys that have
// a trusted_hash under them. It never modifies config.toml and tolerates a
// missing or unparsable file (returns an empty set).
func parseTrustedKeys(path string) map[string]bool {
	trusted := map[string]bool{}
	if path == "" {
		return trusted
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return trusted
	}

	var current string
	inHooksState := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if m := trustStateSectionRe.FindStringSubmatch(line); m != nil {
				current = m[1]
				inHooksState = true
			} else {
				current = ""
				inHooksState = false
			}
			continue
		}
		if inHooksState && trustedHashLineRe.MatchString(line) {
			trusted[current] = true
		}
	}
	return trusted
}

// HookTrustState does a read-only scan of config.toml and reports how many
// of paircli's current hooks.json hook entries Codex has recorded a
// trusted_hash for (trusted), out of how many paircli hooks currently exist
// in hooks.json (total). paircli never writes to config.toml itself.
func HookTrustState() (trusted, total int) {
	positions, err := ourHookPositions()
	if err != nil || len(positions) == 0 {
		return 0, 0
	}
	total = len(positions)

	hooksPath := HooksPath()
	trustedKeys := parseTrustedKeys(configTomlPath())
	for _, p := range positions {
		key := fmt.Sprintf("%s:%s:%d:%d", hooksPath, eventSnakeCase[p.Event], p.Group, p.Index)
		if trustedKeys[key] {
			trusted++
		}
	}
	return trusted, total
}

// readHooksFile reads hooks.json, returning an empty map (existed=false)
// when the file does not exist. Invalid JSON is an error; nothing is changed.
func readHooksFile(path string) (settings map[string]interface{}, raw []byte, existed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil, false, nil
		}
		return nil, nil, false, err
	}
	settings = map[string]interface{}{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, nil, false, fmt.Errorf("codex: existing hooks.json is not valid JSON: %w", err)
	}
	return settings, data, true, nil
}

// backupOnce copies raw to path+".paircli.bak" unless that backup already exists.
func backupOnce(path string, raw []byte) error {
	backupPath := path + ".paircli.bak"
	if _, err := os.Stat(backupPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(backupPath, raw, 0o644)
}

// writeHooksFile marshals settings with 2-space indent to a temp file in
// path's directory, then renames it into place.
func writeHooksFile(path string, settings map[string]interface{}) error {
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".paircli-hooks-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// installEvent returns existing's matcher groups with any paircli-owned hook
// command removed (see hookMarker), plus a new group holding cmd under
// matcher (matcher == "" omits the field).
func installEvent(existing interface{}, matcher, cmd string) []interface{} {
	matchers := stripOurHooks(existing)

	entry := map[string]interface{}{
		"type":    "command",
		"command": cmd,
		"timeout": 10,
	}
	group := map[string]interface{}{
		"hooks": []interface{}{entry},
	}
	if matcher != "" {
		group["matcher"] = matcher
	}
	return append(matchers, group)
}

// removeOurHooks is stripOurHooks with a nil (not empty-slice) result when
// nothing remains, so callers can delete the event key.
func removeOurHooks(existing interface{}) []interface{} {
	remaining := stripOurHooks(existing)
	if len(remaining) == 0 {
		return nil
	}
	return remaining
}

// stripOurHooks returns existing's matcher groups with any hook whose
// command contains hookMarker removed, and matcher groups left with no
// hooks dropped entirely. Non-map entries are preserved as-is.
func stripOurHooks(existing interface{}) []interface{} {
	arr, ok := existing.([]interface{})
	if !ok {
		return nil
	}
	var out []interface{}
	for _, m := range arr {
		mm, ok := m.(map[string]interface{})
		if !ok {
			out = append(out, m)
			continue
		}
		hooksList, _ := mm["hooks"].([]interface{})
		var kept []interface{}
		for _, h := range hooksList {
			hm, ok := h.(map[string]interface{})
			if !ok {
				kept = append(kept, h)
				continue
			}
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, hookMarker) {
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			continue
		}
		mm["hooks"] = kept
		out = append(out, mm)
	}
	return out
}

// eventHasHookCommand reports whether any hook under existing's matcher
// groups has a command containing substr.
func eventHasHookCommand(existing interface{}, substr string) bool {
	arr, ok := existing.([]interface{})
	if !ok {
		return false
	}
	for _, m := range arr {
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
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, substr) {
				return true
			}
		}
	}
	return false
}

// hookStdinPayload is the subset of fields paircli reads from the JSON Codex
// sends on stdin to hook commands, across every installed event.
type hookStdinPayload struct {
	SessionID string          `json:"session_id"`
	CWD       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
	Source    string          `json:"source"`
	Trigger   string          `json:"trigger"`
	Reason    string          `json:"reason"`
}

// RunHookEvent implements `paircli hook codex <Event>`: it decodes the hook
// payload Codex sends on stdin, builds one model.HookRecord, and appends it
// to today's hook log (internal/hooklog). It never returns a non-nil error
// and never prints to stdout: every failure is logged to
// ~/.paircli/hook-errors.log so a broken hook can never break the calling
// agent.
func RunHookEvent(event string, stdin io.Reader) error {
	var payload hookStdinPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		logHookError(fmt.Errorf("codex: decode hook stdin for %s: %w", event, err))
		return nil
	}

	rec := model.HookRecord{
		Harness:   model.HarnessCodex,
		Event:     event,
		SessionID: payload.SessionID,
		TS:        time.Now().UTC(),
		CWD:       payload.CWD,
	}

	needsGit := false
	switch event {
	case "SessionStart":
		rec.Trigger = "session_start"
		rec.Reason = payload.Source
		needsGit = true
	case "UserPromptSubmit":
		rec.Trigger = "prompt"
		needsGit = true
	case "PostToolUse":
		cmd := extractBashCommand(payload.ToolInput)
		if !gitStateChangeRe.MatchString(cmd) {
			return nil // nothing worth recording
		}
		rec.Trigger = "commit"
		rec.Command = model.Clip(cmd, 300)
		rec.ToolName = payload.ToolName
		rec.ToolUseID = payload.ToolUseID
		needsGit = true
	case "PermissionRequest":
		rec.ToolName = payload.ToolName
		rec.ToolUseID = payload.ToolUseID
	case "Interrupt":
		rec.Reason = "user"
	case "PreCompact":
		rec.Reason = payload.Trigger
	case "Stop":
		rec.Trigger = "stop"
		needsGit = true
	case "SessionEnd":
		rec.Trigger = "session_end"
		rec.Reason = payload.Reason
		needsGit = true
	}

	cwd := rec.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	if needsGit {
		sha, branch, err := gitinfo.HeadAndBranch(cwd)
		if err != nil {
			rec.Error = err.Error()
		} else {
			rec.HeadSHA = sha
			rec.Branch = branch
		}
	}

	if event == "UserPromptSubmit" || event == "Stop" {
		files, err := hooklog.Snapshot(cwd)
		if err != nil {
			if rec.Error == "" {
				rec.Error = err.Error()
			}
		} else {
			rec.Snapshot = files
		}
	}

	if err := hooklog.Append(hooklog.DefaultDir(), rec); err != nil {
		logHookError(fmt.Errorf("codex: append hook record: %w", err))
	}
	return nil
}

// extractBashCommand reads "command" out of a Bash tool_input payload.
func extractBashCommand(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.Command
}

// logHookError appends one timestamped line to ~/.paircli/hook-errors.log.
// It is itself best-effort: a hook must never fail loudly, even when it
// cannot even log why.
func logHookError(err error) {
	home, herr := os.UserHomeDir()
	if herr != nil {
		return
	}
	dir := filepath.Join(home, ".paircli")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return
	}
	path := filepath.Join(dir, "hook-errors.log")
	f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %v\n", time.Now().UTC().Format(time.RFC3339), err)
}
