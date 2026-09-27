// Package pi parses Pi coding-agent session files into normalized
// model.Session timelines. See docs/plan/formats/pi.md for the mapping this
// file (and session_tools.go, tree.go) implements.
//
// pi.go (owned by a later task) is untouched by this file; both live in the
// same package, so every unexported identifier here is prefixed "sess" to
// avoid clashing with names that task adds.
package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// DefaultRoot returns Pi's default session root, ~/.pi/agent/sessions.
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

// Discover returns every *.jsonl file under root (searched recursively),
// whose modification time is at or after since (a zero since returns all),
// sorted by path.
func Discover(root string, since time.Time) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return nil // missing/unreadable root: no sessions, not an error
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".jsonl" {
			return nil
		}
		if !since.IsZero() {
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			if info.ModTime().Before(since) {
				return nil
			}
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// sessHeader is the first line of a session file.
type sessHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	ParentSession string `json:"parentSession"`
}

// sessRawEntry is the generic shape of one non-header line. Type-specific
// payloads (message, data) are decoded on demand from the raw JSON.
type sessRawEntry struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`

	Message    json.RawMessage `json:"message"`
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data"`
	Content    string          `json:"content"` // custom_message

	Provider      string `json:"provider"`      // model_change
	ModelID       string `json:"modelId"`       // model_change
	ThinkingLevel string `json:"thinkingLevel"` // thinking_level_change

	Summary  string `json:"summary"`  // compaction, branch_summary
	FromHook bool   `json:"fromHook"` // compaction

	Name string `json:"name"` // session_info
}

// sessReadLines reads path and returns each non-blank line as raw JSON,
// tolerating arbitrarily long lines (base64 images).
func sessReadLines(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []json.RawMessage
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, rerr := r.ReadBytes('\n')
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(trimmed)) > 0 {
			cp := make([]byte, len(trimmed))
			copy(cp, trimmed)
			lines = append(lines, json.RawMessage(cp))
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return nil, rerr
		}
	}
	return lines, nil
}

// sessParentIDFromFilename extracts the uuid part of a session file name
// (the text after the last '_', without ".jsonl").
func sessParentIDFromFilename(p string) string {
	base := filepath.Base(p)
	base = strings.TrimSuffix(base, ".jsonl")
	if idx := strings.LastIndex(base, "_"); idx >= 0 {
		return base[idx+1:]
	}
	return base
}

// sessParseTime parses a Pi ISO-8601 timestamp to UTC. An unparsable or
// empty timestamp yields the zero time.
func sessParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// ParseFile parses one Pi session file into zero or one *model.Session.
func ParseFile(path string) ([]*model.Session, error) {
	lines, err := sessReadLines(path)
	if err != nil {
		return nil, fmt.Errorf("pi: read %s: %w", path, err)
	}

	var header *sessHeader
	headerIdx := -1
	for i, line := range lines {
		var h sessHeader
		if jerr := json.Unmarshal(line, &h); jerr != nil {
			continue
		}
		if h.Type == "session" {
			header = &h
			headerIdx = i
			break
		}
	}
	if header == nil {
		return nil, nil
	}

	sess := &model.Session{
		Harness:    model.HarnessPi,
		ID:         header.ID,
		CWD:        header.CWD,
		SourcePath: path,
		Capture:    model.CaptureReconstructed,
	}
	if header.ParentSession != "" {
		sess.ParentID = sessParentIDFromFilename(header.ParentSession)
	}

	var raws []sessRawEntry
	var nodes []sessNode
	for i, line := range lines {
		if i == headerIdx {
			continue
		}
		var re sessRawEntry
		if jerr := json.Unmarshal(line, &re); jerr != nil {
			continue // skip malformed lines, keep going
		}
		if re.Type == "" {
			continue
		}
		raws = append(raws, re)
		nodes = append(nodes, sessNode{ID: re.ID, ParentID: re.ParentID})
	}

	p := &sessParser{
		active:  sessActivePath(nodes),
		pending: make(map[string]*sessPending),
	}
	for _, re := range raws {
		p.handleEntry(re)
	}
	p.flushUnresolved()

	sess.Title = p.title
	sess.Events = p.events
	sess.Finalize()
	return []*model.Session{sess}, nil
}

