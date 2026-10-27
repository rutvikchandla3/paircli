package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/publish"
	"github.com/rutvikchandla3/paircli/internal/state"
)

const (
	pubHead     = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	pubPubID    = "11111111-1111-4111-8111-111111111111"
	pubReviewID = "22222222-2222-4222-8222-222222222222"
	pubUserID   = "33333333-3333-4333-8333-333333333333"
	pubJobID    = "44444444-4444-4444-8444-444444444444"
	pubSecret   = "ghp_aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3"

	pubViewKey = "pr view 7 -R acme/shop --json number,url,title,body,headRefName,headRefOid,baseRefName,createdAt,commits"
	pubDiffKey = "pr diff 7 -R acme/shop"
)

const pubPRView = `{
  "number": 7, "url": "https://github.com/acme/shop/pull/7", "title": "Add retry with backoff",
  "body": "Adds retries to the webhook sender.", "headRefName": "feat/retry", "headRefOid": "` + pubHead + `",
  "baseRefName": "main", "createdAt": "2026-09-27T13:50:00Z",
  "commits": [{"oid": "aaaaaaa1", "messageHeadline": "Add retry", "messageBody": "", "committedDate": "2026-09-27T14:05:00Z"}]
}`

const pubDiff = `diff --git a/src/webhooks/retry.ts b/src/webhooks/retry.ts
index 1111111..2222222 100644
--- a/src/webhooks/retry.ts
+++ b/src/webhooks/retry.ts
@@ -10,1 +10,2 @@
 function send() {
+return sendWithRetry(url, body)
`

const pubCommit = `{"files":[{"filename":"src/webhooks/retry.ts","status":"modified","patch":"@@ -10,1 +10,2 @@\n function send() {\n+return sendWithRetry(url, body)"}]}`

// pubGH is the gh runner: canned scan output, and the calls the PR comment makes.
type pubGH struct {
	mu    sync.Mutex
	calls []string
	head  string // the PR's current head; "" = pubHead
}

func (g *pubGH) currentHead() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.head == "" {
		return pubHead
	}
	return g.head
}

// setHead simulates a push.
func (g *pubGH) setHead(head string) {
	g.mu.Lock()
	g.head = head
	g.mu.Unlock()
}

func (g *pubGH) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	g.mu.Lock()
	g.calls = append(g.calls, key)
	g.mu.Unlock()
	switch {
	case key == pubViewKey:
		return []byte(strings.Replace(pubPRView, pubHead, g.currentHead(), 1)), nil
	case key == "pr view 7 -R acme/shop --json headRefOid --jq .headRefOid":
		return []byte(g.currentHead() + "\n"), nil
	case key == pubDiffKey:
		return []byte(pubDiff), nil
	case key == "api repos/acme/shop/commits/aaaaaaa1":
		return []byte(pubCommit), nil
	case key == "api user --jq .login":
		return []byte("kousik\n"), nil
	case key == "api --paginate repos/acme/shop/issues/7/comments?per_page=100":
		return []byte("[]"), nil
	case strings.HasPrefix(key, "api --method POST repos/acme/shop/issues/7/comments -f body="):
		return []byte("{}"), nil
	}
	return nil, fmt.Errorf("pubGH: unexpected gh call: %s", key)
}

