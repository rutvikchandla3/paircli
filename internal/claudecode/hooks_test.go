package claudecode

import (
	"bytes"
	"encoding/json"
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

func readTestSettings(t *testing.T, path string) map[string]interface{} {
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

func settingsPathIn(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("HOME", home)
	path, err := SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	return path
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
	path := settingsPathIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	settings := readTestSettings(t, path)
	for _, spec := range hookEvents {
		cmds := commandsFor(settings, spec.Event)
		if countMarked(cmds) != 1 {
			t.Errorf("event %s: got %d paircli hook commands, want 1 (%v)", spec.Event, countMarked(cmds), cmds)
			continue
		}
		if !strings.Contains(cmds[0], " hook claude-code "+spec.Event) {
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
		if entry["timeout"] != float64(10) {
			t.Errorf("event %s: timeout = %v, want 10", spec.Event, entry["timeout"])
		}
	}

	if !HooksInstalled() {
		t.Error("HooksInstalled() = false after a fresh InstallHooks")
	}
}

func TestInstall_PreservesOtherHooksAndKeys(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

	writeFile(t, path, `{
  "foo": "bar",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "other-tool on-start"}]}
    ],
    "Notification": [
      {"matcher": "info", "hooks": [{"type": "command", "command": "notify-send"}]}
    ]
  }
}`)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	settings := readTestSettings(t, path)
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

	notifCmds := commandsFor(settings, "Notification")
	if len(notifCmds) != 1 || notifCmds[0] != "notify-send" {
		t.Errorf("Notification hooks changed: %v", notifCmds)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks (1st): %v", err)
	}
	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks (2nd): %v", err)
	}

	settings := readTestSettings(t, path)
	for _, spec := range hookEvents {
		cmds := commandsFor(settings, spec.Event)
		if countMarked(cmds) != 1 {
			t.Errorf("event %s: got %d paircli hooks after two installs, want 1 (%v)", spec.Event, countMarked(cmds), cmds)
		}
	}
}

func TestInstall_ReplacesLegacyCommand(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

	writeFile(t, path, `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "/old/path/paircli hook claude-code SessionStart"}]}
    ]
  }
}`)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	settings := readTestSettings(t, path)
	cmds := commandsFor(settings, "SessionStart")
	for _, c := range cmds {
		if strings.Contains(c, "/old/path/paircli") {
			t.Errorf("legacy command not replaced: %v", cmds)
		}
	}
	if countMarked(cmds) != 1 {
		t.Errorf("got %d paircli hooks for SessionStart, want 1: %v", countMarked(cmds), cmds)
	}
}

func TestInstall_Backup(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

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

func TestInstall_NoBackupOnFreshFile(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if _, err := os.Stat(path + ".paircli.bak"); !os.IsNotExist(err) {
		t.Errorf("backup created for a settings.json that did not previously exist")
	}
}

func TestInstall_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

	writeFile(t, path, "{not valid json")

	if err := InstallHooks(); err == nil {
		t.Fatal("InstallHooks: want error for invalid existing settings.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not valid json" {
		t.Errorf("settings.json was modified despite the error: %q", data)
	}
	if _, err := os.Stat(path + ".paircli.bak"); !os.IsNotExist(err) {
		t.Error("backup created despite invalid JSON error")
	}
}

func TestUninstall(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

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

	settings := readTestSettings(t, path)
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
	settingsPathIn(t, home)

	if err := UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks on missing settings.json: %v", err)
	}
}

func TestHooksInstalled(t *testing.T) {
	home := t.TempDir()
	path := settingsPathIn(t, home)

	if HooksInstalled() {
		t.Error("HooksInstalled() = true with no settings.json")
	}

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if !HooksInstalled() {
		t.Fatal("HooksInstalled() = false after InstallHooks")
	}

	// Partially remove one event's hook to simulate a broken/partial install.
	settings := readTestSettings(t, path)
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
	all, err := hooklog.Read(dir, model.HarnessClaudeCode, time.Time{})
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

func TestRunHookEvent_UserPromptSubmit_SnapshotNoPromptText(t *testing.T) {
	dir, repo := setupHookEnv(t)
	writeFile(t, filepath.Join(repo, "dirty.go"), "package a\n")

	const promptText = "please do not leak this exact prompt string"
	runHookEventJSON(t, "UserPromptSubmit", map[string]interface{}{
		"session_id": "s2", "cwd": repo, "hook_event_name": "UserPromptSubmit", "prompt_text": promptText,
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

	// The prompt text must never reach the log file on disk.
	raw, err := os.ReadFile(filepath.Join(dir, "claude-code", time.Now().UTC().Format("2006-01-02")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), promptText) {
		t.Error("hook log file contains the raw prompt text")
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

	recs := readRecords(t, dir, "s4")
	if len(recs) != 0 {
		t.Fatalf("got %d records for a non-git command, want 0: %+v", len(recs), recs)
	}
}

func TestRunHookEvent_PermissionDenied(t *testing.T) {
	dir, _ := setupHookEnv(t)
	runHookEventJSON(t, "PermissionDenied", map[string]interface{}{
		"session_id": "s5", "hook_event_name": "PermissionDenied",
		"tool_name": "Bash", "tool_use_id": "tu3",
	})

	recs := readRecords(t, dir, "s5")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Event != "PermissionDenied" || r.ToolName != "Bash" || r.ToolUseID != "tu3" {
		t.Errorf("record = %+v", r)
	}
	if r.HeadSHA != "" {
		t.Errorf("HeadSHA = %q, want empty (no git call for permission events)", r.HeadSHA)
	}
}

func TestRunHookEvent_InstructionsLoaded(t *testing.T) {
	dir, _ := setupHookEnv(t)
	runHookEventJSON(t, "InstructionsLoaded", map[string]interface{}{
		"session_id": "s6", "hook_event_name": "InstructionsLoaded",
		"file_path": "/repo/CLAUDE.md", "load_reason": "session_start",
	})

	recs := readRecords(t, dir, "s6")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Path != "/repo/CLAUDE.md" || r.Reason != "session_start" {
		t.Errorf("record = %+v", r)
	}
}

func TestRunHookEvent_SessionEnd_ReasonFallback(t *testing.T) {
	dir, repo := setupHookEnv(t)
	runHookEventJSON(t, "SessionEnd", map[string]interface{}{
		"session_id": "s7", "cwd": repo, "hook_event_name": "SessionEnd", "reason": "other",
	})

	recs := readRecords(t, dir, "s7")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	if recs[0].Reason != "other" {
		t.Errorf("Reason = %q, want other (fallback)", recs[0].Reason)
	}

	// end_reason takes precedence when both are present.
	runHookEventJSON(t, "SessionEnd", map[string]interface{}{
		"session_id": "s7b", "cwd": repo, "hook_event_name": "SessionEnd",
		"end_reason": "clear", "reason": "other",
	})
	recs2 := readRecords(t, dir, "s7b")
	if len(recs2) != 1 || recs2[0].Reason != "clear" {
		t.Fatalf("records = %+v, want Reason clear", recs2)
	}
}

func TestRunHookEvent_LegacyArg(t *testing.T) {
	dir, repo := setupHookEnv(t)
	data, err := json.Marshal(map[string]interface{}{
		"session_id": "s8", "cwd": repo, "hook_event_name": "SessionEnd", "end_reason": "logout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := RunHookEvent("session-end", bytes.NewReader(data)); err != nil {
		t.Fatalf("RunHookEvent(session-end): %v", err)
	}

	recs := readRecords(t, dir, "s8")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	if recs[0].Event != "SessionEnd" {
		t.Errorf("Event = %q, want SessionEnd (mapped from legacy session-end)", recs[0].Event)
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
