package codex

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// handleEventMsg dispatches a top-level {"type":"event_msg",...} line.
func (p *rolloutParser) handleEventMsg(ln rolloutLine) {
	sub := rolloutStr(ln.Payload, "type")
	switch sub {
	case "item_completed":
		p.handleItemCompleted(ln)
	case "user_message":
		if !p.familyNewer[rolloutFamPrompt] {
			p.emitPromptOld(ln)
		}
	case "agent_message":
		if !p.familyNewer[rolloutFamAssistant] {
			p.emitAssistantOld(ln)
		}
	case "exec_command_end":
		if !p.familyNewer[rolloutFamCommand] {
			p.emitCommandOld(ln)
		}
	case "patch_apply_end":
		if !p.familyNewer[rolloutFamEdit] {
			p.emitEditOld(ln)
		}
	case "mcp_tool_call_end":
		if !p.familyNewer[rolloutFamMCP] {
			p.emitMCPOld(ln)
		}
	case "web_search_end":
		if !p.familyNewer[rolloutFamWeb] {
			p.emitWebOld(ln)
		}
	case "sub_agent_activity":
		if !p.familyNewer[rolloutFamSubagent] {
			p.emitSubagent(ln.TS, rolloutStr(ln.Payload, "kind"), rolloutStr(ln.Payload, "agent_thread_id"),
				rolloutStr(ln.Payload, "agent_path"), rolloutFirstNonEmpty(rolloutStr(ln.Payload, "id"), rolloutStr(ln.Payload, "event_id")))
		}
	case "context_compacted":
		if !p.familyNewer[rolloutFamCompaction] {
			p.compactions = append(p.compactions, rolloutCompactionMark{TS: ln.TS})
		}
	case "turn_aborted":
		reason := rolloutStr(ln.Payload, "reason")
		if reason == "interrupted" {
			reason = "user"
		}
		p.add(model.Event{Kind: model.KindInterrupt, TS: ln.TS, Interrupt: &model.Interrupt{Reason: reason}})
	case "thread_rolled_back":
		p.add(model.Event{Kind: model.KindReset, TS: ln.TS, Reset: &model.Reset{Type: "rollback"}})
	case "task_complete":
		// nothing; Session.Finalize marks final assistant messages.
	}
}

// handleItemCompleted dispatches a newer-generation item_completed item.
func (p *rolloutParser) handleItemCompleted(ln rolloutLine) {
	item := rolloutMap(ln.Payload, "item")
	if item == nil {
		return
	}
	switch rolloutStr(item, "type") {
	case "UserMessage":
		p.emitPromptNew(ln.TS, item)
	case "AgentMessage":
		p.emitAssistantNew(ln.TS, item)
	case "CommandExecution":
		p.emitCommandNew(ln.TS, item)
	case "FileChange":
		p.emitEditNew(ln.TS, item)
	case "McpToolCall":
		p.emitMCPNew(ln.TS, item)
	case "Extension":
		if rolloutStr(item, "kind") == "web.search" {
			p.emitWebNew(ln.TS, item)
		}
	case "SubAgentActivity":
		p.emitSubagent(ln.TS, rolloutStr(item, "kind"), rolloutStr(item, "agent_thread_id"), rolloutStr(item, "agent_path"),
			rolloutFirstNonEmpty(rolloutStr(item, "id"), rolloutStr(item, "event_id")))
	case "ContextCompaction":
		p.compactions = append(p.compactions, rolloutCompactionMark{TS: ln.TS})
	case "Plan":
		p.add(model.Event{
			Kind: model.KindPlan, TS: ln.TS, ID: rolloutStr(item, "id"),
			Plan: &model.Plan{Text: rolloutStr(item, "text"), Source: "plan_item"},
		})
	case "ImageView":
		p.add(model.Event{
			Kind: model.KindImage, TS: ln.TS, ID: rolloutStr(item, "id"),
			Image: &model.Image{Ref: rolloutStr(item, "path")},
		})
	}
}

// --- prompt ---

func rolloutJoinText(parts []interface{}) string {
	var sb strings.Builder
	for _, part := range parts {
		pm, ok := part.(map[string]interface{})
		if !ok {
			continue
		}
		if rolloutHasKey(pm, "text") {
			sb.WriteString(rolloutStr(pm, "text"))
		}
	}
	return sb.String()
}

