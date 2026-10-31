package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/deviceauth"
	"github.com/rutvikchandla3/paircli/internal/publish"
	"github.com/rutvikchandla3/paircli/internal/state"
)

func statusJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := statusRun(append([]string{"--json"}, args...), &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("status output is not JSON: %v (%q)", err, out.String())
	}
	return got
}

func writePub(t *testing.T, owner, repo string, number int, st string) {
	t.Helper()
	err := state.WritePublication(state.Publication{
		PRURL: "https://github.com/" + owner + "/" + repo + "/pull/1", Owner: owner, Repo: repo, Number: number,
		HeadSHA: strings.Repeat("a", 40), State: st, Step: state.StepUpload,
		ReviewURL: state.ReviewURL(owner, repo, number),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatus_JSONShapeWhenFresh(t *testing.T) {
	setupEnv(t)
	got := statusJSON(t)

	if got["schema_version"] != float64(1) || got["cli_version"] != version || got["server_url"] != "https://pair.example" {
		t.Errorf("header fields = %v", got)
	}
	if got["logged_in"] != false {
		t.Errorf("logged_in = %v, want false", got["logged_in"])
	}
	hooks, _ := got["hooks"].(map[string]any)
	for _, name := range []string{"claude-code", "codex", "pi"} {
		if v, ok := hooks[name]; !ok || v != false {
			t.Errorf("hooks[%s] = %v, %v, want false", name, v, ok)
		}
	}
	setup, _ := got["setup"].(map[string]any)
	if setup["state"] != "idle" || setup["error"] != nil {
		t.Errorf("setup = %v, want idle with a null error", setup)
	}
	pubs, ok := got["publications"].([]any)
	if !ok || len(pubs) != 0 {
		t.Errorf("publications = %v, want an empty array", got["publications"])
	}
}

func TestStatus_LoggedInAndNoCredentialPrinted(t *testing.T) {
	setupEnv(t)
	t.Setenv(deviceauth.EnvToken, "tok-secret-value")
	var out bytes.Buffer
	if err := statusRun([]string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "tok-secret-value") {
		t.Error("status printed the credential")
	}
	var got map[string]any
	_ = json.Unmarshal(out.Bytes(), &got)
	if got["logged_in"] != true {
		t.Errorf("logged_in = %v, want true", got["logged_in"])
	}
}

func TestStatus_CredentialForAnotherServerIsNotLoggedIn(t *testing.T) {
	setupEnv(t)
	if err := deviceauth.Save(deviceauth.DefaultFile(), deviceauth.StoredAuth{
		AccessToken: "t", ServerURL: "https://other.example", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if got := statusJSON(t); got["logged_in"] != false {
		t.Errorf("logged_in = %v, want false for another server's credential", got["logged_in"])
	}
}

func TestStatus_ListsPublicationsAndFiltersByPR(t *testing.T) {
	setupEnv(t)
	writePub(t, "five-sh", "awt", 12, state.StateReady)
	writePub(t, "five-sh", "awt", 13, state.StateFailed)

	all, _ := statusJSON(t)["publications"].([]any)
	if len(all) != 2 {
		t.Fatalf("publications = %v, want 2", all)
	}
	one, _ := statusJSON(t, "--pr", "https://github.com/Five-SH/awt/pull/13")["publications"].([]any)
	if len(one) != 1 || one[0].(map[string]any)["number"] != float64(13) || one[0].(map[string]any)["state"] != "failed" {
		t.Errorf("filtered publications = %v, want only #13", one)
	}
	none, _ := statusJSON(t, "--pr", "https://github.com/other/repo/pull/1")["publications"].([]any)
	if len(none) != 0 {
		t.Errorf("publications for an unknown PR = %v, want none", none)
	}
}

func TestStatus_BadPRFlag(t *testing.T) {
	setupEnv(t)
	if err := statusRun([]string{"--pr", "not-a-url"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error for a --pr that is not a GitHub pull request URL")
	}
}

func TestStatus_ReportsAFailedSetupWithItsError(t *testing.T) {
	setupEnv(t)
	msg := "login: device code expired"
	if err := writeSetupStatus(setupStatus{State: setupFailed, Error: &msg, Log: "/logs/setup.log"}); err != nil {
		t.Fatal(err)
	}
	setup, _ := statusJSON(t)["setup"].(map[string]any)
	if setup["state"] != "failed" || setup["error"] != msg || setup["log"] != "/logs/setup.log" {
		t.Errorf("setup = %v", setup)
	}
}

func TestStatus_RunningSetupWithoutALiveProcessIsFailed(t *testing.T) {
	setupEnv(t)
	if err := writeSetupStatus(setupStatus{State: setupRunning}); err != nil {
		t.Fatal(err)
	}
	setup, _ := statusJSON(t)["setup"].(map[string]any)
	if setup["state"] != "failed" || setup["error"] == nil {
		t.Errorf("setup = %v, want a stale running record reported as failed", setup)
	}

	lock, err := state.TryLock("setup")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	setup, _ = statusJSON(t)["setup"].(map[string]any)
	if setup["state"] != "running" {
		t.Errorf("setup = %v, want running while the lock is held", setup)
	}
}

func TestStatus_HumanOutput(t *testing.T) {
	setupEnv(t)
	writePub(t, "five-sh", "awt", 12, state.StateProcessing)
	var out bytes.Buffer
	if err := statusRun(nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"not logged in", "claude-code no", "setup:  idle", "five-sh/awt#12", "uploaded", "https://pair.sh/five-sh/awt/pull/12"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "processing") {
		t.Errorf("output speaks of processing:\n%s", out.String())
	}
}

// `paircli status` says what the publisher did, never where Pair's analysis
// stands: no analysis state, no error code, no "failed" for the analysis.
func TestStatus_HumanOutputIsNeutralAboutTheAnalysis(t *testing.T) {
	setupEnv(t)
	records := []state.Publication{
		{Number: 1, State: state.StateProcessing, Step: state.StepAnalysis, AnalysisState: "running"},
		{Number: 2, State: state.StateReady, Step: state.StepAnalysis, AnalysisState: "succeeded"},
		{Number: 3, State: state.StateReady, Step: state.StepAnalysis, AnalysisState: "failed"},
		// What an earlier paircli left behind for a failed analysis.
		{Number: 4, State: state.StateFailed, Step: state.StepAnalysis, AnalysisState: "failed", Error: "the analysis failed (model_unavailable)", Log: "/logs/p4.log"},
	}
	for _, p := range records {
		p.Owner, p.Repo, p.ReviewURL = "o", "r", state.ReviewURL("o", "r", p.Number)
		if err := state.WritePublication(p); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := statusRun(nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"analysis", "failed", "model_unavailable", "error:", "log:", "processing", "running", "succeeded"} {
		if strings.Contains(strings.ToLower(out.String()), banned) {
			t.Errorf("output mentions %q:\n%s", banned, out.String())
		}
	}
	for number, want := range map[int]string{1: "uploaded", 2: "ready", 3: "ready", 4: "ready"} {
		line := lineWith(out.String(), fmt.Sprintf("o/r#%d ", number))
		if !strings.Contains(line, want) {
			t.Errorf("PR %d line = %q, want %q", number, line, want)
		}
	}
}

// `status --json` is as neutral as the human output: no analysis field, the
// states read uploaded or ready, and a failed analysis is neither an error nor
// a step.
func TestStatus_JSONIsNeutralAboutTheAnalysis(t *testing.T) {
	setupEnv(t)
	records := []state.Publication{
		{Number: 1, State: state.StateProcessing, Step: state.StepAnalysis, AnalysisJobID: testJobID, AnalysisState: "running"},
		{Number: 2, State: state.StateReady, Step: state.StepAnalysis, AnalysisJobID: testJobID, AnalysisState: "failed"},
		// What an earlier paircli left behind for a failed analysis.
		{Number: 3, State: state.StateFailed, Step: state.StepAnalysis, AnalysisJobID: testJobID, AnalysisState: "failed",
			Error: "the analysis failed (model_unavailable)", Log: "/logs/p3.log"},
		{Number: 4, State: state.StateFailed, Step: state.StepFinalize, Error: "Pair rejected the publication"},
	}
	for _, p := range records {
		p.Owner, p.Repo, p.ReviewURL = "o", "r", state.ReviewURL("o", "r", p.Number)
		if err := state.WritePublication(p); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := statusRun([]string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"analysis", "processing", "model_unavailable", "succeeded", testJobID} {
		if strings.Contains(out.String(), banned) {
			t.Errorf("--json output mentions %q:\n%s", banned, out.String())
		}
	}
	var got struct {
		Publications []map[string]any `json:"publications"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	type want struct{ state, step, errText string }
	wants := map[float64]want{
		1: {"uploaded", "finalize", ""},
		2: {"ready", "finalize", ""},
		3: {"ready", "finalize", ""},
		4: {"failed", "finalize", "Pair rejected the publication"},
	}
	if len(got.Publications) != len(wants) {
		t.Fatalf("publications = %v", got.Publications)
	}
	for _, p := range got.Publications {
		w := wants[p["number"].(float64)]
		if p["state"] != w.state || p["step"] != w.step || p["error"] != w.errText {
			t.Errorf("PR %v = state %v, step %v, error %v; want %+v", p["number"], p["state"], p["step"], p["error"], w)
		}
		if p["publication_id"] == nil || p["review_url"] == nil || p["head_sha"] == nil {
			t.Errorf("PR %v lost a field: %v", p["number"], p)
		}
	}
}

// A run that stopped before the upload completed is still reported as failed,
// with its step, error and log.
func TestStatus_HumanOutputKeepsRealFailures(t *testing.T) {
	setupEnv(t)
	err := state.WritePublication(state.Publication{
		Owner: "o", Repo: "r", Number: 1, State: state.StateFailed, Step: state.StepFinalize,
		Error: "Pair rejected the publication", Log: "/logs/p1.log", ReviewURL: state.ReviewURL("o", "r", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := statusRun(nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"failed (finalize)", "error: Pair rejected the publication", "log: /logs/p1.log"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func lineWith(text, sub string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

// --- analysis refresh ---

const testJobID = "11111111-2222-4333-8444-555555555555"

// fakeAnalysis answers AnalysisStatus per publication id and counts requests.
type fakeAnalysis struct {
	mu      sync.Mutex
	answers map[string]*publish.Analysis
	err     error
	asked   []string
}

func (f *fakeAnalysis) AnalysisStatus(ctx context.Context, id string) (*publish.Analysis, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, id)
	if f.err != nil {
		return nil, f.err
	}
	if an, ok := f.answers[id]; ok {
		return an, nil
	}
	return nil, publish.ErrNoAnalysis
}

// useAnalysis makes status reach the fake as a logged-in client.
func useAnalysis(t *testing.T, f *fakeAnalysis) {
	t.Helper()
	statusAnalysisReader = func() (analysisReader, bool) { return f, true }
}

func writeProcessing(t *testing.T, number int, publicationID string) {
	t.Helper()
	err := state.WritePublication(state.Publication{
		PRURL: fmt.Sprintf("https://github.com/o/r/pull/%d", number), Owner: "o", Repo: "r", Number: number,
		State: state.StateProcessing, Step: state.StepAnalysis, PublicationID: publicationID,
		ReviewURL: state.ReviewURL("o", "r", number),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func analysis(st string, code *string) *publish.Analysis {
	return &publish.Analysis{JobID: testJobID, State: st, ErrorCode: code}
}

func storedPublication(t *testing.T, number int) state.Publication {
	t.Helper()
	p, err := state.ReadPublication("o", "r", number)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStatusRefresh_SucceededBecomesReady(t *testing.T) {
	setupEnv(t)
	writeProcessing(t, 1, "pub-1")
	useAnalysis(t, &fakeAnalysis{answers: map[string]*publish.Analysis{"pub-1": analysis(publish.AnalysisSucceeded, nil)}})

	pubs, _ := statusJSON(t)["publications"].([]any)
	if got := pubs[0].(map[string]any)["state"]; got != "ready" {
		t.Errorf("reported state = %v, want ready", got)
	}
	stored := storedPublication(t, 1)
	if stored.State != state.StateReady || stored.AnalysisState != "succeeded" || stored.AnalysisJobID != testJobID {
		t.Errorf("stored record = %+v, want ready with the job", stored)
	}
}

func TestStatusRefresh_FailedBecomesReadyAndTheCodeGoesToTheLog(t *testing.T) {
	setupEnv(t)
	logPath := filepath.Join(t.TempDir(), "publish.log")
	if err := os.WriteFile(logPath, []byte("earlier line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeProcessing(t, 1, "pub-1")
	p := storedPublication(t, 1)
	p.Log = logPath
	if err := state.WritePublication(p); err != nil {
		t.Fatal(err)
	}
	code := "model_timeout"
	useAnalysis(t, &fakeAnalysis{answers: map[string]*publish.Analysis{"pub-1": analysis(publish.AnalysisFailed, &code)}})

	var out bytes.Buffer
	if err := statusRun(nil, &out); err != nil {
		t.Fatal(err)
	}
	stored := storedPublication(t, 1)
	if stored.State != state.StateReady || stored.Error != "" {
		t.Errorf("stored record = %+v, want ready with no error", stored)
	}
	if strings.Contains(out.String(), code) || strings.Contains(out.String(), "failed") {
		t.Errorf("status output shows the failure:\n%s", out.String())
	}
	log, _ := os.ReadFile(logPath)
	if !strings.HasPrefix(string(log), "earlier line\n") || !strings.Contains(string(log), "analysis failed (model_timeout)") {
		t.Errorf("log = %q, want the code appended", log)
	}
}

func TestStatusRefresh_QueuedStaysProcessingAndIsNotRewritten(t *testing.T) {
	setupEnv(t)
	writeProcessing(t, 1, "pub-1")
	before := storedPublication(t, 1)
	useAnalysis(t, &fakeAnalysis{answers: map[string]*publish.Analysis{"pub-1": analysis(publish.AnalysisQueued, nil)}})

	statusJSON(t)
	after := storedPublication(t, 1)
	if after.State != state.StateProcessing || after.AnalysisState != "queued" {
		t.Errorf("stored record = %+v, want processing, queued", after)
	}

	// The same answer again changes nothing, so the record is left alone.
	statusJSON(t)
	if again := storedPublication(t, 1); !again.UpdatedAt.Equal(after.UpdatedAt) {
		t.Errorf("record rewritten without a change: %v then %v (first write %v)", after.UpdatedAt, again.UpdatedAt, before.UpdatedAt)
	}
}

func TestStatusRefresh_UploadedBecomesProcessing(t *testing.T) {
	setupEnv(t)
	writeProcessing(t, 1, "pub-1")
	p := storedPublication(t, 1)
	p.State = state.StateUploaded
	if err := state.WritePublication(p); err != nil {
		t.Fatal(err)
	}
	useAnalysis(t, &fakeAnalysis{answers: map[string]*publish.Analysis{"pub-1": analysis(publish.AnalysisRunning, nil)}})

	statusJSON(t)
	if got := storedPublication(t, 1); got.State != state.StateProcessing || got.AnalysisState != "running" {
		t.Errorf("stored record = %+v, want processing, running", got)
	}
}

func TestStatusRefresh_NetworkErrorKeepsTheRecord(t *testing.T) {
	setupEnv(t)
	writeProcessing(t, 1, "pub-1")
	before := storedPublication(t, 1)
	useAnalysis(t, &fakeAnalysis{err: errors.New("dial tcp: no route to host")})

	var out bytes.Buffer
	if err := statusRun([]string{"--json"}, &out); err != nil {
		t.Fatalf("status returned an error for a network failure: %v", err)
	}
	if after := storedPublication(t, 1); after != before {
		t.Errorf("record changed: %+v -> %+v", before, after)
	}
	if !strings.Contains(out.String(), `"state": "uploaded"`) {
		t.Errorf("output = %s, want the stored processing record read as uploaded", out.String())
	}
}

func TestStatusRefresh_NotLoggedInSkipsTheRefresh(t *testing.T) {
	setupEnv(t) // its default reader reports no credential
	writeProcessing(t, 1, "pub-1")
	calls := 0
	statusAnalysisReader = func() (analysisReader, bool) { calls++; return nil, false }

	pubs, _ := statusJSON(t)["publications"].([]any)
	if len(pubs) != 1 || pubs[0].(map[string]any)["state"] != "uploaded" {
		t.Errorf("publications = %v, want the stored processing record read as uploaded", pubs)
	}
	if calls != 1 {
		t.Errorf("reader looked up %d times, want once", calls)
	}
}

func TestStatusRefresh_SkipsRecordsThatNeedNoRefresh(t *testing.T) {
	setupEnv(t)
	writePub(t, "o", "r", 1, state.StateReady)   // already final
	writePub(t, "o", "r", 2, state.StateRunning) // still being published locally
	writeProcessing(t, 3, "")                    // no publication id to ask about
	fake := &fakeAnalysis{}
	lookups := 0
	statusAnalysisReader = func() (analysisReader, bool) { lookups++; return fake, true }

	statusJSON(t)
	if len(fake.asked) != 0 || lookups != 0 {
		t.Errorf("asked %v after %d lookups, want no requests and no credential lookup", fake.asked, lookups)
	}
}

func TestStatusRefresh_AtMostFiveRecords(t *testing.T) {
	setupEnv(t)
	answers := map[string]*publish.Analysis{}
	for n := 1; n <= 8; n++ {
		id := fmt.Sprintf("pub-%d", n)
		writeProcessing(t, n, id)
		answers[id] = analysis(publish.AnalysisSucceeded, nil)
	}
	fake := &fakeAnalysis{answers: answers}
	useAnalysis(t, fake)

	pubs, _ := statusJSON(t)["publications"].([]any)
	if len(pubs) != 8 {
		t.Fatalf("publications = %d, want all 8 reported", len(pubs))
	}
	if len(fake.asked) != refreshLimit {
		t.Errorf("asked %d publications, want %d", len(fake.asked), refreshLimit)
	}
	ready := 0
	for _, p := range pubs {
		if p.(map[string]any)["state"] == "ready" {
			ready++
		}
	}
	if ready != refreshLimit {
		t.Errorf("%d records refreshed to ready, want %d", ready, refreshLimit)
	}
}

func TestStatusRefresh_OnlyTheFilteredPRIsAsked(t *testing.T) {
	setupEnv(t)
	writeProcessing(t, 1, "pub-1")
	writeProcessing(t, 2, "pub-2")
	fake := &fakeAnalysis{answers: map[string]*publish.Analysis{"pub-2": analysis(publish.AnalysisSucceeded, nil)}}
	useAnalysis(t, fake)

	statusJSON(t, "--pr", "https://github.com/o/r/pull/2")
	if len(fake.asked) != 1 || fake.asked[0] != "pub-2" {
		t.Errorf("asked %v, want only pub-2", fake.asked)
	}
}
