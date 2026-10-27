package state

import (
	"testing"
	"time"
)

func TestPendingMarkerRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("acme", "shop", 7)

	if HasPending(key) {
		t.Fatal("a marker exists before any was written")
	}
	if _, ok := TakePending(key); ok {
		t.Fatal("TakePending found a marker that was never written")
	}
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if err := MarkPending(key, PendingRequest{PRURL: "https://github.com/acme/shop/pull/7", RepoDir: "/r", RequestedAt: at}); err != nil {
		t.Fatal(err)
	}
	if !HasPending(key) {
		t.Fatal("marker not visible")
	}
	got, ok := TakePending(key)
	if !ok || got.PRURL != "https://github.com/acme/shop/pull/7" || got.RepoDir != "/r" || !got.RequestedAt.Equal(at) {
		t.Errorf("TakePending = %+v, %v", got, ok)
	}
	if HasPending(key) {
		t.Error("TakePending left the marker behind")
	}
}

func TestPendingMarkersArePerPR(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := MarkPending(Key("a", "b", 1), PendingRequest{}); err != nil {
		t.Fatal(err)
	}
	if HasPending(Key("a", "b", 2)) {
		t.Error("a marker leaked to another PR")
	}
}
