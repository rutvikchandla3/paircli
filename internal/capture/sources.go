package capture

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// Kinds of source file in the manifest.
const (
	KindMain     = "main"
	KindSubagent = "subagent"
)

// source is one transcript file that belongs to a session.
type source struct {
	path string
	kind string
}

// sourcesFor lists the files a session's raw transcript lives in: the main
// file; for Claude Code also every sibling file that continues the session
// (a resumed session writes a new file that reuses its session id) and the
// session's subagent transcripts. Order is deterministic: main, continuations,
// subagents, each sorted by path.
func sourcesFor(s *model.Session) []source {
	if s.SourcePath == "" {
		return nil
	}
	out := []source{{s.SourcePath, KindMain}}
	if s.Harness != model.HarnessClaudeCode {
		return out
	}
	dir := filepath.Dir(s.SourcePath)
	if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return out
	}
	var continued []string
	siblings, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, path := range siblings {
		if path == s.SourcePath {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(s.Start) {
			continue
		}
		if firstSessionID(path) == s.ID {
			continued = append(continued, path)
		}
	}
	sort.Strings(continued)
	for _, path := range continued {
		out = append(out, source{path, KindMain})
	}
	subagents, _ := filepath.Glob(filepath.Join(dir, s.ID, "subagents", "agent-*.jsonl"))
	sort.Strings(subagents)
	for _, path := range subagents {
		out = append(out, source{path, KindSubagent})
	}
	return out
}

// firstSessionID returns the sessionId of the first transcript line that has
// one, or "".
func firstSessionID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	for i := 0; i < 50; i++ {
		line, err := r.ReadBytes('\n')
		var entry struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.SessionID != "" {
			return entry.SessionID
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

// resolve returns the real path of a regular, non-symlink transcript file.
func resolve(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	return filepath.EvalSymlinks(path)
}

func pathKey(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

var unsafeComponent = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// safeComponent makes value usable as one path element of a zip entry.
func safeComponent(value string) string {
	cleaned := strings.Trim(unsafeComponent.ReplaceAllString(value, "-"), ".-")
	if len(cleaned) > 120 {
		cleaned = cleaned[:120]
	}
	if cleaned == "" {
		return pathKey(value)[:16]
	}
	return cleaned
}
