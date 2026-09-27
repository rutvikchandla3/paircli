// Package claudecode implements the Claude Code capture paths described in
// docs/ARCHITECTURE.md. This file (transcript.go, plus transcript_tools.go)
// is Path B: it reads ~/.claude/projects/*/*.jsonl transcripts (and their
// subagent transcripts) and normalizes them into model.Session timelines per
// docs/plan/formats/claude-code.md. It never modifies pathb.go or hooks.go.
package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// DefaultRoot returns ~/.claude/projects, the root Discover scans.
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// Discover returns main transcript files under root (root/*/*.jsonl) with
// ModTime() >= since (zero since = all), sorted. It never returns files
// under */subagents/. A missing root is not an error.
func Discover(root string, since time.Time) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("claudecode: discover %s: %w", root, err)
	}
	var out []string
	for _, m := range matches {
		if strings.Contains(filepath.ToSlash(m), "/subagents/") {
			continue
		}
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		if !since.IsZero() && info.ModTime().Before(since) {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// ParseFile parses one main transcript into sessions (several when the file
// mixes sessionIds), loads each session's subagent transcripts, and returns
// them sorted by Start.
func ParseFile(path string) ([]*model.Session, error) {
	lines, err := ccReadLines(path)
	if err != nil {
		return nil, fmt.Errorf("claudecode: parse %s: %w", path, err)
	}

	builders := map[string]*ccBuilder{}
	var order []string
	seenUUID := map[string]bool{}
	lastSession := ""

	for _, raw := range lines {
		var e ccEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue // malformed line: skip, keep going
		}
		sid := e.SessionID
		if sid == "" {
			sid = lastSession
		}
		if sid == "" {
			continue // no session context yet
		}
		lastSession = sid

		if e.UUID != "" {
			if seenUUID[e.UUID] {
				continue
			}
			seenUUID[e.UUID] = true
		}

		b, ok := builders[sid]
		if !ok {
			b = newCCBuilder(sid, path)
			builders[sid] = b
			order = append(order, sid)
		}
		b.absorb(e, path)
	}

	out := make([]*model.Session, 0, len(order))
	for _, sid := range order {
		sess := builders[sid].finish()
		loadSubagents(sess, path)
		sess.Finalize()
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// ccReadLines reads path line by line, tolerating lines up to 64 MB.
func ccReadLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	var lines [][]byte
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(bytesTrimSpace(raw)) == 0 {
			continue
		}
		cp := make([]byte, len(raw))
		copy(cp, raw)
		lines = append(lines, cp)
	}
	if err := scanner.Err(); err != nil {
		return lines, err
	}
	return lines, nil
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// ccEntry is the common envelope for one JSONL line. Unknown fields and
// unknown "type" values are ignored, never errors: the format is
// undocumented and changes between releases.
type ccEntry struct {
	Type             string          `json:"type"`
	UUID             string          `json:"uuid"`
	SessionID        string          `json:"sessionId"`
	Timestamp        string          `json:"timestamp"`
	CWD              string          `json:"cwd"`
	GitBranch        string          `json:"gitBranch"`
	Version          string          `json:"version"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	PromptSource     string          `json:"promptSource"`
	Origin           *ccOrigin       `json:"origin"`
	Message          json.RawMessage `json:"message"`
	ToolUseResult    json.RawMessage `json:"toolUseResult"`
	Attachment       json.RawMessage `json:"attachment"`
	CompactMetadata  json.RawMessage `json:"compactMetadata"`
	Content          string          `json:"content"` // system entries only; user/assistant content lives under message
	Subtype          string          `json:"subtype"`
	PermissionMode   string          `json:"permissionMode"`
	AiTitle          string          `json:"aiTitle"`
	Effort           string          `json:"effort"`
}

type ccOrigin struct {
	Kind string `json:"kind"`
}

type ccMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // string or []ccBlock
	Usage   *ccUsage        `json:"usage"`
}

type ccUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ccBlock covers text, tool_use, tool_result and image content blocks.
type ccBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`   // tool_use
	Name      string          `json:"name"` // tool_use
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result: string or []ccBlock
	IsError   bool            `json:"is_error"`
	Source    *ccImageSource  `json:"source"` // image
}

type ccImageSource struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// pendingCall is a tool_use block waiting for its tool_result.
type pendingCall struct {
	ID    string
	Name  string
	Input json.RawMessage
	TS    time.Time
	Model string
}

// ccBuilder accumulates one session's worth of transcript lines.
type ccBuilder struct {
	session *model.Session

	cwd     string
	branch  string
	version string
	title   string

	lastTS         time.Time
	lastPermission model.PermissionMode
	lastModel      string
	lastEffort     string

	pending      map[string]*pendingCall
	pendingOrder []string

	lastCompactionIdx int // index into session.Events, -1 if none yet
	openBashCmd       int // index of a ByUser command awaiting <bash-stdout>, -1 if none

	kindCounters map[model.EventKind]int
}

func newCCBuilder(sessionID, sourcePath string) *ccBuilder {
	return &ccBuilder{
		session:           &model.Session{Harness: model.HarnessClaudeCode, ID: sessionID, SourcePath: sourcePath},
		pending:           map[string]*pendingCall{},
		kindCounters:      map[model.EventKind]int{},
		lastCompactionIdx: -1,
		openBashCmd:       -1,
	}
}

func (b *ccBuilder) emit(ev model.Event) {
	ev.Origin = model.OriginTranscript
	b.session.Events = append(b.session.Events, ev)
}

// fallbackID assigns "<kind>-<n>" counting from 1 per kind, for entries with
// no natural id.
func (b *ccBuilder) fallbackID(kind model.EventKind) string {
	b.kindCounters[kind]++
	return fmt.Sprintf("%s-%d", kind, b.kindCounters[kind])
}

// idFor returns the entry's uuid, or a generated fallback id.
func (b *ccBuilder) idFor(e ccEntry, kind model.EventKind) string {
	if e.UUID != "" {
		return e.UUID
	}
	return b.fallbackID(kind)
}

// idForSuffixed returns "<uuid>/<n>", the form attachments (and multi-event
// entries) use.
func (b *ccBuilder) idForSuffixed(e ccEntry, n int) string {
	if e.UUID != "" {
		return fmt.Sprintf("%s/%d", e.UUID, n)
	}
	return b.fallbackID("attachment") + fmt.Sprintf("/%d", n)
}

// resolveTS parses the entry's timestamp; when absent it falls back to the
// most recently resolved timestamp in the file (permission-mode entries
// sometimes omit it).
func (b *ccBuilder) resolveTS(e ccEntry) time.Time {
	if e.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
			t = t.UTC()
			b.lastTS = t
			return t
		}
	}
	return b.lastTS
}

