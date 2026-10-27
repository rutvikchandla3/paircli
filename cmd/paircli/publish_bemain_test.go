package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/publish"
	"github.com/rutvikchandla3/paircli/internal/state"
)

// beMain answers like pair-be's main branch (before the analysis job): the
// upload request is decoded strictly, a reservation follows ReservePublication
// (an identical replay returns the row, the same revision with other metadata
// or a revision at or below the last finalized one is a 409), finalize ignores
// its body and answers 200 with no analysis fields, and the analysis route does
// not exist, so it gets the router's NOT_FOUND envelope.
type beMain struct {
	pubs            map[int64]*bePub
	latestFinalized int64
	contentVersion  int
	finalizeBodies  []string
	reserveStatuses []int
}

type bePub struct {
	id        string
	meta      publish.Metadata
	finalized bool
}

func newBEMain() *beMain { return &beMain{pubs: map[int64]*bePub{}} }

func beReply(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func beFail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": "nope"}})
}

func (b *beMain) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/reviews/publications/upload-url":
		var m publish.Metadata
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			b.reserveStatuses = append(b.reserveStatuses, http.StatusBadRequest)
			beFail(w, http.StatusBadRequest, "VALIDATION_FAILED")
			return
		}
		status, pub := b.reserve(m)
		b.reserveStatuses = append(b.reserveStatuses, status)
		if pub == nil {
			beFail(w, status, "CONFLICT")
			return
		}
		resp := map[string]any{
			"publication_id": pub.id, "review_id": pubReviewID, "owner": m.Owner, "repo": m.Repo,
			"pull_number": m.PullNumber, "head_sha": m.HeadSHA, "revision": m.Revision, "sha256": m.SHA256,
			"size": m.Size, "state": "reserved", "upload": nil,
		}
		if pub.finalized {
			resp["state"] = "finalized"
		} else {
			digest, _ := hex.DecodeString(m.SHA256)
			resp["upload"] = map[string]any{
				"method": "POST", "url": "https://bucket.s3.us-east-1.amazonaws.com", "expires_at": "2026-10-11T00:15:00Z",
				"fields": map[string]string{
					"key":          fmt.Sprintf("v1/reviews/9/%d/%s/bundles/%s/%d/%s.zip", m.PullNumber, m.HeadSHA, pubUserID, m.Revision, m.SHA256),
					"Content-Type": "application/zip", "x-amz-checksum-algorithm": "SHA256",
					"x-amz-checksum-sha256": base64.StdEncoding.EncodeToString(digest),
				},
			}
		}
		beReply(w, status, resp)
	case strings.HasSuffix(r.URL.Path, "/finalize"):
		body, _ := io.ReadAll(r.Body) // the handler never reads it
		b.finalizeBodies = append(b.finalizeBodies, string(body))
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/reviews/publications/"), "/finalize")
		for rev, pub := range b.pubs {
			if pub.id != id {
				continue
			}
			replayed := pub.finalized
			if !replayed {
				pub.finalized = true
				b.contentVersion++
			}
			if rev > b.latestFinalized {
				b.latestFinalized = rev
			}
			beReply(w, http.StatusOK, map[string]any{
				"publication_id": pub.id, "review_id": pubReviewID, "head_sha": pub.meta.HeadSHA, "revision": rev,
				"sha256": pub.meta.SHA256, "size": pub.meta.Size, "state": "finalized",
				"finalized_at": "2026-10-11T00:00:00Z", "content_version": b.contentVersion, "replayed": replayed,
			})
			return
		}
		beFail(w, http.StatusNotFound, "NOT_FOUND")
	default:
		beFail(w, http.StatusNotFound, "NOT_FOUND") // NoRoute, including GET .../analysis
	}
}

func (b *beMain) reserve(m publish.Metadata) (int, *bePub) {
	if existing, ok := b.pubs[m.Revision]; ok {
		if existing.meta != m {
			return http.StatusConflict, nil
		}
		return http.StatusOK, existing
	}
	if m.Revision <= b.latestFinalized {
		return http.StatusConflict, nil
	}
	pub := &bePub{id: fmt.Sprintf("55555555-5555-4555-8555-%012d", len(b.pubs)+1), meta: m}
	b.pubs[m.Revision] = pub
	return http.StatusCreated, pub
}

func (a *pubAPI) sawAnalysisRequest() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.paths {
		if strings.HasSuffix(p, "/analysis") {
			return true
		}
	}
	return false
}

func newBEMainHarness(t *testing.T) (*pubHarness, *beMain) {
	h := newPubHarness(t)
	be := newBEMain()
	h.api.be = be
	return h, be
}

func TestPublishAgainstBackendMain(t *testing.T) {
	t.Run("a 200 finalize without analysis fields is a finished upload", func(t *testing.T) {
		h, be := newBEMainHarness(t)
		if err := h.run(); err != nil {
			t.Fatalf("publish: %v\n%s", err, h.stderr)
		}
		rec := h.record()
		if rec.State != state.StateUploaded || rec.AnalysisJobID != "" || rec.Error != "" {
			t.Errorf("record = %+v, want uploaded with no analysis job", rec)
		}
		if len(be.finalizeBodies) != 1 || strings.TrimSpace(be.finalizeBodies[0]) != `{"accepts_async":true}` {
			t.Errorf("finalize bodies = %q", be.finalizeBodies)
		}
		if h.api.sawAnalysisRequest() {
			t.Error("publish without --wait asked for the analysis")
		}
	})

	t.Run("--wait ends quickly with the upload when there is no analysis route", func(t *testing.T) {
		h, _ := newBEMainHarness(t)
		if err := h.run("--wait"); err != nil {
			t.Fatalf("publish --wait: %v\n%s", err, h.stderr)
		}
		rec := h.record()
		if rec.State != state.StateUploaded || rec.Error != "" {
			t.Errorf("record = %+v, want uploaded and no error", rec)
		}
		if len(h.sleeps) != 0 {
			t.Errorf("--wait polled %d times for an analysis that cannot exist", len(h.sleeps))
		}
	})

	t.Run("republishing the same head, then a new head, never conflicts", func(t *testing.T) {
		h, be := newBEMainHarness(t)
		for i := 0; i < 2; i++ {
			if err := h.run(); err != nil {
				t.Fatalf("publish #%d of the same head: %v\n%s", i+1, err, h.stderr)
			}
		}
		h.gh.setHead("ffffffffffffffffffffffffffffffffffffffff")
		if err := h.run(); err != nil {
			t.Fatalf("publish of a new head: %v\n%s", err, h.stderr)
		}
		for i, status := range be.reserveStatuses {
			if status != http.StatusCreated && status != http.StatusOK {
				t.Errorf("reservation %d got HTTP %d", i+1, status)
			}
		}
		if len(be.reserveStatuses) != 3 {
			t.Errorf("reservations = %v, want 3", be.reserveStatuses)
		}
		var last int64
		for rev := range be.pubs {
			if rev > last {
				last = rev
			}
		}
		if be.pubs[last].meta.HeadSHA != "ffffffffffffffffffffffffffffffffffffffff" {
			t.Errorf("the newest revision is for %s", be.pubs[last].meta.HeadSHA)
		}
	})
}