// HookRecords returns the HookRecords the paircli Pi extension stored in
// this session file as custom entries with customType "paircli".
func HookRecords(path string) ([]model.HookRecord, error) {
	lines, err := sessReadLines(path)
	if err != nil {
		return nil, fmt.Errorf("pi: read %s: %w", path, err)
	}

	var headerID string
	for _, line := range lines {
		var h sessHeader
		if jerr := json.Unmarshal(line, &h); jerr != nil {
			continue
		}
		if h.Type == "session" {
			headerID = h.ID
			break
		}
	}

	var out []model.HookRecord
	for _, line := range lines {
		var re sessRawEntry
		if jerr := json.Unmarshal(line, &re); jerr != nil {
			continue
		}
		if re.Type != "custom" || re.CustomType != "paircli" || len(re.Data) == 0 {
			continue
		}
		var rec model.HookRecord
		if jerr := json.Unmarshal(re.Data, &rec); jerr != nil {
			continue
		}
		if rec.SessionID == "" {
			rec.SessionID = headerID
		}
		if rec.Harness == "" {
			rec.Harness = model.HarnessPi
		}
		if rec.TS.IsZero() {
			rec.TS = sessParseTime(re.Timestamp)
		}
		out = append(out, rec)
	}
	return out, nil
}

// sessPending is a tool call awaiting its result, keyed by tool-call id.
type sessPending struct {
	CallID    string
	Name      string
	Args      sessArgs
	RawArgs   json.RawMessage
	Model     string
	TS        time.Time
	OffBranch bool
}

// sessParser holds the state accumulated while walking one session's
// entries in file order.
type sessParser struct {
	active     map[string]bool
	events     []model.Event
	pending    map[string]*sessPending
	pendingOrd []string
	curModel   string
	title      string
}

// sessMessage is the "message" field of a message-type entry.
type sessMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Usage        *sessUsage      `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	Details      json.RawMessage `json:"details"`
	IsError      bool            `json:"isError"`
	Command      string          `json:"command"`
	Output       string          `json:"output"`
	ExitCode     *int            `json:"exitCode"`
	Cancelled    bool            `json:"cancelled"`
	Sections     *sessSections   `json:"sections"`
}

type sessSections struct {
	ProjectContext string `json:"project_context"`
}

type sessUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Cost       *struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// sessBlock is one block of an assistant/user message content array.
type sessBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// sessArgs is the union of every tool's arguments shape this parser reads.
type sessArgs struct {
	Path    string       `json:"path"`
	Command string       `json:"command"`
	Content string       `json:"content"`
	Edits   []sessEditOp `json:"edits"`
	OldText string       `json:"oldText"`
	NewText string       `json:"newText"`
	Query   string       `json:"query"`
	Queries []string     `json:"queries"`
	URL     string       `json:"url"`
	URLs    []string     `json:"urls"`
	Agent   string       `json:"agent"`
	Task    string       `json:"task"`
	Tasks   []sessTask   `json:"tasks"`
	Action  string       `json:"action"`
}

type sessEditOp struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type sessTask struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

type sessDetails struct {
	Diff string `json:"diff"`
}

// sessAsString decodes raw as a JSON string; ok is false when it isn't one.
func sessAsString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

// sessTextAndImages extracts prompt text and image count from a user
// message's content, which may be a plain string or an array of blocks.
func sessTextAndImages(raw json.RawMessage) (string, int) {
	if len(raw) == 0 {
		return "", 0
	}
	if s, ok := sessAsString(raw); ok {
		return s, 0
	}
	var blocks []sessBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", 0
	}
	var texts []string
	images := 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "image":
			images++
		}
	}
	return strings.Join(texts, "\n"), images
}

// sessResultText joins the text blocks of a toolResult message's content
// (which may also be a plain string).
func sessResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if s, ok := sessAsString(raw); ok {
		return s
	}
	var blocks []sessBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

var sessProjectInstrRe = regexp.MustCompile(`(?s)<project_instructions path="([^"]*)">(.*?)</project_instructions>`)

