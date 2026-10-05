package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/deviceauth"
)

// fakeBackend serves the three device-auth endpoints. tokenCodes is the
// sequence of envelope error codes the token endpoint returns; "" means a
// successful token.
type fakeBackend struct {
	t              *testing.T
	tokenCodes     []string
	server         *httptest.Server
	mu             sync.Mutex
	tokenCalls     int
	deviceCodeBody map[string]string
	meCalls        int
	meStatus       int
	user           map[string]any
}

func newFakeBackend(t *testing.T, tokenCodes []string, user map[string]any) *fakeBackend {
	t.Helper()
	be := &fakeBackend{t: t, tokenCodes: tokenCodes, user: user, meStatus: http.StatusOK}
	be.server = httptest.NewServer(http.HandlerFunc(be.handle))
	return be
}

func (be *fakeBackend) Close()      { be.server.Close() }
func (be *fakeBackend) URL() string { return be.server.URL }

func (be *fakeBackend) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json")
	switch r.URL.Path {
	case "/api/v1/auth/device/code":
		_ = json.NewDecoder(r.Body).Decode(&be.deviceCodeBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"device_code":               "dc-1",
				"user_code":                 "WDJP-7Q2K",
				"verification_uri":          be.server.URL + "/app/device",
				"verification_uri_complete": be.server.URL + "/app/device?user_code=WDJP-7Q2K",
				"interval":                  5,
				"expires_in":                600,
			},
			"request_id": "req-1",
		})
	case "/api/v1/auth/device/token":
		be.mu.Lock()
		be.tokenCalls++
		code := ""
		if len(be.tokenCodes) > 0 {
			code = be.tokenCodes[0]
			if len(be.tokenCodes) > 1 {
				be.tokenCodes = be.tokenCodes[1:]
			}
		}
		be.mu.Unlock()

		if code != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":      map[string]string{"code": code, "message": code},
				"request_id": "req-1",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       map[string]any{"access_token": "tok-1", "token_type": "bearer", "expires_in": 7776000},
			"request_id": "req-1",
		})
	case "/api/v1/auth/me":
		be.mu.Lock()
		be.meCalls++
		status, user := be.meStatus, be.user
		be.mu.Unlock()
		w.WriteHeader(status)
		if status == http.StatusOK {
			// Real backend wraps the user: data = {user, account, membership}.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": user}, "request_id": "req-1"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "UNAUTHORIZED", "message": "no"}, "request_id": "req-1",
		})
	default:
		http.NotFound(w, r)
	}
}

// stubLogin replaces the login package's two seams and restores them after the
// test. opened and waits collect what the flow would have done.
func stubLogin(t *testing.T, opened *[]string, waits *[]time.Duration) {
	t.Helper()
	prevClient, prevBrowser := loginNewClient, loginOpenBrowser
	loginNewClient = func(baseURL string) *deviceauth.Client {
		c := deviceauth.NewClient(baseURL)
		c.SetWait(func(_ context.Context, d time.Duration) error {
			if waits != nil {
				*waits = append(*waits, d)
			}
			return nil
		})
		return c
	}
	loginOpenBrowser = func(u string) error {
		if opened != nil {
			*opened = append(*opened, u)
		}
		return nil
	}
	t.Cleanup(func() { loginNewClient, loginOpenBrowser = prevClient, prevBrowser })
}

// useTempAuthFile points the store at a temp file and clears the env override.
func useTempAuthFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	t.Setenv(deviceauth.EnvFile, path)
	t.Setenv(deviceauth.EnvToken, "")
	return path
}

