// Package deviceauth implements the CLI side of OAuth 2.0 Device Authorization
// Grant (RFC 8628) against the pair backend, plus the on-disk token store it
// returns. It is the CLI's first sanctioned on-disk secret: nothing else in
// paircli reads ~/.paircli/auth.json, and scan/snapshot/report code never
// touches it.
package deviceauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// codePath, tokenPath and mePath are the frozen backend endpoints.
	codePath  = "/api/v1/auth/device/code"
	tokenPath = "/api/v1/auth/device/token"
	mePath    = "/api/v1/auth/me"

	// requestTimeout bounds a single HTTP request.
	requestTimeout = 30 * time.Second

	// defaultInterval is the poll interval used when the server omits one.
	defaultInterval = 5 * time.Second

	// slowDownStep is how much SLOW_DOWN widens the poll interval
	// (RFC 8628 §3.5).
	slowDownStep = 5 * time.Second
)

// backoff is the retry schedule for transient failures: up to 3 retries (4
// attempts) with 1s, 2s and 4s between them, unless a retry-after header says
// otherwise. It is a local copy of internal/llm's rule — deviceauth must not
// import that package.
var backoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// retryableError marks one attempt as worth retrying, with an optional
// server-requested delay.
type retryableError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// pendingError signals that the user has not decided yet: the server answered
// AUTHORIZATION_PENDING or SLOW_DOWN. slowDown additionally asks the caller to
// widen its poll interval.
type pendingError struct{ slowDown bool }

func (e *pendingError) Error() string { return "deviceauth: authorization pending" }

// PollKind distinguishes the two terminal poll failures.
type PollKind int

const (
	// PollDenied is returned when the user refuses the request (ACCESS_DENIED).
	PollDenied PollKind = iota
	// PollExpired is returned when the code expired or was already consumed
	// (EXPIRED_TOKEN).
	PollExpired
)

// PollError is the typed terminal failure of PollToken.
type PollError struct {
	Kind    PollKind
	Code    string // the server's error.code
	Message string // the server's error.message, when it sent one
}

func (e *PollError) Error() string {
	switch e.Kind {
	case PollDenied:
		return "deviceauth: authorization denied"
	case PollExpired:
		return "deviceauth: device code expired"
	default:
		return "deviceauth: poll failed"
	}
}

// DeviceCode is the payload of POST /api/v1/auth/device/code.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// Token is the payload of a successful POST /api/v1/auth/device/token.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// User is the payload of GET /api/v1/auth/me. The frozen contract only says
// "user payload", so the likely identity fields are all decoded and
// DisplayName picks whichever the server filled in.
type User struct {
	Display string `json:"display_name"`
	Name    string `json:"name"`
	Email   string `json:"email"`
}

// DisplayName returns the best available human label, or "" when the payload
// carried none.
func (u User) DisplayName() string {
	for _, s := range []string{u.Display, u.Name, u.Email} {
		if s != "" {
			return s
		}
	}
	return ""
}

