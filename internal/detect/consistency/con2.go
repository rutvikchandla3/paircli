package consistency

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type con2 struct{}

func init() { engine.Register(con2{}) }

func (con2) ID() string { return "CON-2" }

// con2TodoRe matches the marker words that make an added line an open loop.
var con2TodoRe = regexp.MustCompile(`\b(TODO|FIXME|XXX|HACK)\b`)

// con2Terminal lists the statuses that mean a task is no longer open.
var con2Terminal = map[string]bool{
	"completed": true,
	"done":      true,
	"cancelled": true,
	"canceled":  true,
	"deleted":   true,
}

// con2Task is one task-list entry, kept under the key it was upserted by.
type con2Task struct {
	key    string
	text   string
	status string
}

// con2Open reports whether a task status still counts as open. An empty status
// is open.
func con2Open(status string) bool {
	return !con2Terminal[strings.ToLower(strings.TrimSpace(status))]
}

// con2Key is how a todo item is matched on upsert: its ID, or its text when it
// has no ID.
func con2Key(it model.TodoItem) string {
	if it.ID != "" {
		return it.ID
	}
	return it.Text
}

// con2Replay folds a session's todo events, in order, into its final task
// list. A Replace event replaces the list; any other event upserts each of its
// items by key, updating text and status when the item carries them.
func con2Replay(todos []engine.Item) []con2Task {
	var tasks []con2Task
	for _, it := range todos {
		t := it.E.Todo
		if t == nil {
			continue
		}
		if t.Replace {
			tasks = tasks[:0]
			for _, item := range t.Items {
				tasks = append(tasks, con2Task{key: con2Key(item), text: item.Text, status: item.Status})
			}
			continue
		}
		for _, item := range t.Items {
			k := con2Key(item)
			found := false
			for i := range tasks {
				if tasks[i].key != k {
					continue
				}
				found = true
				if item.Text != "" {
					tasks[i].text = item.Text
				}
				if item.Status != "" {
					tasks[i].status = item.Status
				}
				break
			}
			if !found {
				tasks = append(tasks, con2Task{key: k, text: item.Text, status: item.Status})
			}
		}
	}
	return tasks
}

func (d con2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	openTasks := 0
	for _, s := range c.Sessions {
		var todos []engine.Item
		for _, it := range c.Of(model.KindTodo) {
			// Only the main agent's task list is the session's list.
			if it.S.Ref() == s.Ref() && it.E.AgentID == "" {
				todos = append(todos, it)
			}
		}
		for _, task := range con2Replay(todos) {
			if !con2Open(task.status) {
				continue
			}
			openTasks++
			sig.Findings = append(sig.Findings, model.Finding{
				Summary:  fmt.Sprintf("Task still open at session end: “%s”", model.Clip(task.text, 80)),
				Severity: model.StateAlert,
				Data:     map[string]any{"session": s.Ref(), "status": task.status},
			})
		}
	}

	todoComments := 0
	if c.PR != nil {
		for _, f := range c.PR.Files {
			if classify.Has(classify.Path(f.Path, c.Config), classify.Generated) {
				continue
			}
			for _, dl := range f.AddedLines() {
				m := con2TodoRe.FindStringSubmatch(dl.Text)
				if m == nil {
					continue
				}
				todoComments++
				sig.Findings = append(sig.Findings, model.Finding{
					Summary: fmt.Sprintf("`%s:%d` adds a %s: %s",
						f.Path, dl.NewNo, m[1], model.Clip(strings.TrimSpace(dl.Text), 80)),
					Severity: model.StateInfo,
					Anchors:  []model.Anchor{{File: f.Path, Lines: strconv.Itoa(dl.NewNo)}},
				})
			}
		}
	}

	var parts []string
	if openTasks > 0 {
		parts = append(parts, fmt.Sprintf("%d tasks still open at session end", openTasks))
	}
	if todoComments > 0 {
		parts = append(parts, fmt.Sprintf("%d TODO/FIXME comments added", todoComments))
	}

	if len(parts) == 0 {
		sig.State = model.StateClear
		sig.Summary = "No open tasks and no TODO/FIXME comments added."
	} else {
		// An open task outranks a stray TODO comment.
		sig.State = model.StateInfo
		if openTasks > 0 {
			sig.State = model.StateAlert
		}
		sig.Summary = strings.Join(parts, "; ") + "."
	}

	sig.Data = map[string]any{"open_tasks": openTasks, "todo_comments": todoComments}
	return sig
}
