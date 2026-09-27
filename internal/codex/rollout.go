// Package codex parses Codex CLI rollout transcripts into model.Session
// timelines. See docs/plan/formats/codex.md for the field-level mapping this
// package implements.
package codex

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// Event families used for the "prefer the newer form" rule in codex.md.
const (
	rolloutFamPrompt     = "prompt"
	rolloutFamAssistant  = "assistant"
	rolloutFamCommand    = "command"
	rolloutFamEdit       = "edit"
	rolloutFamMCP        = "mcp"
	rolloutFamWeb        = "web"
	rolloutFamSubagent   = "subagent"
	rolloutFamCompaction = "compaction"
)

// DefaultRoot returns Codex CLI's default session root, ~/.codex.
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

// Discover returns rollout files under root whose modification time is at or
// after since (zero since = all), sorted by path. It looks under
// root/sessions/** for "rollout-*.jsonl" and root/archived_sessions/** for
// any "*.jsonl". Missing directories contribute nothing (not an error).
func Discover(root string, since time.Time) ([]string, error) {
	var out []string

	sessions, err := rolloutDiscoverIn(filepath.Join(root, "sessions"), since, func(name string) bool {
		return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl")
	})
	if err != nil {
		return nil, err
	}
	out = append(out, sessions...)

	archived, err := rolloutDiscoverIn(filepath.Join(root, "archived_sessions"), since, func(name string) bool {
		return strings.HasSuffix(name, ".jsonl")
	})
	if err != nil {
		return nil, err
	}
	out = append(out, archived...)

	sort.Strings(out)
	return out, nil
}

func rolloutDiscoverIn(dir string, since time.Time, match func(name string) bool) ([]string, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !match(d.Name()) {
			return nil
		}
		if !since.IsZero() {
			info, err := d.Info()
			if err != nil {
				return nil // skip files we can't stat
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
	return out, nil
}

// ownerRepo parses "owner/repo" out of common git remote URL forms
// (git@github.com:o/r.git, https://github.com/o/r(.git), ssh://git@github.com/o/r.git).
//
// This duplicates internal/gitinfo's unexported ownerRepoFromURL; T10 adds an
// exported gitinfo.OwnerRepoFromURL and T24 may switch this package to it.
func ownerRepo(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	if idx := strings.Index(url, "github.com:"); idx != -1 {
		return url[idx+len("github.com:"):]
	}
	if idx := strings.Index(url, "github.com/"); idx != -1 {
		return url[idx+len("github.com/"):]
	}
	return ""
}

// rolloutLine is one decoded JSONL line: {"timestamp","type","payload"}.
type rolloutLine struct {
	Type    string
	Payload map[string]interface{}
	TS      time.Time
}

// ParseFile parses one Codex rollout file into 0 or 1 sessions.
func ParseFile(path string) ([]*model.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("codex: parse %s: %w", path, err)
	}
	defer f.Close()

	var lines []rolloutLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue // skip malformed lines, keep going
		}
		ts, ok := rolloutParseTS(rolloutStr(m, "timestamp"))
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		payload, _ := m["payload"].(map[string]interface{})
		lines = append(lines, rolloutLine{Type: typ, Payload: payload, TS: ts})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("codex: parse %s: %w", path, err)
	}
	if len(lines) == 0 {
		return nil, nil
	}

	p := newRolloutParser(path)
	p.scan(lines)
	p.emit(lines)

	if p.session.ID == "" {
		p.session.ID = rolloutIDFromFilename(path)
	}
	if p.session.CWD == "" {
		p.session.CWD = p.firstTurnCWD
	}
	p.session.Finalize()
	return []*model.Session{p.session}, nil
}

// rolloutParser holds the state accumulated across a rollout file's two passes.
type rolloutParser struct {
	path    string
	session *model.Session

	familyNewer map[string]bool // family -> newer-generation events present
	familyAny   map[string]bool // family -> any event of that family present

	fnOutputs map[string]string                 // response_item call_id -> function_call_output text
	spawnArgs map[string]map[string]interface{} // response_item call_id -> spawn_agent arguments

	idn map[model.EventKind]int // auto-id counters, per kind, in file order

	curModel  string
	curEffort string

	haveMode                                  bool
	lastApproval, lastSandbox, lastPermission string
	haveModelChange                           bool
	lastModel, lastEffort                     string

	instrSeen map[string]bool

	firstTurnCWD string

	compactions []rolloutCompactionMark
}

type rolloutCompactionMark struct {
	TS      time.Time
	Summary string
}

