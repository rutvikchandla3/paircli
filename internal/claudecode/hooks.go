package claudecode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// hookMarker is the substring InstallHooks/UninstallHooks/HooksInstalled use
// to recognize a paircli-owned hook command, regardless of the absolute
// binary path in front of it. It also matches older (Path-A v1) installs.
const hookMarker = " hook claude-code "

// hookEventSpec is one Claude Code hook event paircli installs, with its
// optional matcher.
type hookEventSpec struct {
	Event   string
	Matcher string // "" = no matcher
}

// hookEvents is every event paircli installs a hook for (see
// docs/plan/tasks/T08-hooklog-claude-hooks.md).
var hookEvents = []hookEventSpec{
	{Event: "SessionStart"},
	{Event: "UserPromptSubmit"},
	{Event: "PostToolUse", Matcher: "Bash"},
	{Event: "PermissionRequest"},
	{Event: "PermissionDenied"},
	{Event: "InstructionsLoaded"},
	{Event: "CwdChanged"},
	{Event: "PreCompact"},
	{Event: "Stop"},
	{Event: "SessionEnd"},
}

// legacyEventArgs maps pre-v2 `paircli hook claude-code <arg>` argument
// spellings to the real Claude Code event names, so an old settings.json
// (or a stale process) keeps working until reinstalled.
var legacyEventArgs = map[string]string{
	"session-start": "SessionStart",
	"post-tool-use": "PostToolUse",
	"session-end":   "SessionEnd",
}

// gitStateChangeRe matches Bash commands that change repo state, the only
// PostToolUse invocations worth recording a HookRecord for.
var gitStateChangeRe = regexp.MustCompile(`\bgit\s+(commit|merge|rebase|cherry-pick|am|revert|reset|checkout|switch|pull|stash)\b`)

// SettingsPath returns ~/.claude/settings.json.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// EventsDir returns hooklog.DefaultDir()/claude-code, i.e. where RunHookEvent
// actually writes records via hooklog.Append. Kept for the legacy v1
// cmd/paircli/scan.go (removed by T24), which reads this directory directly;
// not part of the T08 contract otherwise.
func EventsDir() (string, error) {
	dir := hooklog.DefaultDir()
	if dir == "" {
		return "", fmt.Errorf("claudecode: could not resolve hook events dir")
	}
	return filepath.Join(dir, "claude-code"), nil
}

// pairCLIBinary returns the command prefix InstallHooks writes into
// settings.json: the absolute, symlink-resolved path to the running paircli
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
// ~/.claude/settings.json without disturbing unrelated existing keys or
// hooks. On the first install it backs up the original file to
// settings.json.paircli.bak (never overwriting an existing backup).
func InstallHooks() error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}

	settings, raw, existed, err := readSettings(path)
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
		cmd := fmt.Sprintf("%s hook claude-code %s", bin, spec.Event)
		hooksRaw[spec.Event] = installEvent(hooksRaw[spec.Event], spec.Matcher, cmd)
	}
	settings["hooks"] = hooksRaw

	return writeSettings(path, settings)
}

// UninstallHooks removes paircli's hook entries from ~/.claude/settings.json,
// dropping matcher groups left with no hooks and events left with no
// matcher groups. Preserves everything else. A missing settings.json is not
// an error.
func UninstallHooks() error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}

	settings, _, existed, err := readSettings(path)
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

	return writeSettings(path, settings)
}

// HooksInstalled reports whether every event in hookEvents has a hook whose
// command contains " hook claude-code <Event>".
func HooksInstalled() bool {
	path, err := SettingsPath()
	if err != nil {
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
		want := " hook claude-code " + spec.Event
		if !eventHasHookCommand(hooksRaw[spec.Event], want) {
			return false
		}
	}
	return true
}

// readSettings reads settings.json, returning an empty map (existed=false)
// when the file does not exist. Invalid JSON is an error; nothing is changed.
func readSettings(path string) (settings map[string]interface{}, raw []byte, existed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil, false, nil
		}
		return nil, nil, false, err
	}
	settings = map[string]interface{}{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, nil, false, fmt.Errorf("claudecode: existing settings.json is not valid JSON: %w", err)
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

// writeSettings marshals settings with 2-space indent to a temp file in
// path's directory, then renames it into place.
func writeSettings(path string, settings map[string]interface{}) error {
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".paircli-settings-*.tmp")
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

// hookStdinPayload is the subset of fields paircli reads from the JSON
// Claude Code sends on stdin to hook commands, across every installed event.
type hookStdinPayload struct {
	SessionID     string          `json:"session_id"`
	CWD           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	Source        string          `json:"source"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolUseID     string          `json:"tool_use_id"`
	FilePath      string          `json:"file_path"`
	LoadReason    string          `json:"load_reason"`
	PreviousCWD   string          `json:"previous_cwd"`
	CompactReason string          `json:"compact_reason"`
	Trigger       string          `json:"trigger"`
	EndReason     string          `json:"end_reason"`
	Reason        string          `json:"reason"`
}

// RunHookEvent implements `paircli hook claude-code <Event>`: it decodes the
// hook payload Claude Code sends on stdin, builds one model.HookRecord, and
// appends it to today's hook log (internal/hooklog). It never returns a
// non-nil error to the caller and never prints to stdout: every failure is
// logged to ~/.paircli/hook-errors.log so a broken hook can never break the
// calling agent.
func RunHookEvent(event string, stdin io.Reader) error {
	if mapped, ok := legacyEventArgs[event]; ok {
		event = mapped
	}

	var payload hookStdinPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		logHookError(fmt.Errorf("claudecode: decode hook stdin for %s: %w", event, err))
		return nil
	}

	rec := model.HookRecord{
		Harness:   model.HarnessClaudeCode,
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
	case "PermissionDenied":
		rec.ToolName = payload.ToolName
		rec.ToolUseID = payload.ToolUseID
	case "InstructionsLoaded":
		rec.Path = payload.FilePath
		rec.Reason = payload.LoadReason
	case "CwdChanged":
		rec.CwdFrom = payload.PreviousCWD
	case "PreCompact":
		rec.Reason = firstNonEmpty(payload.CompactReason, payload.Trigger)
	case "Stop":
		rec.Trigger = "stop"
		needsGit = true
	case "SessionEnd":
		rec.Trigger = "session_end"
		rec.Reason = firstNonEmpty(payload.EndReason, payload.Reason)
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
		logHookError(fmt.Errorf("claudecode: append hook record: %w", err))
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