func rolloutImagesCount(m map[string]interface{}) int {
	n := 0
	if rolloutHasKey(m, "images") {
		n += len(rolloutArr(m, "images"))
	}
	if rolloutHasKey(m, "local_images") {
		n += len(rolloutArr(m, "local_images"))
	}
	return n
}

func (p *rolloutParser) emitPromptNew(ts time.Time, item map[string]interface{}) {
	text := strings.TrimSuffix(rolloutJoinText(rolloutArr(item, "content")), "\n")
	pr := &model.Prompt{Text: text}
	if n := rolloutImagesCount(item); n > 0 {
		pr.Images = n
	}
	p.add(model.Event{Kind: model.KindPrompt, TS: ts, ID: rolloutStr(item, "id"), Prompt: pr})
}

func (p *rolloutParser) emitPromptOld(ln rolloutLine) {
	text := strings.TrimSuffix(rolloutStr(ln.Payload, "message"), "\n")
	pr := &model.Prompt{Text: text}
	if n := rolloutImagesCount(ln.Payload); n > 0 {
		pr.Images = n
	}
	p.add(model.Event{Kind: model.KindPrompt, TS: ln.TS, Prompt: pr})
}

// --- assistant ---

func (p *rolloutParser) emitAssistantNew(ts time.Time, item map[string]interface{}) {
	text := rolloutJoinText(rolloutArr(item, "content"))
	p.add(model.Event{Kind: model.KindMessage, TS: ts, ID: rolloutStr(item, "id"), Model: p.curModel, Message: &model.Message{Text: text}})
}

func (p *rolloutParser) emitAssistantOld(ln rolloutLine) {
	text := rolloutStr(ln.Payload, "message")
	p.add(model.Event{Kind: model.KindMessage, TS: ln.TS, Model: p.curModel, Message: &model.Message{Text: text}})
}

// --- command ---