func newRolloutParser(path string) *rolloutParser {
	return &rolloutParser{
		path: path,
		session: &model.Session{
			Harness:    model.HarnessCodex,
			SourcePath: path,
			Capture:    model.CaptureReconstructed,
		},
		familyNewer: map[string]bool{},
		familyAny:   map[string]bool{},
		fnOutputs:   map[string]string{},
		spawnArgs:   map[string]map[string]interface{}{},
		idn:         map[model.EventKind]int{},
		instrSeen:   map[string]bool{},
	}
}

// nextID returns "<kind>-<n>", counting from 1 per kind in file order.
func (p *rolloutParser) nextID(k model.EventKind) string {
	p.idn[k]++
	return string(k) + "-" + strconv.Itoa(p.idn[k])
}

// add appends e to the session, filling ID and Origin when unset.
func (p *rolloutParser) add(e model.Event) {
	if e.ID == "" {
		e.ID = p.nextID(e.Kind)
	}
	if e.Origin == "" {
		e.Origin = model.OriginTranscript
	}
	p.session.Events = append(p.session.Events, e)
}

// rolloutNewerItemFamily maps item_completed's item.type to the family it belongs to.
// "Extension" (web) is handled separately because it also depends on item.kind.
var rolloutNewerItemFamily = map[string]string{
	"UserMessage":       rolloutFamPrompt,
	"AgentMessage":      rolloutFamAssistant,
	"CommandExecution":  rolloutFamCommand,
	"FileChange":        rolloutFamEdit,
	"McpToolCall":       rolloutFamMCP,
	"SubAgentActivity":  rolloutFamSubagent,
	"ContextCompaction": rolloutFamCompaction,
}

// scan is pass 1: it records, per family, whether the file has newer
// item_completed events, and pre-scans response_item function calls whose
// outputs or arguments are needed to emit later events (request_user_input
// answers, spawn_agent model enrichment) regardless of file order.
func (p *rolloutParser) scan(lines []rolloutLine) {
	for _, ln := range lines {
		switch ln.Type {
		case "turn_context":
			if p.firstTurnCWD == "" {
				p.firstTurnCWD = rolloutStr(ln.Payload, "cwd")
			}
		case "event_msg":
			sub := rolloutStr(ln.Payload, "type")
			switch sub {
			case "item_completed":
				item := rolloutMap(ln.Payload, "item")
				itype := rolloutStr(item, "type")
				if fam, ok := rolloutNewerItemFamily[itype]; ok {
					p.familyNewer[fam] = true
					p.familyAny[fam] = true
				} else if itype == "Extension" && rolloutStr(item, "kind") == "web.search" {
					p.familyNewer[rolloutFamWeb] = true
					p.familyAny[rolloutFamWeb] = true
				}
			case "user_message":
				p.familyAny[rolloutFamPrompt] = true
			case "agent_message":
				p.familyAny[rolloutFamAssistant] = true
			case "exec_command_end":
				p.familyAny[rolloutFamCommand] = true
			case "patch_apply_end":
				p.familyAny[rolloutFamEdit] = true
			case "mcp_tool_call_end":
				p.familyAny[rolloutFamMCP] = true
			case "web_search_end":
				p.familyAny[rolloutFamWeb] = true
			case "sub_agent_activity":
				p.familyAny[rolloutFamSubagent] = true
			case "context_compacted":
				p.familyAny[rolloutFamCompaction] = true
			}
		case "response_item":
			switch rolloutStr(ln.Payload, "type") {
			case "function_call":
				callID := rolloutStr(ln.Payload, "call_id")
				name := rolloutStr(ln.Payload, "name")
				if name == "spawn_agent" && callID != "" {
					if args := rolloutParseArgs(rolloutStr(ln.Payload, "arguments")); args != nil {
						p.spawnArgs[callID] = args
					}
				}
			case "function_call_output":
				callID := rolloutStr(ln.Payload, "call_id")
				if callID != "" {
					p.fnOutputs[callID] = rolloutOutputText(ln.Payload["output"])
				}
			}
		}
	}
}

// emit is pass 2: it walks the file in order, emitting events. Event.Seq/Turn
// ordering is handled later by Session.Finalize, so emission order here only
// needs to produce deterministic auto-IDs, not a sorted timeline.
func (p *rolloutParser) emit(lines []rolloutLine) {
	for _, ln := range lines {
		switch ln.Type {
		case "session_meta":
			p.handleSessionMeta(ln)
		case "turn_context":
			p.handleTurnSettings(ln.Payload, ln.TS)
		case "thread_settings_applied":
			p.handleTurnSettings(rolloutMap(ln.Payload, "thread_settings"), ln.TS)
		case "world_state":
			p.handleWorldState(ln)
		case "event_msg":
			p.handleEventMsg(ln)
		case "compacted":
			p.compactions = append(p.compactions, rolloutCompactionMark{TS: ln.TS, Summary: rolloutStr(ln.Payload, "message")})
		case "response_item":
			p.handleResponseItem(ln)
		}
	}
	p.flushCompactions()
}