func (b *ccBuilder) absorb(e ccEntry, sourcePath string) {
	ts := b.resolveTS(e)

	if e.CWD != "" {
		b.cwd = e.CWD
	}
	if e.GitBranch != "" {
		b.branch = e.GitBranch
	}
	if e.Version != "" {
		b.version = e.Version
	}

	switch e.Type {
	case "user":
		b.handleUser(e, ts, sourcePath)
	case "assistant":
		b.handleAssistant(e, ts)
	case "permission-mode":
		b.handlePermissionMode(e, ts)
	case "attachment":
		b.handleAttachment(e, ts)
	case "system":
		b.handleSystem(e, ts)
	case "ai-title":
		if e.AiTitle != "" {
			b.title = e.AiTitle
		}
	default:
		// file-history-snapshot, file-history-delta, queue-operation,
		// last-prompt, mode, atis-latch, summary, and any unknown type: ignore.
	}
}

// --- type: "user" ---

func (b *ccBuilder) handleUser(e ccEntry, ts time.Time, sourcePath string) {
	if len(e.Message) == 0 {
		return
	}
	var msg ccMessage
	if err := json.Unmarshal(e.Message, &msg); err != nil {
		return
	}

	var asString string
	if json.Unmarshal(msg.Content, &asString) == nil {
		b.handleUserString(e, ts, asString)
		return
	}
	b.handleUserArray(e, ts, msg.Content, sourcePath)
}

