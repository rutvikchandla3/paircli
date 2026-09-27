# Contracts

Everything two tasks share is defined here. The Go source for `internal/model`,
`internal/config` and `internal/redact` is in [`_contracts/`](_contracts/)
and compiles as-is. The package APIs further down are exact signatures that
the owning task must implement and every other task may call.

Changing anything here requires the contract-gap process in `README.md`.

## 1. Session timeline (`internal/model`)

A parser turns one transcript file into `*model.Session` values with a flat,
time-ordered `Events` slice. Each `Event` has exactly one non-nil payload that
matches its `Kind`:

| Kind | Payload | Produced by |
|---|---|---|
| `prompt` | `Prompt` | human-typed prompt, slash command (Text = args), mid-turn steering (`Steering`) |
| `assistant_message` | `Message` | assistant text (not thinking/reasoning) |
| `command` | `Command` | shell command by the agent, or by the human (`ByUser`) |
| `file_edit` | `Edit` | any agent file write: edit/write tools, `apply_patch`, Codex `FileChange` |
| `file_read` | `Read` | read tools; Codex commands whose `parsed_cmd` is all `read` |
| `external_edit` | `External` | file changed outside the agent (Claude Code attachment, hook snapshot) |
| `question` | `Question` | agent asked the human (AskUserQuestion, request_user_input) |
| `plan` | `Plan` | ExitPlanMode, Codex `Plan` item |
| `mode_change` | `Mode` | permission/approval/sandbox mode changed (emit only on change) |
| `model_change` | `ModelChange` | model or effort changed (emit only on change) |
| `interrupt` | `Interrupt` | human interrupted the agent |
| `tool_rejected` | `Rejection` | human rejected a specific tool call |
| `permission` | `Permission` | hook-captured permission prompt/decision |
| `compaction` | `Compaction` | context summarized |
| `reset` | `Reset` | clear, resume, fork, rollback, Pi branch switch |
| `subagent` | `Subagent` | a subagent was started |
| `lookup` | `Lookup` | web search / fetch |
| `mcp_call` | `MCP` | MCP tool call |
| `tool_call` | `ToolCall` | any other tool call |
| `todo` | `Todo` | task-list writes |
| `instructions` | `Instructions` | rules/memory file loaded |
| `diagnostics` | `Diagnostics` | editor/LSP errors for a file |
| `image` | `Image` | screenshot or other image produced |
| `git_head` | `GitHead` | HEAD observed (hooks; Codex session_meta) |
| `snapshot` | `Snapshot` | hook line-hash snapshot of dirty files |
| `cwd_change` | `CwdChange` | working directory changed |
| `failure` | `Failure` | API error / output-length stop |
| `session_end` | `SessionEnd` | session ended |

Rules every parser follows:

1. `Event.ID` is the harness's own id where one exists (tool_use id, entry
   uuid, item id). Otherwise `"<kind>-<n>"` with n counting from 1 per kind
   in file order. IDs are unique within a session and stable across runs.
2. `Origin` is `transcript` for parser events and `hook` for hook-derived ones.
3. `Model` is set on assistant-side events (messages, commands, edits, tool
   calls) to the model that produced them.
4. Subagent events are appended to the parent session with `AgentID` set.
5. `Command.Output` always goes through `model.TruncateOutput`; long free text
   uses `model.Clip` with the limit stated in the field comment.
6. Call `Session.Finalize()` last. It sorts, sets `Seq`, `Turn`,
   `Message.Final`, `Start`, `End`. Parsers never set those themselves.
7. Parsers leave `RepoRoot` and all `RelPath` fields empty; `internal/link` fills them.

## 2. Package APIs

### Harness parsers — `internal/claudecode` (T03), `internal/codex` (T04), `internal/pi` (T05)

Each package exposes the same three functions:

```go
// DefaultRoot returns the harness's default session root
// (claude-code: ~/.claude/projects; codex: ~/.codex; pi: ~/.pi/agent/sessions).
func DefaultRoot() string

// Discover returns transcript files under root whose modification time is at
// or after since (zero since = all), sorted by path. Claude Code returns only
// main transcripts (never files under */subagents/). Codex searches
// root/sessions/** and root/archived_sessions/**. Pi searches recursively.
func Discover(root string, since time.Time) ([]string, error)

// ParseFile parses one transcript into sessions (Claude Code may return
// several when a file mixes sessionIds; Codex and Pi return one).
// Claude Code also loads <dir>/<sessionId>/subagents/*.jsonl for each session.
func ParseFile(path string) ([]*model.Session, error)
```

Pi additionally (T05):