func (g *pubGH) called(prefix string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// pubAPI is the Pair backend: it reserves whatever it is asked for, finalizes
// with finalizeStatus and reports the scripted analysis states.
type pubAPI struct {
	srv            *httptest.Server
	mu             sync.Mutex
	paths          []string
	meta           publish.Metadata
	finalizeStatus int
	analysis       []string
	heads          []string // head_sha of every reservation, in order
	be             *beMain  // when set, answers like the Pair backend's main branch
}

func newPubAPI(t *testing.T) *pubAPI {
	a := &pubAPI{finalizeStatus: http.StatusAccepted, analysis: []string{"running", "succeeded"}}
	a.srv = httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *pubAPI) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paths = append(a.paths, r.Method+" "+r.URL.Path)
	if a.be != nil {
		a.be.handle(w, r)
		return
	}
	reply := func(status int, v any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"data": v})
	}
	m := a.meta
	switch {
	case r.URL.Path == "/api/v1/reviews/publications/upload-url":
		if err := json.NewDecoder(r.Body).Decode(&a.meta); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m = a.meta
		a.heads = append(a.heads, m.HeadSHA)
		digest, _ := hex.DecodeString(m.SHA256)
		reply(http.StatusCreated, map[string]any{
			"publication_id": pubPubID, "review_id": pubReviewID, "owner": m.Owner, "repo": m.Repo,
			"pull_number": m.PullNumber, "head_sha": m.HeadSHA, "revision": m.Revision, "sha256": m.SHA256,
			"size": m.Size, "state": "reserved",
			"upload": map[string]any{
				"method": "POST", "url": "https://bucket.s3.us-east-1.amazonaws.com",
				"fields": map[string]string{
					"key":          fmt.Sprintf("v1/reviews/9/%d/%s/bundles/%s/%d/%s.zip", m.PullNumber, m.HeadSHA, pubUserID, m.Revision, m.SHA256),
					"Content-Type": "application/zip", "x-amz-checksum-algorithm": "SHA256",
					"x-amz-checksum-sha256": base64.StdEncoding.EncodeToString(digest),
				},
			},
		})
	case strings.HasSuffix(r.URL.Path, "/finalize"):
		if a.finalizeStatus >= 400 {
			w.WriteHeader(a.finalizeStatus)
			io.WriteString(w, `{"code":"CONFLICT"}`)
			return
		}
		reply(a.finalizeStatus, map[string]any{
			"publication_id": pubPubID, "review_id": pubReviewID, "head_sha": m.HeadSHA, "revision": m.Revision,
			"sha256": m.SHA256, "size": m.Size, "state": "finalized", "finalized_at": "2026-10-11T00:00:00Z",
			"content_version": 3, "analysis_job_id": pubJobID, "analysis_state": "queued",
		})
	case strings.HasSuffix(r.URL.Path, "/analysis"):
		current := a.analysis[0]
		if len(a.analysis) > 1 {
			a.analysis = a.analysis[1:]
		}
		var code any
		if current == "failed" {
			code = "model_unavailable"
		}
		reply(http.StatusOK, map[string]any{
			"publication_id": pubPubID, "job_id": pubJobID, "state": current, "pipeline_version": "1",
			"attempt_count": 1, "max_attempts": 5, "error_code": code, "updated_at": "2026-10-11T00:00:00Z", "completed_at": nil,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *pubAPI) sawPath(p string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, seen := range a.paths {
		if seen == p {
			return true
		}
	}
	return false
}

// pubS3 captures uploads to the S3 host instead of dialing it.
type pubS3 struct {
	mu     sync.Mutex
	bodies [][]byte
	types  []string
}

func (s *pubS3) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies, s.types = append(s.bodies, body), append(s.types, r.Header.Get("Content-Type"))
	s.mu.Unlock()
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

// uploadedZip returns the zip the first S3 upload carried, by entry name.
func (s *pubS3) uploadedZip(t *testing.T) map[string][]byte {
	t.Helper()
	if len(s.bodies) == 0 {
		t.Fatal("nothing was uploaded to S3")
	}
	_, params, err := mime.ParseMediaType(s.types[0])
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(s.bodies[0]), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("no file part in the upload: %v", err)
		}
		if part.FormName() != "file" {
			continue
		}
		raw, _ := io.ReadAll(part)
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			files[f.Name], _ = io.ReadAll(rc)
			rc.Close()
		}
		return files
	}
}

// pubHarness wires publishRun to fakes and a repository with one linked
// Claude Code session.
type pubHarness struct {
	t       *testing.T
	repoDir string
	gh      *pubGH
	api     *pubAPI
	s3      *pubS3
	env     publishEnv
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer

	credentialCalls int
	credentialErr   error
	notices         []string
	sleeps          []time.Duration
}

