package state

import (
	"os"
	"testing"
)

func TestLatest_ANewerClaimEndsTheOlderOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)

	if !IsLatest(key, "a") {
		t.Fatal("with no record a follower must carry on")
	}
	if err := ClaimLatest(key, Latest{Token: "a", PublicationID: "p1", HeadSHA: "h1"}, nil); err != nil {
		t.Fatal(err)
	}
	if !IsLatest(key, "a") {
		t.Error("the claimant is not the latest")
	}
	if err := ClaimLatest(key, Latest{Token: "b"}, nil); err != nil {
		t.Fatal(err)
	}
	if IsLatest(key, "a") || !IsLatest(key, "b") {
		t.Errorf("after b claimed: a latest = %v, b latest = %v", IsLatest(key, "a"), IsLatest(key, "b"))
	}
}

func TestLatest_IsPerPR(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ClaimLatest(Key("a", "b", 1), Latest{Token: "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if !IsLatest(Key("a", "b", 2), "y") {
		t.Error("a claim on one PR ended a follower of another")
	}
}

func TestLatest_WithLatestRunsOnlyForTheLatest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)
	ClaimLatest(key, Latest{Token: "a"}, nil)
	ClaimLatest(key, Latest{Token: "b"}, nil)

	ran := false
	if WithLatest(key, "a", func() { ran = true }) || ran {
		t.Error("an ended follower acted")
	}
	if !WithLatest(key, "b", func() { ran = true }) || !ran {
		t.Error("the latest follower did not act")
	}
}

func TestLatest_ClaimRunsItsFunctionWhileHeldAndAfterTheTokenChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)
	ClaimLatest(key, Latest{Token: "a"}, nil)

	var sawNew, guarded bool
	ClaimLatest(key, Latest{Token: "b"}, func() {
		sawNew = IsLatest(key, "b")
		_, err := TryLock(key + ".latest")
		guarded = err == ErrLocked
	})
	if !sawNew || !guarded {
		t.Errorf("token written first = %v, guard held = %v", sawNew, guarded)
	}
}

func TestLatest_NothingIsHeldAfterwards(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)
	ClaimLatest(key, Latest{Token: "a"}, nil)
	WithLatest(key, "a", func() {})
	lock, err := TryLock(key + ".latest")
	if err != nil {
		t.Fatalf("the guard stayed locked: %v", err)
	}
	lock.Release()
}

func TestLatest_UnreadableRecordCountsAsLatest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)
	if err := os.MkdirAll(Dir()+"/latest", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(latestPath(key), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsLatest(key, "a") {
		t.Error("a corrupt record silenced the follower for good")
	}
	if !IsLatest(key, "") {
		t.Error("an empty token must always count as latest")
	}
}

func TestLatest_GuardedRunsUnderTheGuardAndTouchesNoClaim(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)
	ClaimLatest(key, Latest{Token: "a"}, nil)
	before, err := os.ReadFile(latestPath(key))
	if err != nil {
		t.Fatal(err)
	}

	var guarded bool
	Guarded(key, func() {
		_, err := TryLock(key + ".latest")
		guarded = err == ErrLocked
	})
	if !guarded {
		t.Error("fn ran without the guard held")
	}
	after, _ := os.ReadFile(latestPath(key))
	if string(after) != string(before) || !IsLatest(key, "a") {
		t.Errorf("Guarded changed the claim: %s -> %s", before, after)
	}
	lock, err := TryLock(key + ".latest")
	if err != nil {
		t.Fatalf("the guard stayed locked: %v", err)
	}
	lock.Release()
}