// envelope is the frozen response shape: success carries data, failure carries
// error, both carry a request_id.
type envelope struct {
	Data      json.RawMessage `json:"data"`
	Error     *apiError       `json:"error"`
	RequestID string          `json:"request_id"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Client talks to the pair backend's device-auth endpoints.
type Client struct {
	baseURL string
	http    *http.Client

	// wait sleeps for d honoring ctx, so tests never block for seconds.
	wait func(ctx context.Context, d time.Duration) error
}

// NewClient builds a client for baseURL, trimming a trailing slash so paths
// never double up.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
		wait:    contextSleep,
	}
}

// SetWait replaces the client's sleep function. It is the seam tests use to
// run the poll loop without real delays.
func (c *Client) SetWait(w func(ctx context.Context, d time.Duration) error) {
	if w != nil {
		c.wait = w
	}
}

// RequestCode starts a device authorization: POST {base}/api/v1/auth/device/code
// with {"client_name": clientName}.
func (c *Client) RequestCode(ctx context.Context, clientName string) (DeviceCode, error) {
	body, err := json.Marshal(map[string]string{"client_name": clientName})
	if err != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: encode request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+codePath, bytes.NewReader(body))
	if err != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: request device code: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: read response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return DeviceCode{}, fmt.Errorf("deviceauth: request device code: status %d: %s",
			resp.StatusCode, errorMessage(data))
	}

	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: decode response: %w", err)
	}
	if env.Error != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: request device code: %s: %s", env.Error.Code, env.Error.Message)
	}
	var dc DeviceCode
	if err := json.Unmarshal(env.Data, &dc); err != nil {
		return DeviceCode{}, fmt.Errorf("deviceauth: decode device code: %w", err)
	}
	return dc, nil
}

// PollToken polls POST {base}/api/v1/auth/device/token until the user decides.
// The first request goes out immediately; between requests it sleeps
// dc.Interval, widened by 5s on each SLOW_DOWN. Transient failures (network
// errors, 5xx) are retried on the shared backoff inside one poll. The poll
// gives up with a *PollError once dc.ExpiresIn elapses or the server denies or
// expires the code.
func (c *Client) PollToken(ctx context.Context, dc DeviceCode) (Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	var deadline time.Time
	if dc.ExpiresIn > 0 {
		deadline = time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	}

	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return Token{}, &PollError{Kind: PollExpired, Code: "EXPIRED_TOKEN"}
		}

		tok, err := c.pollOnce(ctx, dc.DeviceCode)
		if err == nil {
			return tok, nil
		}

		var pe *PollError
		if errors.As(err, &pe) {
			return Token{}, pe
		}

		var pending *pendingError
		if !errors.As(err, &pending) {
			return Token{}, err
		}
		if pending.slowDown {
			interval += slowDownStep
		}
		if werr := c.wait(ctx, interval); werr != nil {
			return Token{}, werr
		}
	}
}

// pollOnce performs one poll attempt, retrying transient failures on the
// shared backoff schedule.
func (c *Client) pollOnce(ctx context.Context, deviceCode string) (Token, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		tok, err := c.postToken(callCtx, deviceCode)
		cancel()
		if err == nil {
			return tok, nil
		}
		if ctx.Err() != nil {
			return Token{}, ctx.Err()
		}
		re, ok := err.(*retryableError)
		if !ok {
			return Token{}, err
		}
		lastErr = re.err
		if attempt >= len(backoff) {
			return Token{}, lastErr
		}
		d := backoff[attempt]
		if re.retryAfter > 0 {
			d = re.retryAfter
		}
		if werr := c.wait(ctx, d); werr != nil {
			return Token{}, werr
		}
	}
}

// postToken performs one HTTP attempt against the token endpoint and maps the
// envelope's error.code onto the poll vocabulary.
func (c *Client) postToken(ctx context.Context, deviceCode string) (Token, error) {
	body, err := json.Marshal(map[string]string{"device_code": deviceCode})
	if err != nil {
		return Token{}, fmt.Errorf("deviceauth: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+tokenPath, bytes.NewReader(body))
	if err != nil {
		return Token{}, fmt.Errorf("deviceauth: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Token{}, &retryableError{err: fmt.Errorf("deviceauth: request token: %w", err)}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Token{}, &retryableError{err: fmt.Errorf("deviceauth: read response: %w", err)}
	}

	if resp.StatusCode == http.StatusOK {
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return Token{}, fmt.Errorf("deviceauth: decode response: %w", err)
		}
		if env.Error != nil {
			return Token{}, mapEnvelopeError(env.Error)
		}
		var tok Token
		if err := json.Unmarshal(env.Data, &tok); err != nil {
			return Token{}, fmt.Errorf("deviceauth: decode token: %w", err)
		}
		return tok, nil
	}

	var env envelope
	_ = json.Unmarshal(data, &env)
	if env.Error != nil {
		if terminal := mapEnvelopeError(env.Error); terminal != nil {
			return Token{}, terminal
		}
	}

	statusErr := fmt.Errorf("deviceauth: token request: status %d: %s", resp.StatusCode, errorMessage(data))
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return Token{}, &retryableError{err: statusErr, retryAfter: parseRetryAfter(resp.Header.Get("retry-after"))}
	}
	return Token{}, statusErr
}

// mapEnvelopeError turns a frozen error.code into the poll vocabulary, or nil
// when the code is none of the three the poll loop understands.
func mapEnvelopeError(e *apiError) error {
	switch e.Code {
	case "AUTHORIZATION_PENDING":
		return &pendingError{}
	case "SLOW_DOWN":
		return &pendingError{slowDown: true}
	case "ACCESS_DENIED":
		return &PollError{Kind: PollDenied, Code: e.Code, Message: e.Message}
	case "EXPIRED_TOKEN":
		return &PollError{Kind: PollExpired, Code: e.Code, Message: e.Message}
	default:
		return nil
	}
}

// Me fetches GET {base}/api/v1/auth/me with the bearer token. Callers treat a
// failure as non-fatal: it only names who logged in.
func (c *Client) Me(ctx context.Context, accessToken string) (User, error) {
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.baseURL+mePath, nil)
	if err != nil {
		return User{}, fmt.Errorf("deviceauth: build request: %w", err)
	}
	req.Header.Set("authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("deviceauth: request me: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return User{}, fmt.Errorf("deviceauth: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("deviceauth: me: status %d: %s", resp.StatusCode, errorMessage(data))
	}

	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return User{}, fmt.Errorf("deviceauth: decode response: %w", err)
	}
	if env.Error != nil {
		return User{}, fmt.Errorf("deviceauth: me: %s: %s", env.Error.Code, env.Error.Message)
	}
	// /auth/me answers {user, account, membership}; only the user is wanted.
	var me struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(env.Data, &me); err != nil {
		return User{}, fmt.Errorf("deviceauth: decode user: %w", err)
	}
	return me.User, nil
}

// errorMessage prefers the envelope's error.message, falling back to the raw
// body.
func errorMessage(data []byte) string {
	var env envelope
	if err := json.Unmarshal(data, &env); err == nil && env.Error != nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return strings.TrimSpace(string(data))
}

// parseRetryAfter parses a retry-after header given in seconds; 0 means "no
// override".
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// contextSleep waits for d or until ctx is done, whichever comes first. It is
// the default wait; tests replace it so the poll loop never really sleeps.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