// rolloutCmdString unwraps a shell command given as either a plain string or an
// argv array. When the array's second-to-last element is "-lc" or "-c", the
// last element (the actual command text) is used; otherwise the array is
// joined with spaces.
func rolloutCmdString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) >= 2 {
			second := parts[len(parts)-2]
			if second == "-lc" || second == "-c" {
				return parts[len(parts)-1]
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func rolloutCmdStatus(statusStr string, exit *int) model.CommandStatus {
	switch statusStr {
	case "interrupted", "cancelled":
		return model.CmdInterrupted
	}
	if exit == nil {
		return model.CmdUnknown
	}
	if *exit == 0 {
		return model.CmdOK
	}
	return model.CmdFailed
}

func rolloutDurationMs(m map[string]interface{}) int64 {
	d := rolloutMap(m, "duration")
	if d == nil {
		return 0
	}
	secs, _ := rolloutNum(d, "secs")
	nanos, _ := rolloutNum(d, "nanos")
	return int64(secs*1000) + int64(nanos/1e6)
}

func rolloutStripFileURL(s string) string {
	return strings.TrimPrefix(s, "file://")
}

func rolloutCmdOutput(m map[string]interface{}) string {
	if v, ok := m["aggregated_output"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return rolloutStr(m, "stdout") + rolloutStr(m, "stderr")
}

// emitReadsFromParsedCmd emits one file_read per parsed_cmd entry when every
// entry has type "read".
func (p *rolloutParser) emitReadsFromParsedCmd(ts time.Time, parsed []interface{}) {
	if len(parsed) == 0 {
		return
	}
	for _, e := range parsed {
		em, ok := e.(map[string]interface{})
		if !ok || rolloutStr(em, "type") != "read" {
			return
		}
	}
	for _, e := range parsed {
		em := e.(map[string]interface{})
		path := rolloutStr(em, "path")
		if path == "" {
			continue
		}
		p.add(model.Event{Kind: model.KindRead, TS: ts, Read: &model.FileRead{Path: path}})
	}
}

func (p *rolloutParser) emitCommandNew(ts time.Time, item map[string]interface{}) {
	cmd := rolloutCmdString(item["command"])
	exit, hasExit := rolloutExitCode(item, "exit_code")
	var exitPtr *int
	if hasExit {
		exitPtr = model.IntPtr(exit)
	}
	c := &model.Command{
		Cmd:        cmd,
		CWD:        rolloutStripFileURL(rolloutStr(item, "cwd")),
		ExitCode:   exitPtr,
		Status:     rolloutCmdStatus(rolloutStr(item, "status"), exitPtr),
		Output:     model.TruncateOutput(rolloutCmdOutput(item)),
		DurationMs: rolloutDurationMs(item),
	}
	p.add(model.Event{Kind: model.KindCommand, TS: ts, ID: rolloutStr(item, "id"), Model: p.curModel, Command: c})
	p.emitReadsFromParsedCmd(ts, rolloutArr(item, "parsed_cmd"))
}

func (p *rolloutParser) emitCommandOld(ln rolloutLine) {
	pl := ln.Payload
	cmd := rolloutCmdString(pl["command"])
	exit, hasExit := rolloutExitCode(pl, "exit_code")
	var exitPtr *int
	if hasExit {
		exitPtr = model.IntPtr(exit)
	}
	c := &model.Command{
		Cmd:        cmd,
		CWD:        rolloutStripFileURL(rolloutStr(pl, "cwd")),
		ExitCode:   exitPtr,
		Status:     rolloutCmdStatus(rolloutStr(pl, "status"), exitPtr),
		Output:     model.TruncateOutput(rolloutCmdOutput(pl)),
		DurationMs: rolloutDurationMs(pl),
	}
	p.add(model.Event{Kind: model.KindCommand, TS: ln.TS, ID: rolloutStr(pl, "call_id"), Model: p.curModel, Command: c})
	p.emitReadsFromParsedCmd(ln.TS, rolloutArr(pl, "parsed_cmd"))
}

// --- edit ---

// rolloutBuildFileEdit builds a FileEdit from one "changes" entry (newer FileChange)
// or an equivalent map assembled for the older patch_apply_end generation.
func rolloutBuildFileEdit(path string, ch map[string]interface{}) *model.FileEdit {
	fe := &model.FileEdit{Path: path, Via: "apply_patch"}
	switch rolloutStr(ch, "type") {
	case "add":
		fe.Op = model.OpCreate
		fe.Added = model.SplitLines(rolloutStr(ch, "content"))
	case "delete":
		fe.Op = model.OpDelete
		fe.Removed = model.SplitLines(rolloutStr(ch, "content"))
	case "update":
		fe.Op = model.OpUpdate
		if mv := rolloutStr(ch, "move_path"); mv != "" {
			fe.Op = model.OpMove
			fe.MovePath = mv
		}
		hunks, added, removed := parseUnifiedHunks(rolloutStr(ch, "unified_diff"))
		fe.Hunks = hunks
		fe.Added = added
		fe.Removed = removed
	}
	return fe
}

func (p *rolloutParser) emitEditNew(ts time.Time, item map[string]interface{}) {
	itemID := rolloutStr(item, "id")
	changes := rolloutMap(item, "changes")
	failed := rolloutHasKey(item, "status") && rolloutStr(item, "status") != "completed"

	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for i, path := range paths {
		chRaw, ok := changes[path].(map[string]interface{})
		if !ok {
			continue
		}
		fe := rolloutBuildFileEdit(path, chRaw)
		if failed {
			fe.Failed = true
		}
		p.add(model.Event{
			Kind: model.KindEdit, TS: ts, ID: fmt.Sprintf("%s/%d", itemID, i+1),
			Model: p.curModel, Edit: fe,
		})
	}
}

func (p *rolloutParser) emitEditOld(ln rolloutLine) {
	pl := ln.Payload
	ch := map[string]interface{}{
		"type":         rolloutStr(pl, "op"),
		"content":      pl["content"],
		"unified_diff": pl["unified_diff"],
		"move_path":    pl["move_path"],
	}
	fe := rolloutBuildFileEdit(rolloutStr(pl, "path"), ch)
	if rolloutHasKey(pl, "success") && !rolloutBool(pl, "success") {
		fe.Failed = true
	}
	p.add(model.Event{Kind: model.KindEdit, TS: ln.TS, ID: rolloutStr(pl, "call_id"), Model: p.curModel, Edit: fe})
}

// emitApplyPatchFallback handles the custom_tool_call apply_patch fallback,
// used only when the file has no edit-family events at all.
func (p *rolloutParser) emitApplyPatchFallback(ln rolloutLine) {
	changes := parseApplyPatch(rolloutStr(ln.Payload, "input"))
	callID := rolloutStr(ln.Payload, "call_id")
	for i, ch := range changes {
		fe := &model.FileEdit{
			Path: ch.Path, Op: ch.Op, MovePath: ch.MovePath,
			Added: ch.Added, Removed: ch.Removed, Hunks: ch.Hunks, Via: "apply_patch",
		}
		id := ""
		if callID != "" {
			id = fmt.Sprintf("%s/%d", callID, i+1)
		}
		p.add(model.Event{Kind: model.KindEdit, TS: ln.TS, ID: id, Model: p.curModel, Edit: fe})
	}
}

// --- mcp ---

func rolloutArgsJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return model.Clip(s, 500)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return model.Clip(string(b), 500)
}

func (p *rolloutParser) emitMCPNew(ts time.Time, item map[string]interface{}) {
	result := rolloutMap(item, "result")
	isErr := result != nil && rolloutBool(result, "isError")
	p.add(model.Event{
		Kind: model.KindMCP, TS: ts, ID: rolloutStr(item, "id"), Model: p.curModel,
		MCP: &model.MCPCall{
			Server:   rolloutStr(item, "server"),
			Tool:     rolloutStr(item, "tool"),
			ReadOnly: rolloutBool(item, "readOnlyHint"),
			Args:     rolloutArgsJSON(item["arguments"]),
			IsError:  isErr,
		},
	})
}

func (p *rolloutParser) emitMCPOld(ln rolloutLine) {
	pl := ln.Payload
	inv := rolloutMap(pl, "invocation")
	isErr := false
	if rm, ok := pl["result"].(map[string]interface{}); ok {
		_, isErr = rm["Err"]
	}
	p.add(model.Event{
		Kind: model.KindMCP, TS: ln.TS, ID: rolloutStr(pl, "call_id"), Model: p.curModel,
		MCP: &model.MCPCall{
			Server:  rolloutStr(inv, "server"),
			Tool:    rolloutStr(inv, "tool"),
			Args:    rolloutArgsJSON(inv["arguments"]),
			IsError: isErr,
		},
	})
}

// --- web ---

func rolloutLookupFromAction(item, action map[string]interface{}) *model.Lookup {
	l := &model.Lookup{Kind: "search", Query: rolloutStr(item, "query")}
	if action == nil {
		return l
	}
	switch rolloutStr(action, "type") {
	case "open_page", "fetch":
		if u := rolloutStr(action, "url"); u != "" {
			l.Kind = "fetch"
			l.URLs = []string{u}
		}
	}
	return l
}

func (p *rolloutParser) emitWebNew(ts time.Time, item map[string]interface{}) {
	p.add(model.Event{
		Kind: model.KindLookup, TS: ts, ID: rolloutStr(item, "id"), Model: p.curModel,
		Lookup: rolloutLookupFromAction(item, rolloutMap(item, "action")),
	})
}

func (p *rolloutParser) emitWebOld(ln rolloutLine) {
	pl := ln.Payload
	p.add(model.Event{
		Kind: model.KindLookup, TS: ln.TS, Model: p.curModel,
		Lookup: rolloutLookupFromAction(pl, rolloutMap(pl, "action")),
	})
}

// --- subagent ---

func (p *rolloutParser) emitSubagent(ts time.Time, kind, agentThreadID, agentPath, callID string) {
	if kind != "started" {
		return
	}
	modelName := ""
	if callID != "" {
		if args, ok := p.spawnArgs[callID]; ok {
			modelName = rolloutStr(args, "model")
		}
	}
	p.add(model.Event{
		Kind: model.KindSubagent, TS: ts, AgentID: agentThreadID,
		Subagent: &model.Subagent{AgentID: agentThreadID, Type: agentPath, Model: modelName},
	})
}

// --- compaction ---

func (p *rolloutParser) flushCompactions() {
	if len(p.compactions) == 0 {
		return
	}
	sort.Slice(p.compactions, func(i, j int) bool { return p.compactions[i].TS.Before(p.compactions[j].TS) })

	var group []rolloutCompactionMark
	flush := func() {
		if len(group) == 0 {
			return
		}
		summary := ""
		for _, g := range group {
			if g.Summary != "" {
				summary = g.Summary
			}
		}
		p.add(model.Event{
			Kind:       model.KindCompaction,
			TS:         group[0].TS,
			Compaction: &model.Compaction{Summary: summary},
		})
		group = nil
	}
	for _, m := range p.compactions {
		if len(group) > 0 && m.TS.Sub(group[len(group)-1].TS) > 10*time.Second {
			flush()
		}
		group = append(group, m)
	}
	flush()
}

// --- response_item: function_call / function_call_output / custom_tool_call ---

func rolloutParseArgs(s string) map[string]interface{} {
	if s == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

func rolloutOutputText(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]interface{}:
		if s, ok := t["text"].(string); ok {
			return s
		}
	case []interface{}:
		return rolloutJoinText(t)
	}
	return ""
}

func rolloutToStrings(a []interface{}) []string {
	out := make([]string, 0, len(a))
	for _, v := range a {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func rolloutIsUnavailable(s string) bool {
	return strings.Contains(strings.ToLower(s), "unavailable")
}

func rolloutParsePlanItems(steps []interface{}) []model.TodoItem {
	items := make([]model.TodoItem, 0, len(steps))
	for _, s := range steps {
		sm, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		items = append(items, model.TodoItem{Text: rolloutStr(sm, "step"), Status: rolloutStr(sm, "status")})
	}
	return items
}

func (p *rolloutParser) handleResponseItem(ln rolloutLine) {
	switch rolloutStr(ln.Payload, "type") {
	case "function_call":
		p.handleFunctionCall(ln)
	case "custom_tool_call":
		if rolloutStr(ln.Payload, "name") == "apply_patch" && !p.familyAny[rolloutFamEdit] {
			p.emitApplyPatchFallback(ln)
		}
	}
}

func (p *rolloutParser) handleFunctionCall(ln rolloutLine) {
	name := rolloutStr(ln.Payload, "name")
	callID := rolloutStr(ln.Payload, "call_id")
	args := rolloutParseArgs(rolloutStr(ln.Payload, "arguments"))

	switch name {
	case "update_plan":
		items := rolloutParsePlanItems(rolloutArr(args, "plan"))
		p.add(model.Event{
			Kind: model.KindTodo, TS: ln.TS, ID: callID,
			Todo: &model.Todo{Tool: "update_plan", Items: items, Replace: true},
		})
	case "request_user_input":
		p.emitQuestion(ln.TS, callID, args)
	case "exec_command", "shell":
		if !p.familyAny[rolloutFamCommand] {
			cmd := rolloutStr(args, "cmd")
			if cmd == "" {
				cmd = rolloutStr(args, "command")
			}
			p.add(model.Event{
				Kind: model.KindCommand, TS: ln.TS, ID: callID, Model: p.curModel,
				Command: &model.Command{Cmd: cmd, Status: model.CmdUnknown},
			})
		}
	}
	// spawn_agent is enriched via the pre-scan in scan(); no event of its own.
	// wait/write_stdin/list_agents/send_message/CollabAgentToolCall and other
	// unknown names are ignored.
}

func (p *rolloutParser) emitQuestion(ts time.Time, callID string, args map[string]interface{}) {
	output := p.fnOutputs[callID]
	if rolloutIsUnavailable(output) {
		return
	}
	var items []model.QA
	if qs := rolloutArr(args, "questions"); len(qs) > 0 {
		for _, q := range qs {
			qm, ok := q.(map[string]interface{})
			if !ok {
				continue
			}
			items = append(items, model.QA{
				Question: rolloutStr(qm, "question"),
				Options:  rolloutToStrings(rolloutArr(qm, "options")),
				Answer:   output,
			})
		}
	} else if q := rolloutStr(args, "question"); q != "" {
		items = append(items, model.QA{Question: q, Options: rolloutToStrings(rolloutArr(args, "options")), Answer: output})
	}
	if len(items) == 0 {
		return
	}
	p.add(model.Event{
		Kind: model.KindQuestion, TS: ts, ID: callID,
		Question: &model.Question{Tool: "request_user_input", Items: items},
	})
}