var ignoredContentPrefixes = []string{
	"<local-command-stdout>",
	"<local-command-caveat>",
	"<local-command-stderr>",
	"<task-notification>",
	"<system-reminder>",
}

func (b *ccBuilder) handleUserString(e ccEntry, ts time.Time, content string) {
	// A following <bash-stdout>/<bash-stderr> entry completes an open
	// ByUser command rather than following the normal ladder below.
	if b.openBashCmd >= 0 && (strings.Contains(content, "<bash-stdout>") || strings.Contains(content, "<bash-stderr>")) {
		b.completeUserBash(content)
		return
	}

	if e.IsCompactSummary {
		b.attachCompactionSummary(e, ts, content)
		return
	}

	if name, args, ok := extractSlashCommand(content); ok {
		b.handleSlashCommand(e, ts, name, args)
		return
	}

	for _, p := range ignoredContentPrefixes {
		if strings.HasPrefix(content, p) {
			return
		}
	}

	if inner, ok := extractTag(content, "bash-input"); ok {
		idx := len(b.session.Events)
		b.emit(model.Event{
			ID:      b.idFor(e, model.KindCommand),
			Kind:    model.KindCommand,
			TS:      ts,
			Command: &model.Command{Cmd: inner, Status: model.CmdUnknown, ByUser: true},
		})
		b.openBashCmd = idx
		return
	}

	if e.IsMeta {
		return
	}

	if isHumanEntry(e, content) {
		b.emit(model.Event{
			ID:     b.idFor(e, model.KindPrompt),
			Kind:   model.KindPrompt,
			TS:     ts,
			Prompt: &model.Prompt{Text: content},
		})
	}
}

func isHumanEntry(e ccEntry, content string) bool {
	if e.PromptSource == "typed" {
		return true
	}
	if e.Origin != nil && e.Origin.Kind == "human" {
		return true
	}
	if e.Origin == nil && e.PromptSource == "" && !strings.HasPrefix(content, "<") {
		return true
	}
	return false
}

var settingsSlashCommands = map[string]bool{
	"/model": true, "/effort": true, "/config": true, "/context": true, "/cost": true,
	"/help": true, "/status": true, "/resume": true, "/login": true, "/logout": true,
	"/exit": true, "/permissions": true, "/hooks": true, "/memory": true, "/doctor": true,
	"/ide": true, "/theme": true, "/usage": true, "/agents": true, "/mcp": true,
	"/plugin": true, "/fast": true, "/statusline": true, "/terminal-setup": true,
	"/vim": true, "/add-dir": true, "/release-notes": true, "/bug": true, "/feedback": true,
}

func (b *ccBuilder) handleSlashCommand(e ccEntry, ts time.Time, name, args string) {
	switch {
	case name == "/clear":
		b.emit(model.Event{ID: b.idFor(e, model.KindReset), Kind: model.KindReset, TS: ts, Reset: &model.Reset{Type: "clear"}})
	case name == "/compact":
		// the compact_boundary system entry records this
	case settingsSlashCommands[name]:
		// settings/info command: ignore
	default:
		b.emit(model.Event{
			ID:     b.idFor(e, model.KindPrompt),
			Kind:   model.KindPrompt,
			TS:     ts,
			Prompt: &model.Prompt{SlashCommand: name, Text: args},
		})
	}
}

func (b *ccBuilder) completeUserBash(content string) {
	idx := b.openBashCmd
	b.openBashCmd = -1
	if idx < 0 || idx >= len(b.session.Events) || b.session.Events[idx].Command == nil {
		return
	}
	cmd := b.session.Events[idx].Command
	stdout, _ := extractTag(content, "bash-stdout")
	stderr, _ := extractTag(content, "bash-stderr")
	out := stdout
	if stderr != "" {
		if out != "" {
			out += "\n"
		}
		out += stderr
	}
	cmd.Output = model.TruncateOutput(out)
	if stdout == "" && stderr != "" {
		cmd.Status = model.CmdFailed
	} else {
		cmd.Status = model.CmdOK
	}
}