```go
// HookRecords returns the HookRecords the paircli Pi extension stored in this
// session file as custom entries with customType "paircli".
func HookRecords(path string) ([]model.HookRecord, error)
```

### `internal/hooklog` (T08)

```go
func DefaultDir() string                                     // ~/.paircli/events
func Append(dir string, rec model.HookRecord) error          // dir/<harness>/<UTC date>.jsonl, O_APPEND
func Read(dir string, h model.Harness, since time.Time) (map[string][]model.HookRecord, error) // keyed by SessionID
func Merge(s *model.Session, recs []model.HookRecord)        // adds OriginHook events, sets Capture=hooked, calls s.Finalize()
func Snapshot(repoDir string) ([]model.FileLines, error)     // line hashes of dirty files (see T08)
```

### `internal/gitinfo` additions

```go
func TopLevel(dir string) (string, error)            // T10: nearest ancestor containing .git (file or dir)
func OwnerRepoFromURL(url string) string             // T10: exported wrapper of existing ownerRepoFromURL
func HeadAndBranch(dir string) (sha, branch string, err error) // T08: git rev-parse HEAD + abbrev-ref, 2s timeout
```

### `internal/classify` (T07)

```go
type CmdClass string
const (
	Test, Lint, Typecheck, Build, Format CmdClass = "test", "lint", "typecheck", "build", "format"
	Install                                      = "install"
	GitCommit, GitPush, GitForcePush             = "git_commit", "git_push", "git_force_push"
	GitResetHard, GitDiscard, GitHistory         = "git_reset_hard", "git_discard", "git_history"
	RmRF, Migration                              = "rm_rf", "migration"
	NetworkWrite, NetworkRead                    = "network_write", "network_read"
	Container, Infra, Cloud, Publish             = "container", "infra", "cloud", "publish"
	GHWrite, GHRead                              = "gh_write", "gh_read"
	DevServer, LocalHTTP                         = "dev_server", "local_http"
	NoVerify, HookSkipEnv, SnapshotUpdate        = "no_verify", "hook_skip_env", "snapshot_update"
	ReadFile                                     = "read_file"
)
// (Written as individual typed consts in code; grouped here for brevity.)

func Command(cmd string, extra []config.CheckPattern) []CmdClass // sorted, deduped
func CheckClass(classes []CmdClass) (CmdClass, bool)            // first of test, typecheck, lint, build
func Segments(cmd string) []string                              // split on && || ; | outside quotes, env prefixes kept
func Tokens(segment string) []string                            // shell-like tokens, quotes removed
func Unwrap(cmd string) string                                  // strips `bash|sh|zsh -lc|-c '<x>'` wrappers → x

type Package struct{ Manager, Name, Version string }
func Packages(cmd string) []Package                             // for install commands
func ReadTargets(cmd string) []string                           // file args of cat/head/tail/less/bat/sed -n
func RmTargets(cmd string) []string                             // args of rm -rf / rm -r

type CheckResult struct {
	Status                  string // "pass" | "fail" | "unknown"
	Passed, Failed, Skipped int    // -1 when not parsed
}
func Check(c *model.Command) CheckResult

type PathClass string
const (
	TestFile, CIConfig, Manifest, Lockfile PathClass = "test", "ci_config", "manifest", "lockfile"
	SecretFile, Generated, Sensitive       PathClass = "secret_file", "generated", "sensitive"
	SnapshotFile, Docs                     PathClass = "snapshot", "docs"
)
func Path(rel string, cfg *config.Config) []PathClass
func Has(classes []PathClass, c PathClass) bool
func Glob(pattern, path string) bool                            // supports **, *, ?, [..]; '/' separators

type SecretHit struct{ Kind, Masked string }
func Secrets(text string) []SecretHit
func Suppression(line string) (kind string, ok bool)            // lint/type/coverage suppression comment
func SkipMarker(line string) (kind string, ok bool)             // test skip/only/xfail marker
func AssertionLike(line string) bool
func ShortCmd(cmd string) string                                // Unwrap + collapse whitespace + clip 80 runes
```

### `internal/engine` (T02)

