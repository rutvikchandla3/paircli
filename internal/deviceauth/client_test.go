package deviceauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeData writes a success envelope with request_id.
func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "request_id": "req-1"})
}

// writeErr writes an error envelope with request_id.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":      map[string]string{"code": code, "message": msg},
		"request_id": "req-1",
	})
}

// recordingWait captures the durations a client would have slept.
func recordingWait(sink *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		*sink = append(*sink, d)
		return nil
	}
}

func testClient(baseURL string) *Client {
	c := NewClient(baseURL)
	c.http = http.DefaultClient
	return c
}

// TestClient_RequestCode checks the path, the client_name body and the decode.
func TestClient_RequestCode(t *testing.T) {
	var gotPath, gotContentType string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("content-type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeData(w, http.StatusCreated, map[string]any{
			"device_code":               "dc-abc",
			"user_code":                 "WDJP-7Q2K",
			"verification_uri":          "http://x/app/device",
			"verification_uri_complete": "http://x/app/device?user_code=WDJP-7Q2K",
			"interval":                  5,
			"expires_in":                600,
		})
	}))
	defer srv.Close()

	// A trailing slash must not double up in the path.
	dc, err := testClient(srv.URL+"/").RequestCode(context.Background(), "paircli 0.2.0-dev")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	if gotPath != codePath {
		t.Errorf("path = %q, want %q", gotPath, codePath)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotBody["client_name"] != "paircli 0.2.0-dev" {
		t.Errorf("client_name = %q", gotBody["client_name"])
	}
	if dc.DeviceCode != "dc-abc" || dc.UserCode != "WDJP-7Q2K" || dc.Interval != 5 || dc.ExpiresIn != 600 {
		t.Errorf("decoded device code = %+v", dc)
	}
	if !strings.Contains(dc.VerificationURIComplete, "user_code=WDJP-7Q2K") {
		t.Errorf("verification_uri_complete = %q", dc.VerificationURIComplete)
	}
}

// TestClient_RequestCode_Error covers the error envelope on the code endpoint.
func TestClient_RequestCode_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", "bad client_name")
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).RequestCode(context.Background(), "paircli")
	if err == nil || !strings.Contains(err.Error(), "bad client_name") {
		t.Fatalf("err = %v, want it to name the envelope message", err)
	}
}

// TestPollToken_PendingThenSuccess walks pending, pending, success and checks
// the poll slept one interval between attempts.
func TestPollToken_PendingThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1, 2:
			writeErr(w, http.StatusBadRequest, "AUTHORIZATION_PENDING", "pending")
		default:
			writeData(w, http.StatusOK, map[string]any{
				"access_token": "tok-1", "token_type": "bearer", "expires_in": 7776000,
			})
		}
	}))
	defer srv.Close()

	var waits []time.Duration
	c := testClient(srv.URL)
	c.SetWait(recordingWait(&waits))

	tok, err := c.PollToken(context.Background(), DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 600})
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if tok.AccessToken != "tok-1" || tok.TokenType != "bearer" || tok.ExpiresIn != 7776000 {
		t.Errorf("token = %+v", tok)
	}
	if len(waits) != 2 || waits[0] != 5*time.Second || waits[1] != 5*time.Second {
		t.Errorf("waits = %v, want two 5s intervals", waits)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("token requests = %d, want 3", got)
	}
}

// TestPollToken_SlowDownBumpsInterval checks SLOW_DOWN adds 5s to the interval.
func TestPollToken_SlowDownBumpsInterval(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			writeErr(w, http.StatusBadRequest, "SLOW_DOWN", "slow down")
			return
		}
		writeData(w, http.StatusOK, map[string]any{"access_token": "tok", "token_type": "bearer", "expires_in": 10})
	}))
	defer srv.Close()

	var waits []time.Duration
	c := testClient(srv.URL)
	c.SetWait(recordingWait(&waits))

	if _, err := c.PollToken(context.Background(), DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 600}); err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if len(waits) != 1 || waits[0] != 10*time.Second {
		t.Fatalf("waits = %v, want [10s] (5s interval + 5s slow-down)", waits)
	}
}

