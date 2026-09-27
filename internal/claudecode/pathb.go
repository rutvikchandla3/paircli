// Package claudecode implements both capture paths for Claude Code, per
// docs/ARCHITECTURE.md's "Claude Code" section.
//
// Path B parses ~/.claude/projects/*/*.jsonl directly. Confirmed against a
// real transcript: these files have sessionId, cwd, gitBranch, timestamp,
// version, and message content on every user/assistant line, but NEVER a
// commit SHA field — so Path B sessions here are always heuristic-only
// unless our own hook (Path A, hooks.go) also ran during that session.
package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/session"
)

// transcriptLine is the subset of fields we care about from one JSONL line.
// Real lines carry many more fields (hook attachments, wireToolInputs,
// etc.) that we intentionally ignore.
type transcriptLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	GitBranch string          `json:"gitBranch"`
	Timestamp string          `json:"timestamp"`
	Version   string          `json:"version"`
	Message   json.RawMessage `json:"message"`
}

type messageEnvelope struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // string (user) or []contentBlock (assistant)
	Usage   *usage          `json:"usage"`
}

type usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"` // tool name, when Type == "tool_use"
}

// DefaultProjectsDir returns ~/.claude/projects, the root Path B scans.
func DefaultProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// ScanPathB walks projectsDir for *.jsonl transcripts and groups lines by
// sessionId into normalized Session records.
func ScanPathB(projectsDir string) ([]session.Session, error) {
	files, err := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}

	byID := map[string]*builder{}
	var order []string

	for _, f := range files {
		if err := scanFile(f, byID, &order); err != nil {
			// Best-effort: skip unreadable/malformed files rather than
			// failing the whole scan.
			continue
		}
	}

	sessions := make([]session.Session, 0, len(order))
	for _, id := range order {
		sessions = append(sessions, byID[id].finish())
	}
	return sessions, nil
}

// builder accumulates one session's worth of transcript lines.
type builder struct {
	sessionID   string
	sourcePath  string
	cwd         string
	branch      string
	models      map[string]bool
	iterations  int // count of user turns (proxy for iteration_count)
	corrections int
	toolCalls   session.ToolCallSummary
	tokens      session.TokenUsage
	start, end  time.Time
	seenTime    bool
}

func newBuilder(path string) *builder {
	return &builder{
		sourcePath: path,
		models:     map[string]bool{},
		toolCalls:  session.ToolCallSummary{},
	}
}

func scanFile(path string, byID map[string]*builder, order *[]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // transcript lines can be large
	for scanner.Scan() {
		var line transcriptLine
		raw := scanner.Bytes()
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		if line.SessionID == "" {
			continue
		}
		b, ok := byID[line.SessionID]
		if !ok {
			b = newBuilder(path)
			b.sessionID = line.SessionID
			byID[line.SessionID] = b
			*order = append(*order, line.SessionID)
		}
		b.absorb(line)
	}
	return scanner.Err()
}

func (b *builder) absorb(line transcriptLine) {
	if line.CWD != "" {
		b.cwd = line.CWD
	}
	if line.GitBranch != "" {
		b.branch = line.GitBranch
	}
	if t, err := time.Parse(time.RFC3339, line.Timestamp); err == nil {
		if !b.seenTime || t.Before(b.start) {
			b.start = t
		}
		if !b.seenTime || t.After(b.end) {
			b.end = t
		}
		b.seenTime = true
	}

	if line.Type != "user" && line.Type != "assistant" {
		return
	}
	if len(line.Message) == 0 {
		return
	}
	var msg messageEnvelope
	if err := json.Unmarshal(line.Message, &msg); err != nil {
		return
	}
	if msg.Model != "" {
		b.models[msg.Model] = true
	}
	if msg.Usage != nil {
		b.tokens.InputTokens += msg.Usage.InputTokens
		b.tokens.OutputTokens += msg.Usage.OutputTokens
		b.tokens.CachedTokens += msg.Usage.CacheReadInputTokens + msg.Usage.CacheCreationInputTokens
	}

	if line.Type == "user" && msg.Role == "user" {
		// A user line with plain string content is a real user turn.
		// Tool-result user lines (content is an array of tool_result
		// blocks) are not a human iteration and are excluded.
		var asString string
		if json.Unmarshal(msg.Content, &asString) == nil {
			b.iterations++
			// TODO: SIGNALS.md's correction_signals heuristic is
			// under-specified beyond "negative-sentiment short follow-up
			// after a tool-heavy turn"; this is a literal, minimal reading
			// of that heuristic (short + a negation/undo keyword), not a
			// sentiment model.
			if isCorrectionLike(asString) {
				b.corrections++
			}
		}
	}

	if line.Type == "assistant" {
		var blocks []contentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			for _, blk := range blocks {
				if blk.Type == "tool_use" && blk.Name != "" {
					b.toolCalls[blk.Name]++
				}
			}
		}
	}
}

// correctionKeywords is a minimal, literal reading of SIGNALS.md's
// correction_signals heuristic example phrases.
var correctionKeywords = []string{"no,", "no ", "don't", "revert", "undo", "stop", "wrong", "not that"}

func isCorrectionLike(text string) bool {
	if len(text) == 0 || len(text) > 120 {
		return false // heuristic targets "short" follow-ups only
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, kw := range correctionKeywords {
		if strings.HasPrefix(lower, kw) || strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func (b *builder) finish() session.Session {
	models := make([]string, 0, len(b.models))
	for m := range b.models {
		models = append(models, m)
	}
	sort.Strings(models)

	sess := session.Session{
		Harness:           "claude-code",
		SessionID:         b.sessionID,
		StartTime:         b.start,
		EndTime:           b.end,
		Models:            models,
		RepoPath:          b.cwd,
		Branch:            b.branch,
		IterationCount:    b.iterations,
		CorrectionSignals: b.corrections,
		ToolCallSummary:   b.toolCalls,
		TokenUsage:        b.tokens,
		CapturePath:       session.CapturePathB,
		// No SHA field exists in Claude Code transcripts (confirmed against
		// a real file and agent-beacon's source comment), so Path B alone
		// can never produce "exact" confidence for Claude Code.
		Confidence: session.ConfidenceUnknown,
		SourcePath: b.sourcePath,
	}
	return sess
}