func newPubHarness(t *testing.T) *pubHarness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	h := &pubHarness{t: t, repoDir: t.TempDir(), gh: &pubGH{}, api: newPubAPI(t), s3: &pubS3{},
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	projects, other := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(h.repoDir, ".git", "HEAD"), "ref: refs/heads/feat/retry\n")
	mustWrite(t, filepath.Join(h.repoDir, ".git", "config"),
		"[remote \"origin\"]\n\turl = git@github.com:acme/shop.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n")
	roots, _ := json.Marshal(map[string]any{"roots": map[string]string{
		"claude_code": projects, "codex": other, "pi": t.TempDir(), "hook_log": t.TempDir(),
	}})
	mustWrite(t, filepath.Join(h.repoDir, ".paircli.json"), string(roots))
	writePubTranscript(t, projects, h.repoDir)

	h.env = publishEnv{
		runner: h.gh,
		credential: func(out, progress io.Writer) (string, string, error) {
			h.credentialCalls++
			if h.credentialErr != nil {
				return "", "", h.credentialErr
			}
			return h.api.srv.URL, "test-token", nil
		},
		newClient: func(server, token string) (*publish.Client, error) {
			c, err := publish.NewClient(server, token)
			if err != nil {
				return nil, err
			}
			c.Upload = &http.Client{Transport: h.s3}
			c.Backoff = []time.Duration{0, 0, 0}
			return c, nil
		},
		notify: func(title, message string) bool {
			h.notices = append(h.notices, title+": "+message)
			return true
		},
		sleep: func(ctx context.Context, d time.Duration) error {
			h.sleeps = append(h.sleeps, d)
			return nil
		},
		now:    func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) },
		stdout: h.stdout,
		stderr: h.stderr,
	}
	return h
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writePubTranscript writes the session scan links to the PR; its prompt
// carries a secret that must not survive into the bundle.
func writePubTranscript(t *testing.T, projects, repoRoot string) {
	t.Helper()
	file := filepath.Join(repoRoot, "src", "webhooks", "retry.ts")
	lines := []string{
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-27T14:00:00.000Z","cwd":"` + repoRoot + `","gitBranch":"feat/retry","version":"2.1.283","promptSource":"typed","message":{"role":"user","content":"Add retry with backoff to the webhook sender. My token is ` + pubSecret + `"}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-27T14:00:30.000Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"I'll add the retry call."},{"type":"tool_use","id":"toolu_01A","name":"Write","input":{"file_path":"` + file + `","content":"return sendWithRetry(url, body)\n"}}]}}`,
		`{"type":"user","uuid":"u2","sessionId":"s1","timestamp":"2026-09-27T14:00:31.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01A","content":"File created"}]},"toolUseResult":{"type":"create","filePath":"` + file + `"}}`,
	}
	path := filepath.Join(projects, "-tmp-shop", "s1.jsonl")
	mustWrite(t, path, strings.Join(lines, "\n")+"\n")
	stamp := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func (h *pubHarness) run(flags ...string) error {
	h.t.Helper()
	a, err := parsePublishArgs(append([]string{"7", "--repo-dir", h.repoDir}, flags...))
	if err != nil {
		h.t.Fatal(err)
	}
	return publishRun(context.Background(), h.env, a)
}

func (h *pubHarness) record() state.Publication {
	h.t.Helper()
	rec, err := state.ReadPublication("acme", "shop", 7)
	if err != nil {
		h.t.Fatalf("no publication record: %v", err)
	}
	return rec
}

func TestPublishEndToEndWithWait(t *testing.T) {
	h := newPubHarness(t)
	if err := h.run("--wait"); err != nil {
		t.Fatalf("publish: %v\nstdout:\n%s\nstderr:\n%s", err, h.stdout, h.stderr)
	}

	rec := h.record()
	if rec.State != state.StateReady || rec.Step != state.StepAnalysis || rec.Error != "" {
		t.Errorf("record state/step/error = %s/%s/%q", rec.State, rec.Step, rec.Error)
	}
	if rec.HeadSHA != pubHead || rec.PublicationID != pubPubID || rec.AnalysisJobID != pubJobID || rec.AnalysisState != "succeeded" {
		t.Errorf("record = %+v", rec)
	}
	if rec.PRURL != "https://github.com/acme/shop/pull/7" || rec.ReviewURL != "https://pair.sh/acme/shop/pull/7" || rec.Owner != "acme" || rec.Repo != "shop" || rec.Number != 7 {
		t.Errorf("record identity = %+v", rec)
	}
	if data, err := os.ReadFile(rec.Log); err != nil || !strings.Contains(string(data), "published revision") {
		t.Errorf("log %q: %v", rec.Log, err)
	}

	files := h.s3.uploadedZip(t)
	prefix := ".paircli/pr-7/" + pubHead + "/"
	for _, name := range []string{"bundle.json", prefix + "signals.json", prefix + "snapshot.json", prefix + "report.md",
		"raw-sessions/manifest.json", "raw-sessions/claude-code/s1/s1.jsonl"} {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle is missing %s", name)
		}
	}
	for name, content := range files {
		if bytes.Contains(content, []byte(pubSecret)) {
			t.Errorf("%s still carries the secret", name)
		}
	}
	var meta struct {
		HeadSHA     string `json:"head_sha"`
		RawSessions bool   `json:"raw_sessions"`
		Redaction   struct {
			Counts map[string]int `json:"counts"`
		} `json:"redaction"`
	}
	json.Unmarshal(files["bundle.json"], &meta)
	if meta.HeadSHA != pubHead || !meta.RawSessions || meta.Redaction.Counts["github_token"] < 1 {
		t.Errorf("bundle.json = %s", files["bundle.json"])
	}

	for _, name := range []string{"capture-cursors.json", "revisions.json"} {
		if _, err := os.Stat(filepath.Join(state.Dir(), name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !h.gh.called("api --method POST repos/acme/shop/issues/7/comments") {
		t.Error("the Pair link comment was not posted")
	}
	if len(h.notices) != 2 || !strings.HasPrefix(h.notices[0], "Pair review ready") || !strings.Contains(h.notices[1], "analysis finished") {
		t.Errorf("notices = %q", h.notices)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 5*time.Second {
		t.Errorf("sleeps = %v, want one 5s poll interval", h.sleeps)
	}
	if !strings.Contains(h.stdout.String(), "published https://pair.sh/acme/shop/pull/7") {
		t.Errorf("stdout = %q", h.stdout)
	}
	if _, err := os.Stat(filepath.Join(h.repoDir, ".paircli", "pr-7", pubHead, "signals.json")); err != nil {
		t.Errorf("scan output is not under the head folder: %v", err)
	}
}

func TestPublishJSONOutputKeepsStdoutClean(t *testing.T) {
	h := newPubHarness(t)
	if err := h.run("--json", "--no-comment"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var out publishSummary
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, h.stdout)
	}
	if out.State != state.StateProcessing || out.PublicationID != pubPubID || out.AnalysisJobID != pubJobID || out.HeadSHA != pubHead || out.Revision == 0 {
		t.Errorf("summary = %+v", out)
	}
	if !strings.Contains(h.stderr.String(), "published revision") {
		t.Errorf("log lines belong on stderr in --json mode: %q", h.stderr)
	}
	if h.gh.called("api --method POST") {
		t.Error("--no-comment still posted")
	}
	if rec := h.record(); rec.State != state.StateProcessing || rec.Step != state.StepAnalysis {
		t.Errorf("without --wait the record stays processing, got %s/%s", rec.State, rec.Step)
	}
	if h.api.sawPath("GET /api/v1/reviews/publications/" + pubPubID + "/analysis") {
		t.Error("the analysis was polled without --wait")
	}
}

func TestPublishDryRunBuildsTheBundleAndTouchesNothing(t *testing.T) {
	h := newPubHarness(t)
	if err := h.run("--dry-run", "--json"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	var out publishSummary
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout: %v\n%s", err, h.stdout)
	}
	if !out.DryRun || out.Bundle == nil || out.HeadSHA != pubHead || !out.Bundle.RawIncluded || out.Bundle.RawFiles != 1 {
		t.Fatalf("plan = %+v / %+v", out, out.Bundle)
	}
	if _, err := os.Stat(out.Bundle.Path); err != nil {
		t.Errorf("the bundle was not kept for inspection: %v", err)
	}
	if h.credentialCalls != 0 || len(h.api.paths) != 0 || len(h.s3.bodies) != 0 || len(h.notices) != 0 {
		t.Errorf("dry run reached out: credential=%d api=%v s3=%d notices=%v", h.credentialCalls, h.api.paths, len(h.s3.bodies), h.notices)
	}
	if h.gh.called("api --method") {
		t.Error("dry run posted a comment")
	}
	if _, err := state.ReadPublication("acme", "shop", 7); err == nil {
		t.Error("dry run wrote a publication record")
	}
	if _, err := os.Stat(filepath.Join(state.Dir(), "capture-cursors.json")); err == nil {
		t.Error("dry run advanced capture cursors")
	}
}

func TestPublishDryRunPrintsAHumanPlan(t *testing.T) {
	h := newPubHarness(t)
	if err := h.run("--dry-run"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dry run for https://github.com/acme/shop/pull/7", pubHead, "bundle:", "would publish to"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, h.stdout)
		}
	}
}

func TestPublishFailureIsRecordedAndCursorsStay(t *testing.T) {
	h := newPubHarness(t)
	h.api.finalizeStatus = http.StatusConflict
	err := h.run()
	if err == nil {
		t.Fatal("a rejected finalize must fail the run")
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		t.Errorf("a plain failure must exit 1, got a coded error %d", coded.ExitCode())
	}
	rec := h.record()
	if rec.State != state.StateFailed || rec.Step != state.StepFinalize || !strings.Contains(rec.Error, "conflicts") || rec.PublicationID != pubPubID {
		t.Errorf("record = %+v", rec)
	}
	if _, statErr := os.Stat(filepath.Join(state.Dir(), "capture-cursors.json")); statErr == nil {
		t.Error("cursors advanced although the publication failed")
	}
	if h.gh.called("api --method POST") {
		t.Error("a failed publication posted the PR comment")
	}
	if len(h.notices) != 1 || !strings.HasPrefix(h.notices[0], "Pair review failed") {
		t.Errorf("notices = %q", h.notices)
	}
}

func TestPublishFailedAnalysisFailsTheWait(t *testing.T) {
	h := newPubHarness(t)
	h.api.analysis = []string{"running", "failed"}
	if err := h.run("--wait"); err == nil || !strings.Contains(err.Error(), "model_unavailable") {
		t.Fatalf("err = %v", err)
	}
	if rec := h.record(); rec.State != state.StateFailed || rec.AnalysisState != "failed" {
		t.Errorf("record = %+v", rec)
	}
}

func TestPublishWaitTimeoutLeavesTheRecordProcessing(t *testing.T) {
	h := newPubHarness(t)
	h.api.analysis = []string{"running"}
	err := h.run("--wait", "--timeout", "1ns")
	if err == nil || !strings.Contains(err.Error(), "had not finished") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if rec := h.record(); rec.State != state.StateProcessing {
		t.Errorf("record state = %s, want processing", rec.State)
	}
}

func TestPublishPollIntervalGrowsToFifteenSeconds(t *testing.T) {
	h := newPubHarness(t)
	h.api.analysis = []string{"running", "running", "running", "running", "running", "succeeded"}
	if err := h.run("--wait"); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{5 * time.Second, 7500 * time.Millisecond, 11250 * time.Millisecond, 15 * time.Second, 15 * time.Second}
	if fmt.Sprint(h.sleeps) != fmt.Sprint(want) {
		t.Errorf("sleeps = %v, want %v", h.sleeps, want)
	}
}

func TestPublishNotLoggedInExitsWithCodeTwo(t *testing.T) {
	h := newPubHarness(t)
	h.credentialErr = errors.New("login denied by user")
	err := h.run()
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	if rec := h.record(); rec.State != state.StateFailed || !strings.Contains(rec.Error, "not logged in") {
		t.Errorf("record = %+v", rec)
	}
	if len(h.api.paths) != 0 || len(h.s3.bodies) != 0 {
		t.Error("an unauthenticated run reached the backend")
	}
}

func TestPublishLoserLeavesAPendingMarkerAndExits(t *testing.T) {
	h := newPubHarness(t)
	key := state.Key("acme", "shop", 7)
	lock, err := state.TryLock(key)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	if err := h.run(); err != nil {
		t.Fatalf("a locked PR must exit successfully, got %v", err)
	}
	if !strings.Contains(h.stdout.String(), "already being published") {
		t.Errorf("stdout = %q", h.stdout)
	}
	if h.credentialCalls != 0 || len(h.api.paths) != 0 {
		t.Error("the second run did work")
	}
	if _, err := state.ReadPublication("acme", "shop", 7); err == nil {
		t.Error("the second run overwrote the first run's record")
	}
	if !state.HasPending(key) {
		t.Fatal("the losing run left no pending marker")
	}
	req, _ := state.TakePending(key)
	if req.PRURL != "https://github.com/acme/shop/pull/7" || req.RepoDir != h.repoDir || req.RequestedAt.IsZero() {
		t.Errorf("marker = %+v", req)
	}
}

func TestPublishDryRunLoserLeavesNoMarker(t *testing.T) {
	h := newPubHarness(t)
	lock, _ := state.TryLock(state.Key("acme", "shop", 7))
	defer lock.Release()
	if err := h.run("--dry-run"); err != nil {
		t.Fatal(err)
	}
	if state.HasPending(state.Key("acme", "shop", 7)) {
		t.Error("a dry run asked the running publish to go again")
	}
}

// pushDuringRun makes the first n "review ready" notifications behave like a
// push plus a losing `paircli publish` that arrived mid-run: the PR gets a new
// head and a pending marker appears.
func (h *pubHarness) pushDuringRun(n int, heads func(i int) string) {
	h.env.notify = func(title, message string) bool {
		h.notices = append(h.notices, title)
		if i := len(h.notices); i <= n {
			if head := heads(i); head != "" {
				h.gh.setHead(head)
			}
			state.MarkPending(state.Key("acme", "shop", 7), state.PendingRequest{PRURL: "https://github.com/acme/shop/pull/7", RepoDir: h.repoDir})
		}
		return true
	}
}

func TestPublishRerunsForAPushThatArrivedMidRun(t *testing.T) {
	h := newPubHarness(t)
	newHead := strings.Repeat("a", 40)
	h.pushDuringRun(1, func(int) string { return newHead })

	if err := h.run("--no-comment"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if fmt.Sprint(h.api.heads) != fmt.Sprint([]string{pubHead, newHead}) {
		t.Errorf("published heads = %v, want the old head and then the pushed one", h.api.heads)
	}
	if state.HasPending(state.Key("acme", "shop", 7)) {
		t.Error("the marker was not removed")
	}
	rec := h.record()
	if rec.HeadSHA != newHead || rec.State != state.StateProcessing {
		t.Errorf("the record must end with the last run's result, got %s %s", rec.HeadSHA, rec.State)
	}
	log, _ := os.ReadFile(rec.Log)
	if !strings.Contains(string(log), "publishing again") || strings.Count(string(log), "publishing https://github.com/acme/shop/pull/7") != 2 {
		t.Errorf("log does not show both runs:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(h.repoDir, ".paircli", "pr-7", newHead, "signals.json")); err != nil {
		t.Errorf("the rerun did not scan the new head: %v", err)
	}
}

func TestPublishRerunForAnUnchangedHeadDoesNothing(t *testing.T) {
	h := newPubHarness(t)
	h.pushDuringRun(1, func(int) string { return "" })

	if err := h.run("--no-comment"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(h.api.heads) != 1 {
		t.Errorf("reservations = %d, want 1: an unchanged head is not published twice", len(h.api.heads))
	}
	if state.HasPending(state.Key("acme", "shop", 7)) {
		t.Error("the marker was not removed")
	}
	if rec := h.record(); rec.State != state.StateProcessing || rec.HeadSHA != pubHead {
		t.Errorf("record = %+v", rec)
	}
}

func TestPublishRerunsAtMostThreeTimes(t *testing.T) {
	h := newPubHarness(t)
	h.pushDuringRun(10, func(i int) string { return strings.Repeat(string("abcdefgh"[i-1]), 40) })

	if err := h.run("--no-comment"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	want := []string{pubHead, strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)}
	if fmt.Sprint(h.api.heads) != fmt.Sprint(want) {
		t.Errorf("published heads = %v, want 1 run + 3 reruns %v", h.api.heads, want)
	}
	if !state.HasPending(state.Key("acme", "shop", 7)) {
		t.Error("the request made during the last allowed run must stay for the next publish")
	}
	if rec := h.record(); rec.HeadSHA != strings.Repeat("c", 40) {
		t.Errorf("record head = %s", rec.HeadSHA)
	}
}

func TestPublishSameHeadReplaysItsRawSessions(t *testing.T) {
	h := newPubHarness(t)
	if err := h.run("--no-comment"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("--no-comment"); err != nil {
		t.Fatal(err)
	}
	if len(h.s3.bodies) != 2 {
		t.Fatalf("uploads = %d, want 2", len(h.s3.bodies))
	}
	h.s3.bodies = h.s3.bodies[1:]
	h.s3.types = h.s3.types[1:]
	if _, ok := h.s3.uploadedZip(t)["raw-sessions/claude-code/s1/s1.jsonl"]; !ok {
		t.Error("republishing the same head must repeat its raw-session range")
	}
}

func TestParsePublishArgs(t *testing.T) {
	got, err := parsePublishArgs([]string{"https://github.com/acme/shop/pull/9", "--repo-dir", "/r", "--wait", "--timeout", "90s", "--dry-run", "--no-comment", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	want := publishArgs{PRArg: "https://github.com/acme/shop/pull/9", RepoDir: "/r", Wait: true, Timeout: 90 * time.Second, DryRun: true, NoComment: true, JSON: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if def, _ := parsePublishArgs([]string{"7"}); def.Timeout != 20*time.Minute || def.Wait || def.DryRun {
		t.Errorf("defaults = %+v", def)
	}
	for _, bad := range [][]string{nil, {"--wait", "7"}, {"7", "extra"}, {"7", "--nope"}} {
		if _, err := parsePublishArgs(bad); err == nil {
			t.Errorf("parsePublishArgs(%v) accepted bad input", bad)
		}
	}
}
