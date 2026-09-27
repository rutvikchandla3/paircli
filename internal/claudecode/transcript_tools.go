package claudecode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// ccInput is the union of every tool's "input" fields we care about. Real
// inputs carry many more fields we ignore.
type ccInput struct {
	Command         string       `json:"command"`
	RunInBackground bool         `json:"run_in_background"`
	FilePath        string       `json:"file_path"`
	OldString       string       `json:"old_string"`
	NewString       string       `json:"new_string"`
	Edits           []ccEditItem `json:"edits"`
	Content         string       `json:"content"`
	NotebookPath    string       `json:"notebook_path"`
	NewSource       string       `json:"new_source"`
	Plan            string       `json:"plan"`
	Todos           []ccTodoItem `json:"todos"`
	Subject         string       `json:"subject"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	TaskID          string       `json:"taskId"`
	ID              string       `json:"id"`
	Status          string       `json:"status"`
	SubagentType    string       `json:"subagent_type"`
	Prompt          string       `json:"prompt"`
	Query           string       `json:"query"`
	URL             string       `json:"url"`
}

type ccEditItem struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

type ccTodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

func decodeInput(raw json.RawMessage) ccInput {
	var in ccInput
	_ = json.Unmarshal(raw, &in)
	return in
}

// ccToolResult is the union of every tool's "toolUseResult" object fields.
type ccToolResult struct {
	Stdout          string                     `json:"stdout"`
	Stderr          string                     `json:"stderr"`
	Interrupted     bool                       `json:"interrupted"`
	TimedOutAfterMs *int64                     `json:"timedOutAfterMs"`
	BashEditDiff    json.RawMessage            `json:"bashEditDiff"`
	Type            string                     `json:"type"` // Write: "create" | "update"
	StructuredPatch []ccPatchHunk              `json:"structuredPatch"`
	FilePath        string                     `json:"filePath"`
	Questions       []ccQuestionItem           `json:"questions"`
	Answers         map[string]json.RawMessage `json:"answers"`
	AgentID         string                     `json:"agentId"`
	ResolvedModel   string                     `json:"resolvedModel"`
	Status          string                     `json:"status"`
}

type ccPatchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

type ccQuestionItem struct {
	Question string     `json:"question"`
	Header   string     `json:"header"`
	Options  []ccOption `json:"options"`
}

type ccOption struct {
	Label string `json:"label"`
}

// completeToolUse resolves the pending tool_use with the same tool_use_id as
// blk against its tool_result, emitting the mapped event(s), then discards
// the pending call.
func (b *ccBuilder) completeToolUse(blk ccBlock, ts time.Time, entryToolUseResult json.RawMessage, sourcePath string) {
	call, ok := b.pending[blk.ToolUseID]
	if !ok {
		return
	}
	delete(b.pending, blk.ToolUseID)

	resultText, images := decodeContentBlocks(blk.Content)

	if isRejection(resultText, blk.IsError, entryToolUseResult) {
		b.emit(model.Event{
			ID:        call.ID,
			Kind:      model.KindRejection,
			TS:        ts,
			Model:     call.Model,
			Rejection: &model.Rejection{Tool: call.Name, ToolUseID: call.ID, Reason: "user"},
		})
		return
	}

	switch {
	case call.Name == "Bash":
		b.emitBash(call, ts, resultText, blk.IsError, entryToolUseResult)
	case call.Name == "Edit" || call.Name == "MultiEdit":
		b.emitEdit(call, ts, blk.IsError, entryToolUseResult)
	case call.Name == "Write":
		b.emitWrite(call, ts, blk.IsError, entryToolUseResult)
	case call.Name == "NotebookEdit":
		b.emitNotebookEdit(call, ts)
	case call.Name == "Read":
		b.emit(model.Event{ID: call.ID, Kind: model.KindRead, TS: ts, Model: call.Model, Read: &model.FileRead{Path: decodeInput(call.Input).FilePath}})
	case call.Name == "AskUserQuestion":
		b.emitAskUserQuestion(call, ts, resultText, entryToolUseResult)
	case call.Name == "ExitPlanMode":
		b.emitExitPlanMode(call, ts, resultText, blk.IsError)
	case call.Name == "TodoWrite":
		b.emitTodoWrite(call, ts)
	case call.Name == "TaskCreate":
		b.emitTaskCreate(call, ts, resultText)
	case call.Name == "TaskUpdate":
		b.emitTaskUpdate(call, ts)
	case call.Name == "Agent" || call.Name == "Task":
		b.emitSubagent(call, ts, entryToolUseResult)
	case call.Name == "WebSearch":
		in := decodeInput(call.Input)
		b.emit(model.Event{ID: call.ID, Kind: model.KindLookup, TS: ts, Model: call.Model, Lookup: &model.Lookup{Kind: "search", Query: in.Query}})
	case call.Name == "WebFetch":
		in := decodeInput(call.Input)
		b.emit(model.Event{ID: call.ID, Kind: model.KindLookup, TS: ts, Model: call.Model, Lookup: &model.Lookup{Kind: "fetch", URLs: []string{in.URL}}})
	case strings.HasPrefix(call.Name, "mcp__"):
		b.emitMCP(call, ts, blk.IsError)
	default:
		b.emit(model.Event{ID: call.ID, Kind: model.KindToolCall, TS: ts, Model: call.Model, ToolCall: &model.ToolCall{Name: call.Name, Input: clipJSON(call.Input), IsError: blk.IsError}})
	}

	for i, img := range images {
		if img.Source == nil {
			continue
		}
		b.emit(model.Event{
			ID:    call.ID + "/" + strconv.Itoa(i),
			Kind:  model.KindImage,
			TS:    ts,
			Model: call.Model,
			Image: &model.Image{
				MediaType: img.Source.MediaType,
				Bytes:     len(img.Source.Data) * 3 / 4,
				Tool:      call.Name,
				Ref:       sourcePath + "#" + call.ID + "/" + strconv.Itoa(i),
			},
		})
	}
}

var exitCodeRE = regexp.MustCompile(`^Exit code (\d+)`)

func isRejection(resultText string, isError bool, toolUseResult json.RawMessage) bool {
	if isError && strings.HasPrefix(resultText, "The user doesn't want to proceed with this tool use") {
		return true
	}
	if s, ok := decodeStringResult(toolUseResult); ok && s == "User rejected tool use" {
		return true
	}
	return false
}

func (b *ccBuilder) emitBash(call *pendingCall, ts time.Time, resultText string, isError bool, toolUseResult json.RawMessage) {
	in := decodeInput(call.Input)
	cmd := &model.Command{Cmd: in.Command, Background: in.RunInBackground}

	var tr ccToolResult
	isObj := len(toolUseResult) > 0 && toolUseResult[0] == '{' && json.Unmarshal(toolUseResult, &tr) == nil

	if !isError && isObj {
		cmd.ExitCode = model.IntPtr(0)
		cmd.Status = model.CmdOK
		if tr.Interrupted {
			cmd.Status = model.CmdInterrupted
		} else if tr.TimedOutAfterMs != nil {
			cmd.Status = model.CmdTimeout
		}
		out := tr.Stdout
		if tr.Stderr != "" {
			if out != "" {
				out += "\n"
			}
			out += tr.Stderr
		}
		cmd.Output = model.TruncateOutput(out)

		if len(tr.BashEditDiff) > 0 {
			b.emitBashEditDiff(call, ts, tr.BashEditDiff)
		}
	} else {
		cmd.Status = model.CmdFailed
		if m := exitCodeRE.FindStringSubmatch(resultText); m != nil {
			n, _ := strconv.Atoi(m[1])
			cmd.ExitCode = model.IntPtr(n)
		} else if s, ok := decodeStringResult(toolUseResult); ok {
			if m := exitCodeRE.FindStringSubmatch(s); m != nil {
				n, _ := strconv.Atoi(m[1])
				cmd.ExitCode = model.IntPtr(n)
			}
		}
		cmd.Output = model.TruncateOutput(resultText)
	}

	b.emit(model.Event{ID: call.ID, Kind: model.KindCommand, TS: ts, Model: call.Model, Command: cmd})
}

func (b *ccBuilder) emitBashEditDiff(call *pendingCall, ts time.Time, raw json.RawMessage) {
	var bd struct {
		FilePath        string        `json:"filePath"`
		StructuredPatch []ccPatchHunk `json:"structuredPatch"`
	}
	if err := json.Unmarshal(raw, &bd); err != nil {
		return
	}
	if bd.FilePath == "" && len(bd.StructuredPatch) == 0 {
		return
	}
	hunks, added, removed := hunksFromPatch(bd.StructuredPatch)
	b.emit(model.Event{
		ID:    call.ID + "/edit",
		Kind:  model.KindEdit,
		TS:    ts,
		Model: call.Model,
		Edit:  &model.FileEdit{Path: bd.FilePath, Op: model.OpUpdate, Via: "bash_edit", Hunks: hunks, Added: added, Removed: removed},
	})
}

func (b *ccBuilder) emitEdit(call *pendingCall, ts time.Time, isError bool, toolUseResult json.RawMessage) {
	in := decodeInput(call.Input)
	edit := &model.FileEdit{Path: in.FilePath, Op: model.OpUpdate, Via: "edit", Failed: isError}

	var tr ccToolResult
	hasPatch := len(toolUseResult) > 0 && json.Unmarshal(toolUseResult, &tr) == nil && len(tr.StructuredPatch) > 0

	if hasPatch {
		edit.Hunks, edit.Added, edit.Removed = hunksFromPatch(tr.StructuredPatch)
	} else if call.Name == "MultiEdit" {
		for _, ed := range in.Edits {
			a, r := diffAddedRemoved(ed.OldString, ed.NewString)
			edit.Added = append(edit.Added, a...)
			edit.Removed = append(edit.Removed, r...)
		}
	} else {
		edit.Added, edit.Removed = diffAddedRemoved(in.OldString, in.NewString)
	}

	b.emit(model.Event{ID: call.ID, Kind: model.KindEdit, TS: ts, Model: call.Model, Edit: edit})
}

func (b *ccBuilder) emitWrite(call *pendingCall, ts time.Time, isError bool, toolUseResult json.RawMessage) {
	in := decodeInput(call.Input)
	edit := &model.FileEdit{Path: in.FilePath, Via: "write", Failed: isError}

	var tr ccToolResult
	_ = json.Unmarshal(toolUseResult, &tr)

	if tr.Type == "create" {
		edit.Op = model.OpCreate
		edit.Added = model.SplitLines(in.Content)
	} else {
		edit.Op = model.OpUpdate
		if len(tr.StructuredPatch) > 0 {
			edit.Hunks, edit.Added, edit.Removed = hunksFromPatch(tr.StructuredPatch)
		}
	}

	b.emit(model.Event{ID: call.ID, Kind: model.KindEdit, TS: ts, Model: call.Model, Edit: edit})
}

func (b *ccBuilder) emitNotebookEdit(call *pendingCall, ts time.Time) {
	in := decodeInput(call.Input)
	edit := &model.FileEdit{Path: in.NotebookPath, Op: model.OpUpdate, Via: "notebook", Added: model.SplitLines(in.NewSource)}
	b.emit(model.Event{ID: call.ID, Kind: model.KindEdit, TS: ts, Model: call.Model, Edit: edit})
}

func (b *ccBuilder) emitAskUserQuestion(call *pendingCall, ts time.Time, resultText string, toolUseResult json.RawMessage) {
	q := &model.Question{Tool: "AskUserQuestion"}

	var tr ccToolResult
	if len(toolUseResult) > 0 && json.Unmarshal(toolUseResult, &tr) == nil && len(tr.Questions) > 0 {
		for _, item := range tr.Questions {
			var opts []string
			for _, o := range item.Options {
				opts = append(opts, o.Label)
			}
			answer := ""
			if raw, ok := tr.Answers[item.Question]; ok {
				answer = decodeAnswer(raw)
			}
			q.Items = append(q.Items, model.QA{Header: item.Header, Question: item.Question, Options: opts, Answer: answer})
		}
	} else {
		q.Items = parseEqualsQA(resultText)
	}

	b.emit(model.Event{ID: call.ID, Kind: model.KindQuestion, TS: ts, Model: call.Model, Question: q})
}

func decodeAnswer(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return strings.Join(arr, ", ")
	}
	return ""
}

// parseEqualsQA is a best-effort fallback parser for "key=value" style
// result text when toolUseResult is missing. It never errors; on failure it
// returns nil (empty Answer).
func parseEqualsQA(text string) []model.QA {
	var out []model.QA
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		out = append(out, model.QA{Question: strings.TrimSpace(parts[0]), Answer: strings.TrimSpace(parts[1])})
	}
	return out
}

func (b *ccBuilder) emitExitPlanMode(call *pendingCall, ts time.Time, resultText string, isError bool) {
	in := decodeInput(call.Input)
	plan := &model.Plan{Source: "exit_plan_mode", Text: in.Plan}
	if isError {
		plan.Approved = model.BoolPtr(false)
	} else if strings.HasPrefix(resultText, "User has approved") {
		plan.Approved = model.BoolPtr(true)
	}
	b.emit(model.Event{ID: call.ID, Kind: model.KindPlan, TS: ts, Model: call.Model, Plan: plan})
}

func (b *ccBuilder) emitTodoWrite(call *pendingCall, ts time.Time) {
	in := decodeInput(call.Input)
	todo := &model.Todo{Tool: "TodoWrite", Replace: true}
	for _, t := range in.Todos {
		todo.Items = append(todo.Items, model.TodoItem{Text: t.Content, Status: t.Status})
	}
	b.emit(model.Event{ID: call.ID, Kind: model.KindTodo, TS: ts, Model: call.Model, Todo: todo})
}

func (b *ccBuilder) emitTaskCreate(call *pendingCall, ts time.Time, resultText string) {
	in := decodeInput(call.Input)
	text := firstNonEmpty(in.Subject, in.Title, in.Description)
	id := parseTaskID(resultText)
	todo := &model.Todo{Tool: "TaskCreate", Items: []model.TodoItem{{ID: id, Text: text, Status: "pending"}}}
	b.emit(model.Event{ID: call.ID, Kind: model.KindTodo, TS: ts, Model: call.Model, Todo: todo})
}

func (b *ccBuilder) emitTaskUpdate(call *pendingCall, ts time.Time) {
	in := decodeInput(call.Input)
	id := in.TaskID
	if id == "" {
		id = in.ID
	}
	todo := &model.Todo{Tool: "TaskUpdate", Items: []model.TodoItem{{ID: id, Status: in.Status, Text: in.Subject}}}
	b.emit(model.Event{ID: call.ID, Kind: model.KindTodo, TS: ts, Model: call.Model, Todo: todo})
}

func (b *ccBuilder) emitSubagent(call *pendingCall, ts time.Time, toolUseResult json.RawMessage) {
	in := decodeInput(call.Input)
	var tr ccToolResult
	_ = json.Unmarshal(toolUseResult, &tr)
	sub := &model.Subagent{
		AgentID: tr.AgentID,
		Type:    in.SubagentType,
		Model:   tr.ResolvedModel,
		Prompt:  model.Clip(in.Prompt, 500),
		Status:  tr.Status,
	}
	b.emit(model.Event{ID: call.ID, Kind: model.KindSubagent, TS: ts, Model: call.Model, Subagent: sub})
}

func (b *ccBuilder) emitMCP(call *pendingCall, ts time.Time, isError bool) {
	rest := strings.TrimPrefix(call.Name, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	server, tool := parts[0], ""
	if len(parts) == 2 {
		tool = parts[1]
	}
	b.emit(model.Event{
		ID:    call.ID,
		Kind:  model.KindMCP,
		TS:    ts,
		Model: call.Model,
		MCP:   &model.MCPCall{Server: server, Tool: tool, Args: clipJSON(call.Input), IsError: isError},
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var taskIDRE = regexp.MustCompile(`\btask[_-]?id["':\s]*([A-Za-z0-9_-]+)`)

func parseTaskID(text string) string {
	m := taskIDRE.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

func hunksFromPatch(patch []ccPatchHunk) ([]model.Hunk, []string, []string) {
	var hunks []model.Hunk
	var added, removed []string
	for _, p := range patch {
		hunks = append(hunks, model.Hunk{OldStart: p.OldStart, OldLines: p.OldLines, NewStart: p.NewStart, NewLines: p.NewLines, Lines: p.Lines})
		for _, l := range p.Lines {
			if l == "" {
				continue
			}
			switch l[0] {
			case '+':
				added = append(added, l[1:])
			case '-':
				removed = append(removed, l[1:])
			}
		}
	}
	return hunks, added, removed
}

// diffAddedRemoved is the fallback used when structuredPatch is missing:
// Added is the lines of newStr not present in oldStr, Removed the reverse.
func diffAddedRemoved(oldStr, newStr string) ([]string, []string) {
	oldLines := model.SplitLines(oldStr)
	newLines := model.SplitLines(newStr)
	oldSet := map[string]bool{}
	for _, l := range oldLines {
		oldSet[l] = true
	}
	newSet := map[string]bool{}
	for _, l := range newLines {
		newSet[l] = true
	}
	var added, removed []string
	for _, l := range newLines {
		if !oldSet[l] {
			added = append(added, l)
		}
	}
	for _, l := range oldLines {
		if !newSet[l] {
			removed = append(removed, l)
		}
	}
	return added, removed
}

// decodeContentBlocks decodes a tool_result's Content field, which is
// either a plain string or an array of blocks. It returns the concatenated
// text (for error-pattern matching and default output) and any image
// blocks, in array order.
func decodeContentBlocks(raw json.RawMessage) (string, []ccBlock) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var blocks []ccBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil
	}
	var texts []string
	var images []ccBlock
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			texts = append(texts, blk.Text)
		case "image":
			images = append(images, blk)
		}
	}
	return strings.Join(texts, "\n"), images
}

