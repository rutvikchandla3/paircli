package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/gitinfo"
)

// hookCommand is the exact command string paircli writes into settings.json
// and also what `doctor` greps for to detect an installed Path-A hook.
const hookCommand = "paircli hook claude-code"

// SettingsPath returns ~/.claude/settings.json.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// hookEntry mirrors Claude Code's real hooks config shape:
// {"hooks": {"<Event>": [{"hooks": [{"type": "command", "command": "..."}]}]}}
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Hooks []hookEntry `json:"hooks"`
}

// InstallHooks merges SessionStart/PostToolUse/SessionEnd hook entries into
// ~/.claude/settings.json without disturbing unrelated existing keys.
func InstallHooks() error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}

	settings := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("claudecode: existing settings.json is not valid JSON: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	hooksRaw, _ := settings["hooks"].(map[string]interface{})
	if hooksRaw == nil {
		hooksRaw = map[string]interface{}{}
	}

	for _, event := range []string{"SessionStart", "PostToolUse", "SessionEnd"} {
		hooksRaw[event] = mergeEventMatchers(hooksRaw[event], event)
	}

	settings["hooks"] = hooksRaw

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// mergeEventMatchers appends our hook command to the event's matcher list
// if not already present, preserving any existing matchers/hooks.
func mergeEventMatchers(existing interface{}, event string) []interface{} {
	var matchers []interface{}
	if arr, ok := existing.([]interface{}); ok {
		matchers = arr
	}

	ourCommand := fmt.Sprintf("%s %s", hookCommand, hookEventArg(event))

	for _, m := range matchers {
		matcherMap, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		hooksList, ok := matcherMap["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, h := range hooksList {
			hm, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); cmd == ourCommand {
				return matchers // already installed
			}
		}
	}

	newMatcher := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": ourCommand,
			},
		},
	}
	return append(matchers, newMatcher)
}

// hookEventArg maps Claude Code's hook event names to the <event> argument
// our own `paircli hook claude-code <event>` subcommand expects.
func hookEventArg(event string) string {
	switch event {
	case "SessionStart":
		return "session-start"
	case "PostToolUse":
		return "post-tool-use"
	case "SessionEnd":
		return "session-end"
	default:
		return event
	}
}

// HooksInstalled reports whether our hook command string is present in
// settings.json, used by `doctor`.
func HooksInstalled() bool {
	path, err := SettingsPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return containsHookCommand(data)
}

func containsHookCommand(data []byte) bool {
	return strings.Contains(string(data), hookCommand)
}

// hookStdinPayload is what Claude Code passes as JSON on stdin to hook
// commands. We only read the fields we need.
type hookStdinPayload struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
}

// EventsDir returns ~/.paircli/events/claude-code.
func EventsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".paircli", "events", "claude-code"), nil
}

// RunHookEvent implements `paircli hook claude-code <event>`: it reads the
// hook payload Claude Code sends on stdin, captures the current commit SHA
// via a real `git rev-parse HEAD` shellout (this is the exact-SHA capture
// path/ARCHITECTURE.md's Path A promises), and appends one JSON line to
// today's event log.
func RunHookEvent(event string, stdin io.Reader) error {
	var payload hookStdinPayload
	dec := json.NewDecoder(stdin)
	_ = dec.Decode(&payload) // best-effort: absent/malformed stdin shouldn't crash the hook

	cwd := payload.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	sha, _ := gitinfo.CurrentSHA(cwd) // best-effort: not every hook fires inside a git repo

	dir, err := EventsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	now := time.Now().UTC()
	logPath := filepath.Join(dir, now.Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	record := map[string]interface{}{
		"event":      event,
		"session_id": payload.SessionID,
		"cwd":        cwd,
		"timestamp":  now.Format(time.RFC3339),
		"commit_sha": sha,
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	if _, err := w.Write(line); err != nil {
		return err
	}
	if _, err := w.WriteString("\n"); err != nil {
		return err
	}
	return w.Flush()
}