```go
type Item struct {
	S *model.Session
	E *model.Event
}

type Context struct {
	PR          *model.PR
	Sessions    []*model.Session   // linked sessions only; RelPath fields populated
	Timeline    []Item             // all events of Sessions, sorted by (TS, Session.Ref(), Seq)
	Attribution *model.Attribution // never nil
	Links       []model.CommitLink
	SessionLinks map[string]model.LinkMethod // Session.Ref() → how it was linked; nil in unit tests
	Judgments   *model.Judgments   // nil unless the LLM pass ran
	Config      *config.Config     // never nil
}

func NewContext(pr *model.PR, sessions []*model.Session, attr *model.Attribution,
	links []model.CommitLink, cfg *config.Config) *Context

func (c *Context) Ref(it Item) model.EventRef
func (c *Context) Evidence(it Item, excerpt string) model.Evidence       // clips to 200 runes, applies redact.Text
func (c *Context) AnchorsFor(items ...Item) []model.Anchor              // PR lines produced by these events
func (c *Context) Of(kinds ...model.EventKind) []Item                   // timeline filtered by kind, in order
func (c *Context) IsPRFile(rel string) bool
func (c *Context) HasSessions() bool
func (c *Context) Session(ref string) *model.Session                    // by Session.Ref(), or nil

func Clock(t time.Time) string                  // "14:02 UTC" (UTC, 24h)
func Plural(n int, one, many string) string     // Plural(1,"edit","edits") == "1 edit"
func Pct(part, whole int) string                // "45%" (rounded); "0%" when whole == 0
func Home(path string) string                   // replaces the user's home dir prefix with "~"

type Detector interface {
	ID() string
	Detect(c *Context) model.Signal
}

func Register(d Detector)                       // call from init(); panics on duplicate or unknown ID
func Detectors() []Detector                     // catalog order
func Run(c *Context) []model.Signal             // all detectors; recovers panics; fills Support; catalog order
func RunIDs(c *Context, ids ...string) []model.Signal

func NewSignal(id string) model.Signal          // pre-filled Question/Title/Priority/Provenance, State=clear
func Unknown(id, reason string) model.Signal    // State=unknown, Summary=reason
func NoSessions(id string) model.Signal         // Unknown(id, "No captured sessions for this PR.")
func Catalog() []Meta                           // the 30 signals in catalog order
type Meta struct {
	ID         string
	Question   model.ReviewQuestion
	Title      string
	Priority   model.Priority
	Provenance model.Provenance
}
```

Detector rules:
- Exactly one `Signal` per detector; `Signal.ID == Detector.ID()`.
- Start from `engine.NewSignal(id)`; if `!c.HasSessions()` return `engine.NoSessions(id)`.
- Findings are ordered by time, then file path.
- Never mutate `c` or anything reachable from it.

### `internal/testkit` (T02)

```go
func Session(h model.Harness, id string) *SB  // CWD/RepoRoot "/repo", RepoRemote "acme/shop", clock 2026-09-27T14:00:00Z, +1 minute per event
func (b *SB) At(hhmm string) *SB             // next event at 2026-09-27T<hhmm>:00Z; later events continue +1 minute
func (b *SB) Model(m string) *SB             // model for subsequent assistant-side events (default "test-model")
func (b *SB) Agent(id string) *SB            // subsequent events get AgentID (""=main agent)
func (b *SB) Prompt(text string) *SB
func (b *SB) Steer(text string) *SB
func (b *SB) Say(text string) *SB            // assistant message
func (b *SB) Edit(rel string, added ...string) *SB
func (b *SB) Replace(rel string, removed, added []string) *SB
func (b *SB) Create(rel string, lines ...string) *SB
func (b *SB) Read(rel string) *SB
func (b *SB) Run(cmd string, exit int, output string) *SB   // agent command; exit<0 → Status unknown, ExitCode nil
func (b *SB) UserRun(cmd string, exit int) *SB
func (b *SB) Add(e model.Event) *SB          // any event; zero ID/TS/Origin are filled
func (b *SB) Build() *model.Session          // IDs "e1".."eN" unless set; calls Finalize

func PR(repo string, number int) *PB
func (p *PB) Add(path string, startLine int, lines ...string) *PB  // one added hunk
func (p *PB) Remove(path string, oldStart int, lines ...string) *PB
func (p *PB) Status(path string, st model.FileStatus) *PB
func (p *PB) Commit(sha, hhmm string) *PB
func (p *PB) Body(text string) *PB
func (p *PB) Build() *model.PR

func Ctx(pr *model.PR, sessions ...*model.Session) *engine.Context // ExactAttribution + config.Default(), links empty
func ExactAttribution(pr *model.PR, sessions []*model.Session) *model.Attribution
func Find(sigs []model.Signal, id string) *model.Signal
```

### `internal/pr` and `internal/diff` (T06)

