package link

import (
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestWindowFor_WithCommits(t *testing.T) {
	cfg := config.Default()
	pr := testkit.PR("acme/shop", 1).Commit("sha1", "14:00").Commit("sha2", "16:00").Build()

	w := WindowFor(pr, cfg)

	earliest := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 9, 27, 16, 0, 0, 0, time.UTC)
	wantFrom := earliest.Add(-cfg.WindowBefore.Duration)
	wantTo := latest.Add(cfg.WindowAfter.Duration)

	if !w.From.Equal(wantFrom) {
		t.Errorf("From = %v, want %v", w.From, wantFrom)
	}
	if !w.To.Equal(wantTo) {
		t.Errorf("To = %v, want %v", w.To, wantTo)
	}
}

func TestWindowFor_NoCommits(t *testing.T) {
	cfg := config.Default()
	pr := testkit.PR("acme/shop", 2).Build()
	pr.CreatedAt = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

	w := WindowFor(pr, cfg)

	wantFrom := pr.CreatedAt.Add(-cfg.WindowBefore.Duration)
	if !w.From.Equal(wantFrom) {
		t.Errorf("From = %v, want %v", w.From, wantFrom)
	}
	now := time.Now()
	if w.To.Before(now.Add(-5*time.Second)) || w.To.After(now.Add(5*time.Second)) {
		t.Errorf("To = %v, want ~now (%v)", w.To, now)
	}
}