type sessInstrTag struct {
	Path    string
	Content string
}

// sessExtractInstructions finds every <project_instructions path="…">…</…>
// tag in a system message's project_context section.
func sessExtractInstructions(s string) []sessInstrTag {
	matches := sessProjectInstrRe.FindAllStringSubmatch(s, -1)
	var out []sessInstrTag
	for _, m := range matches {
		out = append(out, sessInstrTag{Path: m[1], Content: strings.TrimSpace(m[2])})
	}
	return out
}

// sessUsageOf converts a decoded sessUsage into model.Usage, or nil.
func sessUsageOf(u *sessUsage) *model.Usage {
	if u == nil {
		return nil
	}
	out := &model.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
	if u.Cost != nil {
		out.CostUSD = u.Cost.Total
	}
	return out
}

// finish appends the events produced by one entry, assigning ids to any
// event whose ID is still empty: the entry id alone when exactly one such
// event exists, otherwise "<entryID>/<n>" counting from 1 in emission
// order. Events that already carry an ID (tool-call-derived ones) are
// appended unchanged.
func (p *sessParser) finish(entryID string, outs []model.Event) {
	need := 0
	for _, e := range outs {
		if e.ID == "" {
			need++
		}
	}
	n := 0
	for _, e := range outs {
		if e.ID == "" {
			n++
			if need == 1 {
				e.ID = entryID
			} else {
				e.ID = fmt.Sprintf("%s/%d", entryID, n)
			}
		}
		p.events = append(p.events, e)
	}
}

func (p *sessParser) handleEntry(re sessRawEntry) {
	ts := sessParseTime(re.Timestamp)
	offBranch := !sessIsActive(p.active, re.ID)

	switch re.Type {
	case "message":
		p.handleMessageEntry(re, ts, offBranch)
	case "model_change":
		p.curModel = re.ModelID
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindModelChange, Origin: model.OriginTranscript, OffBranch: offBranch,
			Model:       re.ModelID,
			ModelChange: &model.ModelChange{Model: re.ModelID, Provider: re.Provider},
		}})
	case "thinking_level_change":
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindModelChange, Origin: model.OriginTranscript, OffBranch: offBranch,
			Model:       p.curModel,
			ModelChange: &model.ModelChange{Model: p.curModel, Effort: re.ThinkingLevel},
		}})
	case "compaction":
		trigger := "auto"
		if re.FromHook {
			trigger = "extension"
		}
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindCompaction, Origin: model.OriginTranscript, OffBranch: offBranch,
			Compaction: &model.Compaction{Trigger: trigger, Summary: re.Summary},
		}})
	case "branch_summary":
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindReset, Origin: model.OriginTranscript, OffBranch: offBranch,
			Reset: &model.Reset{Type: "branch_switch", Summary: re.Summary},
		}})
	case "session_info":
		if re.Name != "" {
			p.title = re.Name
		}
	case "custom":
		// customType "paircli" is handled by HookRecords, not as an event;
		// usage/label/context_edit and any other customType are ignored.
	case "custom_message":
		if re.CustomType != "answers" {
			return
		}
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindQuestion, Origin: model.OriginTranscript, OffBranch: offBranch,
			Question: &model.Question{Tool: "answers", Items: []model.QA{{Answer: re.Content}}},
		}})
	default:
		// unknown entry type: ignore
	}
}