```go
// package diff
func Parse(unified string) ([]model.DiffFile, error)

// package pr
type Runner interface{ Run(args ...string) ([]byte, error) } // runs `gh <args>`
type GH struct{}                                              // real runner
func Fetch(r Runner, repo string, number int, withCommitPatches bool) (*model.PR, error)
func ResolveArg(cwd, arg string) (repo string, number int, err error) // number or PR URL
```

### `internal/link` (T10)

```go
type Window struct{ From, To time.Time }
func WindowFor(pr *model.PR, cfg *config.Config) Window
func Discover(cfg *config.Config, w Window, withHooks bool) ([]*model.Session, error)
func Select(pr *model.PR, sessions []*model.Session, w Window) (cands []*model.Session, dropped int)
type Result struct {
	Sessions     []*model.Session            // linked, in start order
	Links        []model.CommitLink          // one per PR commit, PR order
	SessionLinks map[string]model.LinkMethod // Session.Ref() → method
	Dropped      int                         // Select drops + Finalize drops
	Unattributed []string
}
func Finalize(pr *model.PR, cands []*model.Session, attr *model.Attribution,
	commitSessions map[string][]string, droppedEarlier int) Result
```

### `internal/attrib` (T11)

```go
func Attribute(pr *model.PR, sessions []*model.Session, cfg *config.Config) *model.Attribution
func CommitSessions(pr *model.PR, sessions []*model.Session) map[string][]string // commit SHA → session refs
```

### `internal/render` (T22)

```go
type BuildInput struct {
	PR           *model.PR
	Sessions     []*model.Session
	Links        []model.CommitLink
	SessionLinks map[string]model.LinkMethod
	Attribution  *model.Attribution
	Signals      []model.Signal
	Dropped      int
	Unattributed []string
	Version      string
	Now          time.Time
}
func BuildReport(in BuildInput) *model.Report

type Options struct {
	OutDir          string // .paircli/pr-<n>
	IncludePrompts  bool
	MaxCommentLines int
}
func Write(opts Options, rep *model.Report, attr *model.Attribution, sessions []*model.Session) error
func ReportMarkdown(rep *model.Report) string
func CommentMarkdown(rep *model.Report, opts Options) string // starts with "<!-- paircli -->"
```

### `internal/llm` (T09)

```go
type Request struct {
	Model     string
	System    string
	Prompt    string
	MaxTokens int
}
type Response struct {
	Text                      string
	InputTokens, OutputTokens int
}
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}
func New(cfg config.LLM) (Provider, error) // Provider "none" or "" → (nil, nil)
type Fake struct {                         // for tests
	Replies []string                       // returned in order; error when exhausted
	Calls   []Request
}
func (f *Fake) Name() string
func (f *Fake) Complete(ctx context.Context, req Request) (Response, error)
```

### `internal/judge` (T23)

```go
func Run(ctx context.Context, p llm.Provider, ec *engine.Context, signals []model.Signal, cfg *config.Config) (*model.Judgments, error)
```

### `internal/enrich` (T26)

```go
func Apply(c *engine.Context, signals []model.Signal) []model.Signal // uses c.Judgments; nil → unchanged
func Evidence(c *engine.Context, cites []string) []model.Evidence    // "ev:<ref>/<event>" cites → Evidence
func Anchors(file, lines string, cites []string) []model.Anchor      // "file:<path>:<a>-<b>" cites → Anchors
```

### `internal/scan` (T24; T26 and T27 add small pieces)

```go
type Options struct {
	Repo            string
	Number          int
	CWD             string
	OutDir          string         // default <repo root or CWD>/.paircli/pr-<n>
	Runner          pr.Runner
	Config          *config.Config // nil → config.Load(repo root)
	NoHooks         bool
	NoCommitPatches bool
	NoAgentTrace    bool           // T27
	Post            bool
	IncludePrompts  bool
	LLM             llm.Provider   // nil = LLM pass off
	Now             time.Time
	Version         string
}
type Result struct {
	Report *model.Report
	OutDir string
	Alerts int
}
func Run(ctx context.Context, o Options) (*Result, error)
```

### `internal/agenttrace` (T27)

```go
func Write(dir string, pr *model.PR, attr *model.Attribution, sessions []*model.Session) error
```

## 3. Output files

`.paircli/pr-<n>/` (gitignored):

| File | Content | Writer |
|---|---|---|
| `signals.json` | `model.Report`, indented 2 spaces | T22 |
| `report.md` | Human report | T22 |
| `comment.md` | PR comment body, ≤ `comment.max_lines` alert lines | T22 |
| `authorship.json` | `model.Attribution` | T22 |
| `sessions/<harness>-<id>.json` | linked `model.Session` with events | T22 |
| `agent-trace.json` | Agent Trace records | T27 |
