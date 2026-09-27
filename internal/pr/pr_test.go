package pr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// fakeRunner maps a joined gh argument list to canned bytes or an error, and
// fails the test on any unexpected call.
type fakeRunner struct {
	t         *testing.T
	responses map[string][]byte
	errs      map[string]error
	calls     []string
}

func newFakeRunner(t *testing.T) *fakeRunner {
	return &fakeRunner{t: t, responses: map[string][]byte{}, errs: map[string]error{}}
}

func (f *fakeRunner) withFile(key, path string) *fakeRunner {
	f.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatalf("reading testdata %s: %v", path, err)
	}
	f.responses[key] = data
	return f
}

func (f *fakeRunner) withErr(key string, err error) *fakeRunner {
	f.errs[key] = err
	return f
}

func (f *fakeRunner) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err, ok := f.errs[key]; ok {
		return nil, err
	}
	if out, ok := f.responses[key]; ok {
		return out, nil
	}
	f.t.Fatalf("fakeRunner: unexpected gh args: %q", key)
	return nil, nil
}

const testRepo = "acme/shop"

func prViewKey() string {
	return fmt.Sprintf("pr view 42 -R %s --json number,url,title,body,headRefName,baseRefName,createdAt,commits", testRepo)
}

func prDiffKey() string {
	return fmt.Sprintf("pr diff 42 -R %s", testRepo)
}

func commitAPIKey(sha string) string {
	return fmt.Sprintf("api repos/%s/commits/%s", testRepo, sha)
}

func TestFetch_Basic(t *testing.T) {
	r := newFakeRunner(t).
		withFile(prViewKey(), "testdata/pr_view.json").
		withFile(prDiffKey(), "testdata/pr_diff.txt")

	got, err := Fetch(r, testRepo, 42, false)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got.Repo != testRepo || got.Number != 42 || got.URL != "https://github.com/acme/shop/pull/42" {
		t.Errorf("Repo/Number/URL = %q/%d/%q", got.Repo, got.Number, got.URL)
	}
	if got.Title != "Add checkout flow" || got.Body != "This adds the checkout flow." {
		t.Errorf("Title/Body = %q/%q", got.Title, got.Body)
	}
	if got.HeadRef != "feature/checkout" || got.BaseRef != "main" {
		t.Errorf("HeadRef/BaseRef = %q/%q", got.HeadRef, got.BaseRef)
	}
	wantCreated := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	if !got.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, wantCreated)
	}

	if len(got.Commits) != 2 {
		t.Fatalf("len(Commits) = %d, want 2", len(got.Commits))
	}
	c0 := got.Commits[0]
	if c0.SHA != "aaaaaaa1111111111111111111111111111111" || c0.Message != "Add checkout handler" {
		t.Errorf("commit0 = %+v", c0)
	}
	c1 := got.Commits[1]
	wantMsg1 := "Fix checkout validation\n\nHandles empty cart edge case."
	if c1.SHA != "bbbbbbb2222222222222222222222222222222" || c1.Message != wantMsg1 {
		t.Errorf("commit1 = %+v, want message %q", c1, wantMsg1)
	}
	if c0.Files != nil || c1.Files != nil {
		t.Errorf("commit Files should be nil without withCommitPatches")
	}

	if len(got.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(got.Files))
	}
	f := got.Files[0]
	if f.Path != "checkout.go" || f.Status != model.StatusModified {
		t.Errorf("file = %+v", f)
	}
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) != 3 {
		t.Errorf("file hunks = %+v", f.Hunks)
	}
}

func TestFetch_CommitPatches(t *testing.T) {
	r := newFakeRunner(t).
		withFile(prViewKey(), "testdata/pr_view.json").
		withFile(prDiffKey(), "testdata/pr_diff.txt").
		withFile(commitAPIKey("aaaaaaa1111111111111111111111111111111"), "testdata/commit_aaaaaaa.json").
		withFile(commitAPIKey("bbbbbbb2222222222222222222222222222222"), "testdata/commit_bbbbbbb.json")

	got, err := Fetch(r, testRepo, 42, true)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Commits) != 2 {
		t.Fatalf("len(Commits) = %d, want 2", len(got.Commits))
	}

	c0 := got.Commits[0]
	if len(c0.Files) != 1 {
		t.Fatalf("commit0 Files = %+v, want 1", c0.Files)
	}
	if c0.Files[0].Path != "checkout.go" || c0.Files[0].Status != model.StatusAdded {
		t.Errorf("commit0 file = %+v", c0.Files[0])
	}
	if len(c0.Files[0].Hunks) != 1 || len(c0.Files[0].Hunks[0].Lines) != 2 {
		t.Errorf("commit0 file hunks = %+v", c0.Files[0].Hunks)
	}

	c1 := got.Commits[1]
	if len(c1.Files) != 3 {
		t.Fatalf("commit1 Files = %+v, want 3", c1.Files)
	}
	mod := c1.Files[0]
	if mod.Path != "validate.go" || mod.Status != model.StatusModified {
		t.Errorf("commit1 file0 = %+v", mod)
	}
	if len(mod.Hunks) != 1 || len(mod.Hunks[0].Lines) != 3 {
		t.Errorf("commit1 file0 hunks = %+v", mod.Hunks)
	}
	ren := c1.Files[1]
	if ren.Path != "newname.go" || ren.OldPath != "oldname.go" || ren.Status != model.StatusRenamed {
		t.Errorf("commit1 file1 = %+v", ren)
	}
	if len(ren.Hunks) != 1 || len(ren.Hunks[0].Lines) != 2 {
		t.Errorf("commit1 file1 hunks = %+v", ren.Hunks)
	}
	bin := c1.Files[2]
	if bin.Path != "binary.png" || !bin.Binary || len(bin.Hunks) != 0 {
		t.Errorf("commit1 file2 (no patch) = %+v, want Binary:true, no hunks", bin)
	}
}

