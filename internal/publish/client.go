// Package publish builds a PR's evidence bundle and publishes it to Pair: a
// deterministic zip (bundle.go), the revision journal that keeps replays
// idempotent (journal.go), and the client for the review publication API
// (reserve, S3 upload, finalize, analysis status).
//
// The client trusts nothing the server says: every response is size-capped
// and checked against what was sent, and the upload URL must be an S3 host
// over HTTPS before a single byte of the bundle goes to it.
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Timeouts and caps, as in the plugin client this replaces.
const (
	requestTimeout = 20 * time.Second
	uploadTimeout  = 5 * time.Minute
	maxResponse    = 1024 * 1024

	// MaxBundleBytes is the largest bundle Pair accepts.
	MaxBundleBytes = 100 * 1024 * 1024
)

// Metadata identifies one bundle to the publication API.
type Metadata struct {
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	PullNumber int    `json:"pull_number"`
	HeadSHA    string `json:"head_sha"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Revision   int64  `json:"revision"`
}

// Client talks to one Pair backend as one user.
type Client struct {
	Server string // validated origin, no trailing slash
	Token  string

	// HTTP carries API calls and Upload carries the S3 POST. Both must not
	// follow redirects; NewClient builds them that way.
	HTTP   *http.Client
	Upload *http.Client

	// Backoff is the wait before each retry of a transient finalize failure.
	Backoff []time.Duration
}

// NewClient validates server and token and returns a Client.
func NewClient(server, token string) (*Client, error) {
	origin, err := serverOrigin(server)
	if err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("Pair CLI credential is empty or contains invalid header characters")
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		Server:  origin,
		Token:   token,
		HTTP:    &http.Client{Timeout: requestTimeout, CheckRedirect: noRedirect},
		Upload:  &http.Client{Timeout: uploadTimeout, CheckRedirect: noRedirect},
		Backoff: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
	}, nil
}

// serverOrigin accepts an https origin, or http for localhost, and nothing
// else: no credentials, path, query or fragment.
func serverOrigin(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", errors.New("Pair backend URL is not a valid origin")
	}
	local := u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
	if u.Scheme != "https" && !local {
		return "", errors.New("Pair backend URL must use HTTPS outside localhost")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Pair backend URL must be an origin without credentials or a path")
	}
	return strings.TrimRight(server, "/"), nil
}

// Error is a failed API call. Its message never carries server-provided text,
// only a fixed description and, when the body names one, a well-formed code.
type Error struct {
	Status int
	Code   string
	cause  error
}

func (e *Error) Error() string {
	var msg string
	switch e.Status {
	case 0:
		msg = "Pair request failed"
		if e.cause != nil {
			msg += ": " + e.cause.Error()
		}
		return msg
	case http.StatusUnauthorized:
		msg = "Pair CLI login expired or was rejected; run paircli login and retry"
	case http.StatusForbidden:
		msg = "Pair refused evidence publication; check GitHub App authorization and repository access"
	case http.StatusNotFound:
		msg = "Pair review publication or repository is unavailable"
	case http.StatusConflict:
		msg = "Pair publication conflicts with server state; resolve the revision conflict"
	default:
		msg = fmt.Sprintf("Pair rejected evidence publication (HTTP %d)", e.Status)
	}
	if e.Code != "" {
		msg += " [" + e.Code + "]"
	}
	return msg
}

func (e *Error) Unwrap() error { return e.cause }

// CodeAuthorizationRequired is the code Pair answers a 403 with when the user
// has not authorized the Pair GitHub App for the repository. Only the user can
// fix it, on the PR's page on pair.sh.
const CodeAuthorizationRequired = "GITHUB_AUTHORIZATION_REQUIRED"

// AuthorizationRequired reports whether the user must authorize the Pair
// GitHub App before this request can succeed.
func (e *Error) AuthorizationRequired() bool {
	return e.Status == http.StatusForbidden && e.Code == CodeAuthorizationRequired
}

// Transient reports whether retrying the same request may succeed.
func (e *Error) Transient() bool {
	switch e.Status {
	case 0, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

// do sends one API request and returns the status and the response's "data"
// object. Statuses outside ok are an *Error.
func (c *Client) do(ctx context.Context, method, path string, body any, ok ...int) (int, json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, &Error{cause: sanitize(err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return 0, nil, &Error{cause: sanitize(err)}
	}
	if len(raw) > maxResponse {
		return 0, nil, errors.New("Pair publication response is too large")
	}
	for _, status := range ok {
		if resp.StatusCode == status {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 || envelope.Data[0] != '{' {
				return 0, nil, errors.New("Pair returned an invalid publication response")
			}
			return resp.StatusCode, envelope.Data, nil
		}
	}
	return 0, nil, &Error{Status: resp.StatusCode, Code: errorCode(raw)}
}

// sanitize drops the request URL from a transport error so a message names
// only what went wrong.
func sanitize(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// errorCode extracts a well-formed machine code from an error body, which
// carries it at the top level or inside an "error" envelope, or returns "".
func errorCode(raw []byte) string {
	var body struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	for _, code := range []string{body.Code, body.Error.Code} {
		if codePattern.MatchString(code) {
			return code
		}
	}
	return ""
}