// TestPollToken_TypedFailures checks ACCESS_DENIED and EXPIRED_TOKEN come back
// as *PollError with the right Kind.
func TestPollToken_TypedFailures(t *testing.T) {
	cases := []struct {
		code string
		kind PollKind
	}{
		{"ACCESS_DENIED", PollDenied},
		{"EXPIRED_TOKEN", PollExpired},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeErr(w, http.StatusBadRequest, tc.code, "no")
			}))
			defer srv.Close()

			c := testClient(srv.URL)
			c.SetWait(func(context.Context, time.Duration) error { return nil })

			_, err := c.PollToken(context.Background(), DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 600})
			var pe *PollError
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v, want *PollError", err)
			}
			if pe.Kind != tc.kind || pe.Code != tc.code {
				t.Errorf("PollError = %+v, want kind %v code %s", pe, tc.kind, tc.code)
			}
		})
	}
}

// TestPollToken_Retries5xx checks a 5xx is retried on the shared backoff.
func TestPollToken_Retries5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := atomic.AddInt32(&calls, 1); n <= 2 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		writeData(w, http.StatusOK, map[string]any{"access_token": "tok", "token_type": "bearer", "expires_in": 10})
	}))
	defer srv.Close()

	var waits []time.Duration
	c := testClient(srv.URL)
	c.SetWait(recordingWait(&waits))

	if _, err := c.PollToken(context.Background(), DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 600}); err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if len(waits) != 2 || waits[0] != backoff[0] || waits[1] != backoff[1] {
		t.Fatalf("waits = %v, want the [1s,2s] backoff", waits)
	}
}

// TestPollToken_UnknownCodeIsFatal checks an unlisted error.code is a plain
// non-retryable failure, not a poll signal.
func TestPollToken_UnknownCodeIsFatal(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeErr(w, http.StatusBadRequest, "SOMETHING_ELSE", "mystery")
	}))
	defer srv.Close()

	var waits []time.Duration
	c := testClient(srv.URL)
	c.SetWait(recordingWait(&waits))

	_, err := c.PollToken(context.Background(), DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 600})
	if err == nil || !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("err = %v, want a fatal error naming the code", err)
	}
	var pe *PollError
	if errors.As(err, &pe) {
		t.Errorf("unknown code must not be a *PollError: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("requests = %d, want 1 (no retry)", got)
	}
	if len(waits) != 0 {
		t.Errorf("waits = %v, want none", waits)
	}
}

// TestPollToken_Deadline checks the poll gives up once expires_in elapses.
func TestPollToken_Deadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, "AUTHORIZATION_PENDING", "pending")
	}))
	defer srv.Close()

	// Real time: one interval of 1.1s outlives the 1s deadline.
	c := testClient(srv.URL)
	c.SetWait(func(ctx context.Context, d time.Duration) error {
		return contextSleep(ctx, 1100*time.Millisecond)
	})

	_, err := c.PollToken(context.Background(), DeviceCode{DeviceCode: "dc", Interval: 1, ExpiresIn: 1})
	var pe *PollError
	if !errors.As(err, &pe) || pe.Kind != PollExpired {
		t.Fatalf("err = %v, want *PollError{PollExpired}", err)
	}
}

// TestPollToken_ContextCancel checks a cancelled context unblocks the poll.
func TestPollToken_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, "AUTHORIZATION_PENDING", "pending")
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := testClient(srv.URL)
	c.SetWait(func(_ context.Context, _ time.Duration) error {
		cancel()
		return context.Canceled
	})

	if _, err := c.PollToken(ctx, DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 600}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestMe checks the bearer header and the decoded identity.
func TestMe(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotPath = r.URL.Path
		writeData(w, http.StatusOK, map[string]any{"user": map[string]string{"display_name": "Ada", "email": "ada@example.com"}})
	}))
	defer srv.Close()

	u, err := testClient(srv.URL).Me(context.Background(), "tok-9")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if gotPath != mePath || gotAuth != "Bearer tok-9" {
		t.Errorf("path/auth = %q/%q", gotPath, gotAuth)
	}
	if u.DisplayName() != "Ada" {
		t.Errorf("DisplayName = %q", u.DisplayName())
	}
}

// TestUserDisplayName covers the fallback order.
func TestUserDisplayName(t *testing.T) {
	if got := (User{}).DisplayName(); got != "" {
		t.Errorf("empty user = %q", got)
	}
	if got := (User{Email: "ada@example.com"}).DisplayName(); got != "ada@example.com" {
		t.Errorf("email fallback = %q", got)
	}
	if got := (User{Display: "Ada", Email: "ada@x"}).DisplayName(); got != "Ada" {
		t.Errorf("display_name wins = %q", got)
	}
}