func (p *rolloutParser) handleSessionMeta(ln rolloutLine) {
	pl := ln.Payload
	p.session.ID = rolloutStr(pl, "id")
	p.session.CWD = rolloutStr(pl, "cwd")
	p.session.HarnessVersion = rolloutStr(pl, "cli_version")
	for _, k := range []string{"forked_from_id", "parent_thread_id", "parent_id"} {
		if v := rolloutStr(pl, k); v != "" {
			p.session.ParentID = v
			break
		}
	}
	git := rolloutMap(pl, "git")
	if git == nil {
		return
	}
	sha := rolloutStr(git, "commit_hash")
	branch := rolloutStr(git, "branch")
	p.session.StartSHA = sha
	p.session.Branch = branch
	p.session.RepoRemote = ownerRepo(rolloutStr(git, "repository_url"))
	p.add(model.Event{
		Kind:    model.KindGitHead,
		TS:      ln.TS,
		GitHead: &model.GitHead{SHA: sha, Branch: branch, Trigger: "transcript_meta"},
	})
}

// handleTurnSettings applies turn_context (or thread_settings_applied's
// thread_settings) fields: mode_change / model_change on change, and always
// updates the "current" model used to stamp Event.Model on later events.
func (p *rolloutParser) handleTurnSettings(s map[string]interface{}, ts time.Time) {
	if s == nil {
		return
	}

	approval := rolloutStr(s, "approval_policy")
	sandbox := rolloutStr(rolloutMap(s, "sandbox_policy"), "type")
	perm := ""
	if cm := rolloutMap(s, "collaboration_mode"); cm != nil && rolloutStr(cm, "mode") == "plan" {
		perm = "plan"
	}
	if !p.haveMode || approval != p.lastApproval || sandbox != p.lastSandbox || perm != p.lastPermission {
		p.add(model.Event{
			Kind: model.KindMode,
			TS:   ts,
			Mode: &model.Mode{Approval: approval, Sandbox: sandbox, Permission: model.PermissionMode(perm)},
		})
		p.lastApproval, p.lastSandbox, p.lastPermission = approval, sandbox, perm
		p.haveMode = true
	}

	modelName := rolloutStr(s, "model")
	effort := rolloutStr(s, "effort")
	if effort == "" {
		if cm := rolloutMap(s, "collaboration_mode"); cm != nil {
			effort = rolloutStr(rolloutMap(cm, "settings"), "reasoning_effort")
		}
	}
	p.curModel = modelName
	p.curEffort = effort
	if !p.haveModelChange || modelName != p.lastModel || effort != p.lastEffort {
		p.add(model.Event{
			Kind:        model.KindModelChange,
			TS:          ts,
			ModelChange: &model.ModelChange{Model: modelName, Effort: effort},
		})
		p.lastModel, p.lastEffort = modelName, effort
		p.haveModelChange = true
	}
}

func (p *rolloutParser) handleWorldState(ln rolloutLine) {
	agentsMD := rolloutMap(rolloutMap(ln.Payload, "state"), "agents_md")
	if agentsMD == nil {
		return
	}
	content := rolloutStr(agentsMD, "text")
	if content == "" {
		return
	}
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	if p.instrSeen[hash] {
		return
	}
	p.instrSeen[hash] = true
	firstLine := content
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		firstLine = content[:idx]
	}
	p.add(model.Event{
		Kind: model.KindInstructions,
		TS:   ln.TS,
		Instructions: &model.Instructions{
			Path: "AGENTS.md", Scope: "project",
			Content: content, Hash: hash, FirstLine: firstLine,
		},
	})
}

// --- small generic JSON accessors over map[string]interface{} ---

func rolloutStr(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func rolloutMap(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		if x, ok := v.(map[string]interface{}); ok {
			return x
		}
	}
	return nil
}

func rolloutArr(m map[string]interface{}, key string) []interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		if x, ok := v.([]interface{}); ok {
			return x
		}
	}
	return nil
}

func rolloutNum(m map[string]interface{}, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func rolloutBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func rolloutHasKey(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// rolloutExitCode reads an exit code that is a JSON number in newer rollouts and a
// JSON string in older ones.
func rolloutExitCode(m map[string]interface{}, key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func rolloutFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var rolloutUUIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// rolloutIDFromFilename derives a session id from a rollout file name's trailing UUID.
func rolloutIDFromFilename(path string) string {
	base := filepath.Base(path)
	if m := rolloutUUIDRe.FindString(base); m != "" {
		return m
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func rolloutParseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}