func (b *ccBuilder) attachCompactionSummary(e ccEntry, ts time.Time, content string) {
	if b.lastCompactionIdx >= 0 && b.lastCompactionIdx < len(b.session.Events) && b.session.Events[b.lastCompactionIdx].Compaction != nil {
		b.session.Events[b.lastCompactionIdx].Compaction.Summary = content
		return
	}
	b.lastCompactionIdx = len(b.session.Events)
	b.emit(model.Event{
		ID:         b.idFor(e, model.KindCompaction),
		Kind:       model.KindCompaction,
		TS:         ts,
		Compaction: &model.Compaction{Summary: content},
	})
}

func (b *ccBuilder) handleUserArray(e ccEntry, ts time.Time, raw json.RawMessage, sourcePath string) {
	var blocks []ccBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return
	}

	var texts []string
	interrupted := false
	images := 0

	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			if blk.Text == "[Request interrupted by user]" || strings.HasPrefix(blk.Text, "[Request interrupted by user") {
				interrupted = true
				continue
			}
			if blk.Text != "" {
				texts = append(texts, blk.Text)
			}
		case "image":
			images++
		case "tool_result":
			b.completeToolUse(blk, ts, e.ToolUseResult, sourcePath)
		}
	}

	if interrupted {
		b.emit(model.Event{ID: b.idFor(e, model.KindInterrupt), Kind: model.KindInterrupt, TS: ts, Interrupt: &model.Interrupt{Reason: "user"}})
		return
	}

	if len(texts) > 0 {
		joined := strings.Join(texts, "\n")
		if isHumanEntry(e, joined) {
			b.emit(model.Event{
				ID:     b.idFor(e, model.KindPrompt),
				Kind:   model.KindPrompt,
				TS:     ts,
				Prompt: &model.Prompt{Text: joined, Images: images},
			})
		}
	}
}

// --- type: "assistant" ---

func (b *ccBuilder) handleAssistant(e ccEntry, ts time.Time) {
	if len(e.Message) == 0 {
		return
	}
	var msg ccMessage
	if err := json.Unmarshal(e.Message, &msg); err != nil {
		return
	}

	if msg.Model != "" && msg.Model != "<synthetic>" {
		if msg.Model != b.lastModel || e.Effort != b.lastEffort {
			b.lastModel = msg.Model
			b.lastEffort = e.Effort
			b.emit(model.Event{
				ID:          b.idFor(e, model.KindModelChange),
				Kind:        model.KindModelChange,
				TS:          ts,
				Model:       msg.Model,
				ModelChange: &model.ModelChange{Model: msg.Model, Effort: e.Effort},
			})
		}
	}

	var blocks []ccBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}

	textCount := 0
	for _, blk := range blocks {
		if blk.Type == "text" && blk.Text != "" {
			textCount++
		}
	}
	textSeen := 0
	firstMsg := true

	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			if blk.Text == "" {
				continue
			}
			var id string
			if textCount > 1 {
				id = b.idForSuffixed(e, textSeen)
			} else {
				id = b.idFor(e, model.KindMessage)
			}
			textSeen++
			m := &model.Message{Text: blk.Text}
			if firstMsg {
				m.Usage = ccUsageToModel(msg.Usage)
				firstMsg = false
			}
			b.emit(model.Event{ID: id, Kind: model.KindMessage, TS: ts, Model: msg.Model, Message: m})
		case "tool_use":
			if blk.ID == "" {
				continue
			}
			b.pending[blk.ID] = &pendingCall{ID: blk.ID, Name: blk.Name, Input: blk.Input, TS: ts, Model: msg.Model}
			b.pendingOrder = append(b.pendingOrder, blk.ID)
		default:
			// thinking, redacted_thinking: ignore
		}
	}
}

func ccUsageToModel(u *ccUsage) *model.Usage {
	if u == nil {
		return nil
	}
	return &model.Usage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
}

// --- type: "permission-mode" ---