// decodeStringResult reports whether raw decodes as a plain JSON string,
// returning it when it does.
func decodeStringResult(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func clipJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return model.Clip(string(raw), 500)
}

var lineNumberPrefixRE = regexp.MustCompile(`^\s*\d+\t`)

// stripLineNumbers splits a numbered snippet ("37\tconst x = 1;") into
// lines with the "<n>\t" prefix removed.
func stripLineNumbers(snippet string) []string {
	if snippet == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(snippet, "\n") {
		out = append(out, lineNumberPrefixRE.ReplaceAllString(l, ""))
	}
	return out
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// extractTag returns the text between "<tag>" and "</tag>" in s, and
// whether the opening tag was found at all (open without close still
// reports ok=true, using the text to end of string).
func extractTag(s, tag string) (string, bool) {
	open := "<" + tag + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(open):]
	closeTag := "</" + tag + ">"
	if j := strings.Index(rest, closeTag); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}

// extractSlashCommand reports whether s contains a <command-name> tag and,
// if so, its name (with leading "/") and args.
func extractSlashCommand(s string) (name, args string, ok bool) {
	if !strings.Contains(s, "<command-name>") {
		return "", "", false
	}
	name, _ = extractTag(s, "command-name")
	name = strings.TrimSpace(name)
	args, _ = extractTag(s, "command-args")
	return name, args, name != ""
}