func TestFetch_CommitLimit(t *testing.T) {
	const n = 101
	var sb strings.Builder
	sb.WriteString(`{"number":7,"url":"https://github.com/acme/shop/pull/7","title":"t","body":"","headRefName":"h","baseRefName":"main","createdAt":"2026-09-20T10:00:00Z","commits":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sha := fmt.Sprintf("%040d", i)
		sb.WriteString(fmt.Sprintf(`{"oid":%q,"messageHeadline":"c%d","messageBody":"","committedDate":"2026-09-20T10:00:00Z"}`, sha, i))
	}
	sb.WriteString(`]}`)

	r := newFakeRunner(t)
	r.responses[fmt.Sprintf("pr view 7 -R %s --json number,url,title,body,headRefName,baseRefName,createdAt,commits", testRepo)] = []byte(sb.String())
	r.responses[fmt.Sprintf("pr diff 7 -R %s", testRepo)] = []byte("")
	for i := 0; i < 100; i++ {
		sha := fmt.Sprintf("%040d", i)
		r.responses[commitAPIKey(sha)] = []byte(`{"files":[]}`)
	}
	// Deliberately no response registered for commit 100 (the 101st commit);
	// fakeRunner.Run calls t.Fatalf if Fetch tries to fetch it.

	got, err := Fetch(r, testRepo, 7, true)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Commits) != n {
		t.Fatalf("len(Commits) = %d, want %d", len(got.Commits), n)
	}

	apiCalls := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, "api repos/") {
			apiCalls++
		}
	}
	if apiCalls != 100 {
		t.Errorf("api calls = %d, want 100", apiCalls)
	}
	if got.Commits[100].Files != nil {
		t.Errorf("commit 100 (101st): Files = %+v, want nil (skipped, no error)", got.Commits[100].Files)
	}
}

func TestFetch_GHError(t *testing.T) {
	r := newFakeRunner(t).
		withErr(prViewKey(), errors.New("exit status 1: GraphQL: Could not resolve to a PullRequest with the number of 42. (repository.pullRequest)"))

	_, err := Fetch(r, testRepo, 42, false)
	if err == nil {
		t.Fatal("Fetch: err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "gh pr view") {
		t.Errorf("err = %q, want it to mention gh pr view", err)
	}
	if !strings.Contains(err.Error(), "Could not resolve to a PullRequest") {
		t.Errorf("err = %q, want it to include the underlying gh message", err)
	}
}

// writeGitFixture builds a minimal .git dir (HEAD + config with an origin
// remote) under dir, mirroring internal/gitinfo's test fixtures.
func writeGitFixture(t *testing.T, dir, remoteURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + remoteURL + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveArg_URL(t *testing.T) {
	dir := t.TempDir()
	writeGitFixture(t, dir, "git@github.com:someone/somewhere.git")

	repo, number, err := ResolveArg(dir, "https://github.com/acme/shop/pull/123")
	if err != nil {
		t.Fatalf("ResolveArg: %v", err)
	}
	if repo != "acme/shop" || number != 123 {
		t.Errorf("repo/number = %q/%d, want acme/shop/123", repo, number)
	}
}

func TestResolveArg_Number(t *testing.T) {
	dir := t.TempDir()
	writeGitFixture(t, dir, "git@github.com:acme/shop.git")

	repo, number, err := ResolveArg(dir, "42")
	if err != nil {
		t.Fatalf("ResolveArg: %v", err)
	}
	if repo != "acme/shop" || number != 42 {
		t.Errorf("repo/number = %q/%d, want acme/shop/42", repo, number)
	}
}

func TestResolveArg_NumberNoRemote(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := ResolveArg(dir, "42")
	if err == nil {
		t.Fatal("ResolveArg: err = nil, want non-nil (no remote configured)")
	}
}

func TestResolveArg_NotAPRArg(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ResolveArg(dir, "not-a-number-or-url")
	if err == nil {
		t.Fatal("ResolveArg: err = nil, want non-nil")
	}
}