// TestLoginRun_FullFlow walks the whole flow against the fake backend.
func TestLoginRun_FullFlow(t *testing.T) {
	path := useTempAuthFile(t)
	be := newFakeBackend(t, []string{"AUTHORIZATION_PENDING", ""}, map[string]any{"name": "Ada Lovelace"})
	defer be.Close()

	var opened []string
	var waits []time.Duration
	stubLogin(t, &opened, &waits)

	var out, progress bytes.Buffer
	if err := loginRun([]string{"--server", be.URL()}, &out, &progress); err != nil {
		t.Fatalf("loginRun: %v", err)
	}

	if !strings.Contains(out.String(), "WDJP-7Q2K") {
		t.Errorf("stdout missing the user code:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Logged in as Ada Lovelace") {
		t.Errorf("stdout missing the identity:\n%s", out.String())
	}
	if strings.Contains(out.String(), "tok-1") {
		t.Errorf("stdout leaked the token:\n%s", out.String())
	}

	if len(waits) != 1 || waits[0] != 5*time.Second {
		t.Errorf("waits = %v, want one 5s interval", waits)
	}
	want := be.URL() + "/app/device?user_code=WDJP-7Q2K"
	if len(opened) != 1 || opened[0] != want {
		t.Errorf("opened = %v, want [%s]", opened, want)
	}

	stored, err := deviceauth.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "tok-1" || stored.TokenType != "bearer" || stored.User != "Ada Lovelace" {
		t.Errorf("stored = %+v", stored)
	}
	if stored.ServerURL != be.URL() {
		t.Errorf("stored server = %q, want %q", stored.ServerURL, be.URL())
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file perm = %o, want 600", perm)
	}
	if be.deviceCodeBody["client_name"] != "paircli "+version {
		t.Errorf("client_name = %q, want paircli <version>", be.deviceCodeBody["client_name"])
	}
}

// TestLoginRun_JSON checks --json keeps stdout valid JSON and moves progress to
// stderr.
func TestLoginRun_JSON(t *testing.T) {
	useTempAuthFile(t)
	be := newFakeBackend(t, []string{""}, map[string]any{"email": "ada@example.com"})
	defer be.Close()
	stubLogin(t, nil, nil)

	var out, progress bytes.Buffer
	if err := loginRun([]string{"--server", be.URL(), "--json"}, &out, &progress); err != nil {
		t.Fatalf("loginRun: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if payload["user_code"] != "WDJP-7Q2K" {
		t.Errorf("payload = %v", payload)
	}
	if strings.Contains(out.String(), "Logged in") {
		t.Errorf("human text leaked onto stdout:\n%s", out.String())
	}
	if !strings.Contains(progress.String(), "Logged in as ada@example.com") {
		t.Errorf("progress missing the identity:\n%s", progress.String())
	}
}

// TestLoginRun_NoBrowser checks --no-browser skips the browser.
func TestLoginRun_NoBrowser(t *testing.T) {
	useTempAuthFile(t)
	be := newFakeBackend(t, []string{""}, map[string]any{"name": "Ada"})
	defer be.Close()

	var opened []string
	stubLogin(t, &opened, nil)

	var out, progress bytes.Buffer
	if err := loginRun([]string{"--server", be.URL(), "--no-browser"}, &out, &progress); err != nil {
		t.Fatalf("loginRun: %v", err)
	}
	if len(opened) != 0 {
		t.Errorf("browser opened despite --no-browser: %v", opened)
	}
}

// TestLoginRun_BrowserFailureIsWarned checks a failed browser open is a warning.
func TestLoginRun_BrowserFailureIsWarned(t *testing.T) {
	useTempAuthFile(t)
	be := newFakeBackend(t, []string{""}, map[string]any{"name": "Ada"})
	defer be.Close()

	stubLogin(t, nil, nil)
	prev := loginOpenBrowser
	loginOpenBrowser = func(string) error { return context.DeadlineExceeded }
	t.Cleanup(func() { loginOpenBrowser = prev })

	var out, progress bytes.Buffer
	if err := loginRun([]string{"--server", be.URL()}, &out, &progress); err != nil {
		t.Fatalf("loginRun: %v", err)
	}
	if !strings.Contains(progress.String(), "could not open the browser") {
		t.Errorf("progress = %q, want a browser warning", progress.String())
	}
	if !strings.Contains(out.String(), "Logged in") {
		t.Errorf("login should still succeed:\n%s", out.String())
	}
}

// TestLoginRun_MeFailureIsNonFatal checks /auth/me failing does not fail login.
func TestLoginRun_MeFailureIsNonFatal(t *testing.T) {
	path := useTempAuthFile(t)
	be := newFakeBackend(t, []string{""}, nil)
	be.meStatus = http.StatusInternalServerError
	defer be.Close()
	stubLogin(t, nil, nil)

	var out, progress bytes.Buffer
	if err := loginRun([]string{"--server", be.URL()}, &out, &progress); err != nil {
		t.Fatalf("loginRun: %v", err)
	}
	if !strings.Contains(out.String(), "Logged in") {
		t.Errorf("stdout = %q", out.String())
	}
	stored, err := deviceauth.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "tok-1" {
		t.Errorf("stored = %+v", stored)
	}
}

// TestLoginRun_PollFailures maps the typed poll failures to clear messages.
func TestLoginRun_PollFailures(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"ACCESS_DENIED", "denied by user"},
		{"EXPIRED_TOKEN", "code expired"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			useTempAuthFile(t)
			be := newFakeBackend(t, []string{tc.code}, nil)
			defer be.Close()
			stubLogin(t, nil, nil)

			var out, progress bytes.Buffer
			err := loginRun([]string{"--server", be.URL()}, &out, &progress)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestResolveServer checks the --server > env > default precedence.
func TestResolveServer(t *testing.T) {
	t.Setenv(serverURLEnv, "https://env.example/")
	if got := resolveServer("https://flag.example/"); got != "https://flag.example" {
		t.Errorf("flag wins: %q", got)
	}
	if got := resolveServer(""); got != "https://env.example" {
		t.Errorf("env fallback: %q", got)
	}
	t.Setenv(serverURLEnv, "")
	if got := resolveServer(""); got != defaultServerURL {
		t.Errorf("default: %q", got)
	}
}

// TestParseLoginArgs checks flag parsing and the sibling-style parse errors.
func TestParseLoginArgs(t *testing.T) {
	a, err := parseLoginArgs([]string{"--server", "http://x", "--no-browser", "--json"})
	if err != nil {
		t.Fatalf("parseLoginArgs: %v", err)
	}
	if a.Server != "http://x" || !a.NoBrowser || !a.JSON || a.Timeout != 15*time.Minute {
		t.Errorf("args = %+v", a)
	}

	if _, err := parseLoginArgs([]string{"--nope"}); err == nil {
		t.Error("unknown flag should error")
	}
	if _, err := parseLoginArgs([]string{"extra"}); err == nil {
		t.Error("a positional argument should error")
	}
}

// TestLogoutRun checks removal, idempotency, --json and the env-token note.
func TestLogoutRun(t *testing.T) {
	path := useTempAuthFile(t)
	if err := deviceauth.Save(path, deviceauth.StoredAuth{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var out, progress bytes.Buffer
	if err := logoutRun(nil, &out, &progress); err != nil {
		t.Fatalf("logoutRun: %v", err)
	}
	if !strings.Contains(out.String(), "removed") {
		t.Errorf("stdout = %q", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token file still present: %v", err)
	}

	// Second run: idempotent.
	out.Reset()
	if err := logoutRun(nil, &out, &progress); err != nil {
		t.Fatalf("logoutRun again: %v", err)
	}
	if !strings.Contains(out.String(), "Already logged out") {
		t.Errorf("stdout = %q", out.String())
	}

	// --json payload, with the env override noted.
	t.Setenv(deviceauth.EnvToken, "env-token")
	out.Reset()
	progress.Reset()
	if err := logoutRun([]string{"--json"}, &out, &progress); err != nil {
		t.Fatalf("logoutRun --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if payload["env_token_set"] != true {
		t.Errorf("payload = %v", payload)
	}
	if !strings.Contains(progress.String(), deviceauth.EnvToken) {
		t.Errorf("progress should note the env token: %q", progress.String())
	}
}

// TestDoctorPair checks the three login states doctor reports, and that the
// token itself is never printed.
func TestDoctorPair(t *testing.T) {
	path := useTempAuthFile(t)
	t.Setenv(deviceauth.EnvToken, "")
	if got := doctorPair(); got != "not logged in" {
		t.Errorf("no file = %q, want not logged in", got)
	}

	expires := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if err := deviceauth.Save(path, deviceauth.StoredAuth{AccessToken: "super-secret", User: "Ada", ExpiresAt: expires}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := doctorPair()
	if !strings.Contains(got, "Ada") || !strings.Contains(got, "expires 2026-12-01") {
		t.Errorf("file state = %q", got)
	}
	if strings.Contains(got, "super-secret") {
		t.Errorf("doctor leaked the token: %q", got)
	}

	t.Setenv(deviceauth.EnvToken, "env-secret")
	got = doctorPair()
	if got != deviceauth.EnvToken+" set" {
		t.Errorf("env state = %q", got)
	}
	if strings.Contains(got, "env-secret") {
		t.Errorf("doctor leaked the env token: %q", got)
	}
}