func (p *sessParser) handleMessageEntry(re sessRawEntry, ts time.Time, offBranch bool) {
	var m sessMessage
	if err := json.Unmarshal(re.Message, &m); err != nil {
		return
	}
	switch m.Role {
	case "user":
		text, images := sessTextAndImages(m.Content)
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindPrompt, Origin: model.OriginTranscript, OffBranch: offBranch,
			Prompt: &model.Prompt{Text: text, Images: images},
		}})
	case "assistant":
		p.finish(re.ID, p.handleAssistant(m, ts, offBranch))
	case "toolResult":
		p.handleToolResult(m, ts, offBranch)
	case "bashExecution":
		status := model.CmdOK
		switch {
		case m.Cancelled:
			status = model.CmdInterrupted
		case m.ExitCode != nil && *m.ExitCode != 0:
			status = model.CmdFailed
		}
		p.finish(re.ID, []model.Event{{
			TS: ts, Kind: model.KindCommand, Origin: model.OriginTranscript, OffBranch: offBranch,
			Command: &model.Command{
				Cmd: m.Command, ByUser: true, ExitCode: m.ExitCode, Status: status,
				Output: model.TruncateOutput(m.Output),
			},
		}})
	case "system":
		if m.Sections == nil {
			return
		}
		var outs []model.Event
		for _, tag := range sessExtractInstructions(m.Sections.ProjectContext) {
			outs = append(outs, model.Event{
				TS: ts, Kind: model.KindInstructions, Origin: model.OriginTranscript, OffBranch: offBranch,
				Instructions: &model.Instructions{Path: tag.Path, Scope: "project", Content: model.Clip(tag.Content, 16384)},
			})
		}
		p.finish(re.ID, outs)
	default:
		// custom, hookMessage, others: ignore
	}
}

func (p *sessParser) handleAssistant(m sessMessage, ts time.Time, offBranch bool) []model.Event {
	var outs []model.Event

	if m.Model != "" && m.Model != p.curModel {
		outs = append(outs, model.Event{
			TS: ts, Kind: model.KindModelChange, Origin: model.OriginTranscript, OffBranch: offBranch,
			Model:       m.Model,
			ModelChange: &model.ModelChange{Model: m.Model, Provider: m.Provider},
		})
	}
	if m.Model != "" {
		p.curModel = m.Model
	}

	var blocks []sessBlock
	_ = json.Unmarshal(m.Content, &blocks)

	firstMsg := true
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			msg := &model.Message{Text: b.Text}
			if firstMsg {
				msg.Usage = sessUsageOf(m.Usage)
				firstMsg = false
			}
			outs = append(outs, model.Event{
				TS: ts, Kind: model.KindMessage, Origin: model.OriginTranscript, OffBranch: offBranch,
				Model: m.Model, Message: msg,
			})
		case "toolCall":
			var args sessArgs
			_ = json.Unmarshal(b.Arguments, &args)
			if b.Name == "subagent" && (args.Action == "list" || args.Action == "status") {
				continue
			}
			p.pending[b.ID] = &sessPending{
				CallID: b.ID, Name: b.Name, Args: args, RawArgs: b.Arguments,
				Model: m.Model, TS: ts, OffBranch: offBranch,
			}
			p.pendingOrd = append(p.pendingOrd, b.ID)
		}
	}

	switch m.StopReason {
	case "aborted":
		outs = append(outs, model.Event{
			TS: ts, Kind: model.KindInterrupt, Origin: model.OriginTranscript, OffBranch: offBranch,
			Interrupt: &model.Interrupt{Reason: "aborted"},
		})
	case "error":
		outs = append(outs, model.Event{
			TS: ts, Kind: model.KindFailure, Origin: model.OriginTranscript, OffBranch: offBranch,
			Model: m.Model, Failure: &model.Failure{Type: "api_error", Message: m.ErrorMessage},
		})
	case "length":
		outs = append(outs, model.Event{
			TS: ts, Kind: model.KindFailure, Origin: model.OriginTranscript, OffBranch: offBranch,
			Model: m.Model, Failure: &model.Failure{Type: "length"},
		})
	}

	return outs
}

func (p *sessParser) handleToolResult(m sessMessage, ts time.Time, offBranch bool) {
	pc, ok := p.pending[m.ToolCallID]
	if !ok {
		return
	}
	delete(p.pending, m.ToolCallID)
	if ev := p.buildToolEvent(pc, m, ts, offBranch); ev != nil {
		p.events = append(p.events, *ev)
	}
}

// flushUnresolved emits, at each pending call's own timestamp, the events
// for tool calls that never received a toolResult.
func (p *sessParser) flushUnresolved() {
	for _, id := range p.pendingOrd {
		pc, ok := p.pending[id]
		if !ok {
			continue
		}
		delete(p.pending, id)
		if ev := p.buildToolEvent(pc, sessMessage{}, pc.TS, pc.OffBranch); ev != nil {
			p.events = append(p.events, *ev)
		}
	}
}
