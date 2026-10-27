package prcomment

import (
	"errors"
	"strings"
	"testing"
)

const (
	pairURL  = "https://pair.sh/interviewstreet/pair-poc/pull/5"
	userKey  = "api user --jq .login"
	listKey  = "api --paginate repos/interviewstreet/pair-poc/issues/5/comments?per_page=100"
	postKey  = "api --method POST repos/interviewstreet/pair-poc/issues/5/comments"
	patchFmt = "api --method PATCH repos/interviewstreet/pair-poc/issues/comments/"
)

// fakeGH serves canned gh output by argument list (ignoring the trailing
// "-f body=..." pair of a write) and records every call.
type fakeGH struct {
	t     *testing.T
	out   map[string]string
	errs  map[string]error
	calls []string
}

func newGH(t *testing.T, viewer, comments string) *fakeGH {
	return &fakeGH{t: t, out: map[string]string{userKey: viewer + "\n", listKey: comments}, errs: map[string]error{}}
}

func (f *fakeGH) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	for prefix, err := range f.errs {
		if strings.HasPrefix(key, prefix) {
			return nil, err
		}
	}
	if out, ok := f.out[key]; ok {
		return []byte(out), nil
	}
	if strings.HasPrefix(key, postKey+" -f body=") || strings.HasPrefix(key, patchFmt) {
		return []byte("{}"), nil
	}
	f.t.Fatalf("unexpected gh call: %s", key)
	return nil, nil
}

func (f *fakeGH) write() string { return f.calls[len(f.calls)-1] }

func TestUpsertCreatesTheMarkerComment(t *testing.T) {
	gh := newGH(t, "dhruvi", "[]")
	got, err := Upsert(gh, "interviewstreet", "pair-poc", 5, pairURL)
	if err != nil || got != Created {
		t.Fatalf("Upsert = %v, %v", got, err)
	}
	if !strings.HasPrefix(gh.write(), postKey+" -f body=") {
		t.Errorf("write = %q", gh.write())
	}
	if !strings.Contains(gh.write(), pairURL) || !strings.Contains(gh.write(), Marker) {
		t.Errorf("body missing the URL or marker: %q", gh.write())
	}
}

func TestUpsertUpdatesOnlyTheViewersOwnMarkerComment(t *testing.T) {
	comments := `[
	  {"id": 41, "body": "` + Marker + `", "user": {"login": "someone-else"}},
	  {"id": 42, "body": "old\n\n` + Marker + `", "user": {"login": "Dhruvi"}},
	  {"id": 43, "body": "unrelated", "user": {"login": "dhruvi"}}
	]`
	gh := newGH(t, "dhruvi", comments)
	got, err := Upsert(gh, "interviewstreet", "pair-poc", 5, pairURL)
	if err != nil || got != Updated {
		t.Fatalf("Upsert = %v, %v", got, err)
	}
	if !strings.HasPrefix(gh.write(), patchFmt+"42 -f body=") {
		t.Errorf("write = %q, want a PATCH of comment 42", gh.write())
	}
}

func TestUpsertNeverTouchesAnotherUsersComment(t *testing.T) {
	gh := newGH(t, "dhruvi", `[{"id": 41, "body": "`+Marker+`", "user": {"login": "someone-else"}}]`)
	got, err := Upsert(gh, "interviewstreet", "pair-poc", 5, pairURL)
	if err != nil || got != Created {
		t.Fatalf("Upsert = %v, %v, want a new comment of the viewer's own", got, err)
	}
	for _, call := range gh.calls {
		if strings.Contains(call, "PATCH") {
			t.Errorf("edited someone else's comment: %s", call)
		}
	}
}

func TestUpsertLeavesAnIdenticalCommentAlone(t *testing.T) {
	comments := `[{"id": 42, "body": "Review this PR in Pair: ` + pairURL + `\n\n` + Marker + `", "user": {"login": "dhruvi"}}]`
	gh := newGH(t, "dhruvi", comments)
	got, err := Upsert(gh, "interviewstreet", "pair-poc", 5, pairURL)
	if err != nil || got != Unchanged {
		t.Fatalf("Upsert = %v, %v", got, err)
	}
	if len(gh.calls) != 2 {
		t.Errorf("calls = %v, want only the reads", gh.calls)
	}
}

func TestUpsertFindsTheCommentOnALaterPage(t *testing.T) {
	pages := `[{"id": 1, "body": "first", "user": {"login": "a"}}]` + "\n" +
		`[{"id": 77, "body": "` + Marker + `", "user": {"login": "dhruvi"}}]`
	gh := newGH(t, "dhruvi", pages)
	got, err := Upsert(gh, "interviewstreet", "pair-poc", 5, pairURL)
	if err != nil || got != Updated || !strings.HasPrefix(gh.write(), patchFmt+"77 ") {
		t.Fatalf("Upsert = %v, %v, write %q", got, err, gh.write())
	}
}

func TestUpsertReportsGHFailures(t *testing.T) {
	gh := newGH(t, "dhruvi", "[]")
	gh.errs[userKey] = errors.New("gh: not logged in")
	if _, err := Upsert(gh, "o", "r", 5, pairURL); err == nil {
		t.Error("a failing gh user lookup was ignored")
	}
	if _, err := Upsert(newGH(t, "", "[]"), "interviewstreet", "pair-poc", 5, pairURL); err == nil {
		t.Error("an empty viewer login was accepted")
	}
	if _, err := Upsert(newGH(t, "dhruvi", "not json"), "interviewstreet", "pair-poc", 5, pairURL); err == nil {
		t.Error("an unreadable comment list was ignored")
	}
	if _, err := Upsert(nil, "o", "r", 5, pairURL); err == nil {
		t.Error("a nil runner was accepted")
	}
}
