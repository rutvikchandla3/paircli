package publish

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func journalFixture(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// clock pins the journal's clock to *now.
func clock(t *testing.T, now *int64) {
	t.Helper()
	old := nowNanos
	nowNanos = func() int64 { return *now }
	t.Cleanup(func() { nowNanos = old })
}

func mustRevision(t *testing.T, server, head, sum string, size int64) int64 {
	t.Helper()
	r, err := Revision(server, head, sum, size)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRevisionReplaysAndAdvancesAcrossHeadsAndClockRollback(t *testing.T) {
	journalFixture(t)
	now := int64(100)
	clock(t, &now)
	const server = "https://pair.example"

	first := mustRevision(t, server, testHead, "aa", 8)
	if first != 100 {
		t.Fatalf("first = %d, want the clock", first)
	}
	if again := mustRevision(t, server, testHead, "aa", 8); again != first {
		t.Errorf("a replay got %d, want %d", again, first)
	}

	now = 90 // the clock moved back
	if next := mustRevision(t, server, "b"+testHead[1:], "aa", 8); next != 101 {
		t.Errorf("next head = %d, want latest+1 = 101", next)
	}
	if changed := mustRevision(t, server, testHead, "bb", 8); changed != 102 {
		t.Errorf("changed content = %d, want 102", changed)
	}
	if other := mustRevision(t, "https://other.example", testHead, "aa", 8); other != 90 {
		t.Errorf("another server = %d, want its own clock-based 90", other)
	}
	if first2 := mustRevision(t, server, testHead, "aa", 8); first2 != first {
		t.Errorf("first bundle's revision drifted to %d", first2)
	}
}

func TestRevisionSizeIsPartOfTheKey(t *testing.T) {
	journalFixture(t)
	now := int64(10)
	clock(t, &now)
	a := mustRevision(t, "s", testHead, "aa", 8)
	b := mustRevision(t, "s", testHead, "aa", 9)
	if a == b {
		t.Errorf("different sizes share revision %d", a)
	}
}

func TestRevisionConcurrentCallersConvergeAndStayUnique(t *testing.T) {
	journalFixture(t)
	now := int64(1000)
	clock(t, &now)

	same := make([]int64, 16)
	distinct := make([]int64, 8)
	var wg sync.WaitGroup
	for i := range same {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			same[i] = mustRevision(t, "s", testHead, "same", 8)
		}(i)
	}
	for i := range distinct {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			distinct[i] = mustRevision(t, "s", testHead, string(rune('a'+i)), 8)
		}(i)
	}
	wg.Wait()

	for _, r := range same {
		if r != same[0] {
			t.Fatalf("the same bundle got revisions %d and %d", same[0], r)
		}
	}
	seen := map[int64]bool{same[0]: true}
	for _, r := range distinct {
		if seen[r] {
			t.Fatalf("revision %d was handed out twice", r)
		}
		seen[r] = true
	}
}

func TestRevisionRecoversFromACorruptJournal(t *testing.T) {
	journalFixture(t)
	now := int64(500)
	clock(t, &now)
	if err := os.MkdirAll(filepath.Dir(journalPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustRevision(t, "s", testHead, "aa", 8); got != 500 {
		t.Errorf("revision = %d, want the clock", got)
	}
	if got := mustRevision(t, "s", testHead, "aa", 8); got != 500 {
		t.Errorf("replay after recovery = %d", got)
	}
}

func TestRevisionRejectsOutOfRangeValues(t *testing.T) {
	journalFixture(t)
	now := int64(-5)
	clock(t, &now)
	if _, err := Revision("s", testHead, "aa", 8); err == nil {
		t.Error("a non-positive revision was issued")
	}
}