func (b *ccBuilder) handlePermissionMode(e ccEntry, ts time.Time) {
	norm, ok := normalizePermission(e.PermissionMode)
	if !ok || norm == b.lastPermission {
		return
	}
	b.lastPermission = norm
	b.emit(model.Event{
		ID:   b.idFor(e, model.KindMode),
		Kind: model.KindMode,
		TS:   ts,
		Mode: &model.Mode{Permission: norm, RawPermission: e.PermissionMode},
	})
}

func normalizePermission(raw string) (model.PermissionMode, bool) {
	switch raw {
	case "":
		return "", false
	case "default":
		return model.PermAsk, true
	case "acceptEdits":
		return model.PermAcceptEdits, true
	case "plan":
		return model.PermPlan, true
	case "auto":
		return model.PermAuto, true
	case "bypassPermissions", "dontAsk":
		return model.PermBypass, true
	default:
		return model.PermissionMode(raw), true
	}
}

// --- type: "attachment" ---

type ccAttachmentEnvelope struct {
	Type        string          `json:"type"`
	Filename    string          `json:"filename"`
	Snippet     string          `json:"snippet"`
	Path        string          `json:"path"`
	Content     json.RawMessage `json:"content"`
	Files       []ccAttachFile  `json:"files"`
	CommandMode string          `json:"commandMode"`
	Prompt      string          `json:"prompt"`
}

type ccAttachFile struct {
	URI         string             `json:"uri"`
	Path        string             `json:"path"`
	Type        string             `json:"type"`
	Content     string             `json:"content"`
	Diagnostics []ccDiagnosticItem `json:"diagnostics"`
}

type ccDiagnosticItem struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func (b *ccBuilder) handleAttachment(e ccEntry, ts time.Time) {
	if len(e.Attachment) == 0 {
		return
	}
	var a ccAttachmentEnvelope
	if err := json.Unmarshal(e.Attachment, &a); err != nil {
		return
	}

	switch a.Type {
	case "edited_text_file":
		b.emit(model.Event{
			ID:   b.idForSuffixed(e, 0),
			Kind: model.KindExternalEdit,
			TS:   ts,
			External: &model.ExternalEdit{
				Path:   a.Filename,
				Lines:  stripLineNumbers(a.Snippet),
				Source: "cc_attachment",
			},
		})
	case "diagnostics":
		for i, f := range a.Files {
			errs, warns := 0, 0
			var msgs []string
			for _, d := range f.Diagnostics {
				switch d.Severity {
				case "Error":
					errs++
				case "Warning":
					warns++
				}
				if len(msgs) < 5 && d.Message != "" {
					msgs = append(msgs, d.Message)
				}
			}
			b.emit(model.Event{
				ID:   b.idForSuffixed(e, i),
				Kind: model.KindDiagnostics,
				TS:   ts,
				Diagnostics: &model.Diagnostics{
					Path:     strings.TrimPrefix(f.URI, "file://"),
					Errors:   errs,
					Warnings: warns,
					Messages: msgs,
				},
			})
		}
	case "instructions":
		for i, f := range a.Files {
			content := model.Clip(f.Content, 16384)
			b.emit(model.Event{
				ID:   b.idForSuffixed(e, i),
				Kind: model.KindInstructions,
				TS:   ts,
				Instructions: &model.Instructions{
					Path:      f.Path,
					Scope:     scopeFromType(f.Type),
					Content:   content,
					Hash:      sha256Hex(content),
					FirstLine: firstLine(content),
				},
			})
		}
	case "nested_memory":
		var nc struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(a.Content, &nc)
		content := model.Clip(nc.Content, 16384)
		b.emit(model.Event{
			ID:   b.idForSuffixed(e, 0),
			Kind: model.KindInstructions,
			TS:   ts,
			Instructions: &model.Instructions{
				Path:      a.Path,
				Scope:     "nested",
				Content:   content,
				Hash:      sha256Hex(content),
				FirstLine: firstLine(content),
			},
		})
	case "queued_command":
		if a.CommandMode != "task-notification" && !strings.HasPrefix(a.Prompt, "<") {
			b.emit(model.Event{
				ID:     b.idForSuffixed(e, 0),
				Kind:   model.KindPrompt,
				TS:     ts,
				Prompt: &model.Prompt{Text: a.Prompt, Steering: true},
			})
		}
	default:
		// ignore
	}
}

