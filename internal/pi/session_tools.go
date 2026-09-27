package pi

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// buildToolEvent turns a completed (or, with a zero-value m, unresolved)
// tool call into its event, per the table in docs/plan/formats/pi.md.
func (p *sessParser) buildToolEvent(pc *sessPending, m sessMessage, ts time.Time, offBranch bool) *model.Event {
	switch {
	case pc.Name == "bash":
		return p.buildBashResult(pc, m, ts, offBranch)
	case pc.Name == "edit":
		added, removed := sessEditDiff(m.Details, pc.Args)
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindEdit, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			Edit: &model.FileEdit{Path: pc.Args.Path, Op: model.OpUpdate, Via: "edit", Added: added, Removed: removed, Failed: m.IsError},
		}
	case pc.Name == "write":
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindEdit, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			Edit: &model.FileEdit{Path: pc.Args.Path, Op: model.OpCreate, Via: "write", Added: model.SplitLines(pc.Args.Content), Failed: m.IsError},
		}
	case pc.Name == "read":
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindRead, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			Read: &model.FileRead{Path: pc.Args.Path},
		}
	case pc.Name == "web_search":
		query := pc.Args.Query
		if query == "" && len(pc.Args.Queries) > 0 {
			query = strings.Join(pc.Args.Queries, " | ")
		}
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindLookup, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			Lookup: &model.Lookup{Kind: "search", Query: query},
		}
	case pc.Name == "fetch_content":
		urls := pc.Args.URLs
		if len(urls) == 0 && pc.Args.URL != "" {
			urls = []string{pc.Args.URL}
		}
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindLookup, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			Lookup: &model.Lookup{Kind: "fetch", URLs: urls},
		}
	case pc.Name == "subagent":
		typ, task := pc.Args.Agent, pc.Args.Task
		if typ == "" && len(pc.Args.Tasks) > 0 {
			typ = pc.Args.Tasks[0].Agent
			task = pc.Args.Tasks[0].Task
		}
		status := ""
		if m.IsError {
			status = "error"
		}
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindSubagent, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			Subagent: &model.Subagent{Type: typ, Prompt: model.Clip(task, 500), Status: status},
		}
	case strings.Contains(pc.Name, "mcp"):
		server, tool := sessMCPServerTool(pc.Name)
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindMCP, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			MCP: &model.MCPCall{Server: server, Tool: tool, Args: sessArgsClip(pc.RawArgs), IsError: m.IsError},
		}
	default:
		return &model.Event{
			ID: pc.CallID, TS: ts, Kind: model.KindToolCall, Origin: model.OriginTranscript,
			OffBranch: offBranch, Model: pc.Model,
			ToolCall: &model.ToolCall{Name: pc.Name, Input: sessArgsClip(pc.RawArgs), IsError: m.IsError},
		}
	}
}

func (p *sessParser) buildBashResult(pc *sessPending, m sessMessage, ts time.Time, offBranch bool) *model.Event {
	output := sessResultText(m.Content)
	status := model.CmdOK
	if m.IsError {
		status = model.CmdFailed
	}
	var exitCode *int
	if code, ok := sessParseExitCode(output); ok {
		exitCode = model.IntPtr(code)
	} else if !m.IsError {
		exitCode = model.IntPtr(0)
	}
	return &model.Event{
		ID: pc.CallID, TS: ts, Kind: model.KindCommand, Origin: model.OriginTranscript,
		OffBranch: offBranch, Model: pc.Model,
		Command: &model.Command{Cmd: pc.Args.Command, Status: status, ExitCode: exitCode, Output: model.TruncateOutput(output)},
	}
}

var sessExitCodeRe = regexp.MustCompile(`(?:exit code|exited with code)\s+(\d+)`)

// sessParseExitCode finds the last "exit code N" / "exited with code N" in
// output.
func sessParseExitCode(output string) (int, bool) {
	matches := sessExitCodeRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return 0, false
	}
	last := matches[len(matches)-1]
	n, err := strconv.Atoi(last[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

var (
	sessDiffAddRe = regexp.MustCompile(`^\+(?:\s*)(?:\d+)\s`)
	sessDiffDelRe = regexp.MustCompile(`^-(?:\s*)(?:\d+)\s`)
)

// sessParseDiffLines splits a Pi edit toolResult's details.diff into added
// and removed lines, stripping the leading sign/spaces/line-number/space
// prefix from each matching line.
func sessParseDiffLines(diff string) (added, removed []string) {
	for _, line := range strings.Split(diff, "\n") {
		if loc := sessDiffAddRe.FindStringIndex(line); loc != nil {
			added = append(added, line[loc[1]:])
			continue
		}
		if loc := sessDiffDelRe.FindStringIndex(line); loc != nil {
			removed = append(removed, line[loc[1]:])
		}
	}
	return added, removed
}

// sessLineDiff is the fallback line-diff used when details.diff is absent:
// added lines are lines of newText not present in oldText, removed lines
// are the reverse (mirrors the Claude Code Edit fallback in
// docs/plan/formats/claude-code.md).
func sessLineDiff(oldText, newText string) (added, removed []string) {
	oldLines := model.SplitLines(oldText)
	newLines := model.SplitLines(newText)
	inOld := make(map[string]bool, len(oldLines))
	for _, l := range oldLines {
		inOld[l] = true
	}
	inNew := make(map[string]bool, len(newLines))
	for _, l := range newLines {
		inNew[l] = true
	}
	for _, l := range newLines {
		if !inOld[l] {
			added = append(added, l)
		}
	}
	for _, l := range oldLines {
		if !inNew[l] {
			removed = append(removed, l)
		}
	}
	return added, removed
}

// sessEditDiff computes Added/Removed for an edit tool call: from the
// toolResult's details.diff when present, else from the call's arguments
// (edits[], or a top-level oldText/newText pair), diffed with sessLineDiff.
func sessEditDiff(details json.RawMessage, args sessArgs) (added, removed []string) {
	var d sessDetails
	if len(details) > 0 {
		_ = json.Unmarshal(details, &d)
	}
	if d.Diff != "" {
		return sessParseDiffLines(d.Diff)
	}

	edits := args.Edits
	if len(edits) == 0 && (args.OldText != "" || args.NewText != "") {
		edits = []sessEditOp{{OldText: args.OldText, NewText: args.NewText}}
	}
	for _, e := range edits {
		a, r := sessLineDiff(e.OldText, e.NewText)
		added = append(added, a...)
		removed = append(removed, r...)
	}
	return added, removed
}

// sessMCPServerTool splits an MCP tool name into server and tool, best
// effort: the segment right after "mcp" is the server, the rest is the
// tool.
func sessMCPServerTool(name string) (server, tool string) {
	idx := strings.Index(name, "mcp")
	if idx < 0 {
		return "", name
	}
	rest := strings.TrimPrefix(name[idx+3:], "_")
	parts := strings.SplitN(rest, "_", 2)
	server = parts[0]
	if len(parts) > 1 {
		tool = parts[1]
	}
	return server, tool
}

// sessArgsClip renders a tool call's raw arguments as JSON, clipped to 500
// runes.
func sessArgsClip(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return model.Clip(string(raw), 500)
}