func scopeFromType(t string) string {
	switch t {
	case "User":
		return "user"
	case "Project":
		return "project"
	case "Local":
		return "local"
	case "AutoMem":
		return "memory"
	default:
		return strings.ToLower(t)
	}
}

// --- type: "system" ---

func (b *ccBuilder) handleSystem(e ccEntry, ts time.Time) {
	switch {
	case e.Subtype == "compact_boundary":
		var meta struct {
			Trigger string `json:"trigger"`
		}
		_ = json.Unmarshal(e.CompactMetadata, &meta)
		b.lastCompactionIdx = len(b.session.Events)
		b.emit(model.Event{
			ID:         b.idFor(e, model.KindCompaction),
			Kind:       model.KindCompaction,
			TS:         ts,
			Compaction: &model.Compaction{Trigger: meta.Trigger},
		})
	case strings.Contains(e.Subtype, "error"):
		b.emit(model.Event{
			ID:      b.idFor(e, model.KindFailure),
			Kind:    model.KindFailure,
			TS:      ts,
			Failure: &model.Failure{Type: e.Subtype, Message: e.Content},
		})
	default:
		// turn_duration, stop_hook_summary, away_summary, etc.: ignore
	}
}

// --- subagent transcripts ---

func loadSubagents(sess *model.Session, mainPath string) {
	dir := filepath.Join(filepath.Dir(mainPath), sess.ID, "subagents")
	matches, err := filepath.Glob(filepath.Join(dir, "agent-*.jsonl"))
	if err != nil || len(matches) == 0 {
		return
	}
	sort.Strings(matches)

	for _, m := range matches {
		base := filepath.Base(m)
		agentID := strings.TrimSuffix(strings.TrimPrefix(base, "agent-"), ".jsonl")
		metaModel := readAgentMetaModel(strings.TrimSuffix(m, ".jsonl") + ".meta.json")

		sub := parseSubagentFile(m, sess.ID)
		if sub == nil {
			continue
		}
		for _, ev := range sub.Events {
			if ev.Kind == model.KindPrompt {
				continue // the parent's subagent event already holds the prompt
			}
			if ev.Model == "" {
				ev.Model = metaModel
			}
			ev.AgentID = agentID
			sess.Events = append(sess.Events, ev)
		}
	}
}

func parseSubagentFile(path, sessionID string) *model.Session {
	lines, err := ccReadLines(path)
	if err != nil {
		return nil
	}
	b := newCCBuilder(sessionID, path)
	seen := map[string]bool{}
	for _, raw := range lines {
		var e ccEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		if e.UUID != "" {
			if seen[e.UUID] {
				continue
			}
			seen[e.UUID] = true
		}
		b.absorb(e, path)
	}
	return b.finish()
}

func readAgentMetaModel(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var meta struct {
		Model         string `json:"model"`
		ResolvedModel string `json:"resolvedModel"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	if meta.ResolvedModel != "" {
		return meta.ResolvedModel
	}
	return meta.Model
}

func (b *ccBuilder) finish() *model.Session {
	// A tool_use that never got a result: emit it as a generic tool_call.
	for _, id := range b.pendingOrder {
		call, ok := b.pending[id]
		if !ok {
			continue
		}
		delete(b.pending, id)
		b.emit(model.Event{
			ID:       call.ID,
			Kind:     model.KindToolCall,
			TS:       call.TS,
			Model:    call.Model,
			ToolCall: &model.ToolCall{Name: call.Name, Input: clipJSON(call.Input), IsError: false},
		})
	}

	b.session.CWD = b.cwd
	b.session.Branch = b.branch
	b.session.HarnessVersion = b.version
	b.session.Title = b.title
	b.session.Capture = model.CaptureReconstructed
	return b.session
}
