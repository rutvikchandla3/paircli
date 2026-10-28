package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testHead      = "0123456789abcdef0123456789abcdef01234567"
	testToken     = "test-cli-token"
	publicationID = "11111111-1111-4111-8111-111111111111"
	reviewID      = "22222222-2222-4222-8222-222222222222"
	bucketUser    = "33333333-3333-4333-8333-333333333333"
	jobID         = "44444444-4444-4444-8444-444444444444"
	s3URL         = "https://bucket.s3.us-east-1.amazonaws.com"
)

var zipData = []byte("zip-data")

func testMeta() Metadata {
	sum := sha256.Sum256(zipData)
	return Metadata{Owner: "acme", Repo: "repo", PullNumber: 42, HeadSHA: testHead,
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(zipData)), Revision: 7}
}

// reserved is a reservation response for m, as a generic map so a test can
// corrupt one field.
func reserved(m Metadata, state string) map[string]any {
	p := map[string]any{
		"publication_id": publicationID, "review_id": reviewID, "owner": m.Owner, "repo": m.Repo,
		"pull_number": m.PullNumber, "head_sha": m.HeadSHA, "revision": m.Revision,
		"sha256": m.SHA256, "size": m.Size, "state": state, "upload": nil,
	}
	if state == StateReserved {
		digest, _ := hex.DecodeString(m.SHA256)
		p["upload"] = map[string]any{
			"method": "POST", "url": s3URL, "expires_at": "2026-10-12T00:00:00Z",
			"fields": map[string]string{
				"key":          fmt.Sprintf("v1/reviews/123/%d/%s/bundles/%s/%d/%s.zip", m.PullNumber, m.HeadSHA, bucketUser, m.Revision, m.SHA256),
				"Content-Type": "application/zip", "x-amz-checksum-algorithm": "SHA256",
				"x-amz-checksum-sha256": base64.StdEncoding.EncodeToString(digest), "policy": "signed-policy",
			},
		}
	}
	return p
}

func finalizedBody(m Metadata, async bool) map[string]any {
	f := map[string]any{
		"publication_id": publicationID, "review_id": reviewID, "head_sha": m.HeadSHA, "revision": m.Revision,
		"sha256": m.SHA256, "size": m.Size, "state": StateFinalized, "finalized_at": "2026-10-11T00:00:00Z",
		"content_version": 1, "replayed": false,
	}
	if async {
		f["analysis_job_id"], f["analysis_state"] = jobID, AnalysisQueued
	}
	return f
}

// call is one request the fake API saw.
type call struct {
	method, path, auth string
	body               []byte
}

type reply struct {
	status int
	body   string
}

func data(status int, v any) reply {
	raw, _ := json.Marshal(map[string]any{"data": v})
	return reply{status, string(raw)}
}

// fakeAPI serves scripted replies per "METHOD path" and records every call.
type fakeAPI struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []call
	replies map[string][]reply
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, replies: map[string][]reply{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.calls = append(f.calls, call{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
		queue := f.replies[key]
		var rep reply
		if len(queue) > 0 {
			rep, f.replies[key] = queue[0], queue[1:]
		}
		f.mu.Unlock()
		if rep.status == 0 {
			t.Errorf("unexpected call %s", key)
			rep = reply{http.StatusInternalServerError, "{}"}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		io.WriteString(w, rep.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) on(method, path string, replies ...reply) {
	f.replies[method+" "+path] = append(f.replies[method+" "+path], replies...)
}

func (f *fakeAPI) count(method, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.method == method && c.path == path {
			n++
		}
	}
	return n
}

const (
	reservePath  = "/api/v1/reviews/publications/upload-url"
	finalizePath = "/api/v1/reviews/publications/" + publicationID + "/finalize"
	analysisPath = "/api/v1/reviews/publications/" + publicationID + "/analysis"
)

// s3Request is one upload the fake S3 saw.
type s3Request struct {
	url           string
	header        http.Header
	body          []byte
	contentLength int64
}

type fakeS3 struct {
	mu       sync.Mutex
	requests []s3Request
	status   int
	err      error
}

func (f *fakeS3) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, s3Request{r.URL.String(), r.Header.Clone(), body, r.ContentLength})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusNoContent
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func newTestClient(t *testing.T, api *fakeAPI, s3 *fakeS3) *Client {
	t.Helper()
	c, err := NewClient(api.srv.URL, testToken)
	if err != nil {
		t.Fatal(err)
	}
	c.Upload = &http.Client{Transport: s3}
	c.Backoff = []time.Duration{0, 0, 0}
	return c
}

func writeBundleFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), testHead+".zip")
	if err := os.WriteFile(path, zipData, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewClientValidatesServerAndToken(t *testing.T) {
	for _, server := range []string{"https://pair.sh", "https://pair.sh/", "http://localhost:8080", "http://127.0.0.1:9"} {
		if _, err := NewClient(server, testToken); err != nil {
			t.Errorf("NewClient(%q): %v", server, err)
		}
	}
	for _, server := range []string{"http://pair.example", "https://user:pw@pair.example", "https://pair.example/api", "https://pair.example?x=1", "", "pair.example"} {
		if _, err := NewClient(server, testToken); err == nil {
			t.Errorf("NewClient(%q) accepted a bad origin", server)
		}
	}
	if _, err := NewClient("https://pair.sh", "tok\r\nInjected: x"); err == nil || strings.Contains(err.Error(), "Injected") {
		t.Errorf("header-injection token: err = %v", err)
	}
	if _, err := NewClient("https://pair.sh", ""); err == nil {
		t.Error("empty token accepted")
	}
}

func TestReserveUsesExactContractAndBearer(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := newFakeAPI(t)
			m := testMeta()
			api.on("POST", reservePath, data(status, reserved(m, StateReserved)))
			c := newTestClient(t, api, &fakeS3{})

			pub, err := c.Reserve(context.Background(), m)
			if err != nil {
				t.Fatal(err)
			}
			if pub.PublicationID != publicationID || pub.State != StateReserved || pub.Upload == nil {
				t.Errorf("publication = %+v", pub)
			}
			got := api.calls[0]
			if got.auth != "Bearer "+testToken {
				t.Errorf("Authorization = %q", got.auth)
			}
			var sent Metadata
			if err := json.Unmarshal(got.body, &sent); err != nil || sent != m {
				t.Errorf("request body = %s (%v), want %+v", got.body, err, m)
			}
		})
	}
}

func TestReserveRejectsMismatchedMetadata(t *testing.T) {
	zeroUUID := "00000000-0000-0000-0000-000000000000"
	cases := map[string]any{
		"head_sha": strings.Repeat("b", 40), "sha256": strings.Repeat("c", 64), "revision": 8, "size": 9,
		"pull_number": 43, "publication_id": "not-a-uuid", "review_id": zeroUUID,
	}
	for field, value := range cases {
		t.Run(field, func(t *testing.T) {
			api := newFakeAPI(t)
			m := testMeta()
			body := reserved(m, StateReserved)
			body[field] = value
			api.on("POST", reservePath, data(201, body))
			_, err := newTestClient(t, api, &fakeS3{}).Reserve(context.Background(), m)
			if err == nil || !strings.Contains(err.Error(), "mismatched") && !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("err = %v, want a mismatch or invalid error", err)
			}
		})
	}
}

func TestReserveRejectsWrongChecksumPolicyOrObjectKey(t *testing.T) {
	for field, value := range map[string]string{
		"Content-Type": "text/plain", "x-amz-checksum-sha256": "wrong",
		"x-amz-checksum-algorithm": "MD5", "key": "legacy/key.zip",
	} {
		t.Run(field, func(t *testing.T) {
			api := newFakeAPI(t)
			m := testMeta()
			body := reserved(m, StateReserved)
			body["upload"].(map[string]any)["fields"].(map[string]string)[field] = value
			api.on("POST", reservePath, data(201, body))
			_, err := newTestClient(t, api, &fakeS3{}).Reserve(context.Background(), m)
			if err == nil || !strings.Contains(err.Error(), "invalid publication upload") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestReserveRejectsUnexpectedStatesAndShapes(t *testing.T) {
	m := testMeta()
	finalizedWithUpload := reserved(m, StateReserved)
	finalizedWithUpload["state"] = StateFinalized
	getMethod := reserved(m, StateReserved)
	getMethod["upload"].(map[string]any)["method"] = "PUT"
	missingOwner := reserved(m, StateReserved)
	missingOwner["owner"] = " "
	stringRevision := reserved(m, StateReserved)
	stringRevision["revision"] = "7"

	for name, body := range map[string]any{
		"finalized but still carries an upload": finalizedWithUpload, "wrong method": getMethod,
		"blank owner": missingOwner, "revision as a string": stringRevision,
		"not an object": []int{1}, "empty object": map[string]any{"publication_id": "invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.on("POST", reservePath, data(201, body))
			if _, err := newTestClient(t, api, &fakeS3{}).Reserve(context.Background(), m); err == nil {
				t.Fatal("accepted a bad reservation")
			}
		})
	}
}

func TestAPIErrorsDoNotExposeProviderTextOrCredentials(t *testing.T) {
	for _, status := range []int{301, 400, 401, 403, 404, 409, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := newFakeAPI(t)
			api.on("POST", reservePath, reply{status, `{"error":{"code":"CONFLICT","message":"private provider detail ` + testToken + `"}}`})
			_, err := newTestClient(t, api, &fakeS3{}).Reserve(context.Background(), testMeta())
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), "private provider detail") || strings.Contains(err.Error(), testToken) {
				t.Errorf("error leaks server text or the token: %v", err)
			}
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Status != status {
				t.Errorf("err = %#v, want *Error with status %d", err, status)
			}
		})
	}
}

func TestInvalidOrOversizedBodiesFailClosed(t *testing.T) {
	for name, body := range map[string]string{
		"not json": "invalid JSON", "oversized": `{"data":{"x":"` + strings.Repeat("x", maxResponse) + `"}}`,
		"no data": `{}`, "data array": `{"data":[]}`, "data null": `{"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.on("POST", reservePath, reply{201, body})
			_, err := newTestClient(t, api, &fakeS3{}).Reserve(context.Background(), testMeta())
			if err == nil || (!strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "too large")) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestUploadBundleStreamsFileLastWithoutPairToken(t *testing.T) {
	s3 := &fakeS3{}
	c := newTestClient(t, newFakeAPI(t), s3)
	m := testMeta()
	signed := reserved(m, StateReserved)["upload"].(map[string]any)
	fields := signed["fields"].(map[string]string)

	err := c.UploadBundle(context.Background(), &SignedUpload{Method: "POST", URL: s3URL, Fields: fields}, writeBundleFile(t))
	if err != nil {
		t.Fatal(err)
	}
	req := s3.requests[0]
	if req.header.Get("Authorization") != "" {
		t.Error("the Pair token was sent to S3")
	}
	if req.contentLength != int64(len(req.body)) {
		t.Errorf("Content-Length %d != body %d", req.contentLength, len(req.body))
	}
	_, params, err := mime.ParseMediaType(req.header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(req.body), params["boundary"])
	var names []string
	var file []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(part)
		names = append(names, part.FormName())
		if part.FormName() == "file" {
			file = content
		} else if string(content) != fields[part.FormName()] {
			t.Errorf("field %s = %q, want %q", part.FormName(), content, fields[part.FormName()])
		}
	}
	if len(names) != len(fields)+1 || names[len(names)-1] != "file" {
		t.Errorf("parts = %v, want every field then file last", names)
	}
	if !bytes.Equal(file, zipData) {
		t.Errorf("file part = %q", file)
	}
}

func TestUploadBundleRejectsUntrustedTargets(t *testing.T) {
	for _, target := range []string{
		"https://evil.example/upload", "http://bucket.s3.us-east-1.amazonaws.com", "https://bucket.s3.us-east-1.amazonaws.com:8443",
		"https://user:pw@bucket.s3.us-east-1.amazonaws.com", "https://ec2.us-east-1.amazonaws.com", "https://s3.amazonaws.com.evil.example",
		"not a url", "",
	} {
		t.Run(target, func(t *testing.T) {
			s3 := &fakeS3{}
			c := newTestClient(t, newFakeAPI(t), s3)
			err := c.UploadBundle(context.Background(), &SignedUpload{Method: "POST", URL: target, Fields: map[string]string{}}, writeBundleFile(t))
			if err == nil || !strings.Contains(err.Error(), "non-S3") {
				t.Fatalf("err = %v, want non-S3 rejection", err)
			}
			if len(s3.requests) != 0 {
				t.Error("the bundle was sent to an untrusted host")
			}
		})
	}
	// Both S3 hostname styles are accepted.
	for _, target := range []string{"https://bucket.s3.us-east-1.amazonaws.com", "https://s3.us-east-1.amazonaws.com/bucket", "https://bucket.s3-us-west-2.amazonaws.com", "https://bucket.s3.amazonaws.com:443"} {
		if _, err := s3Target(target); err != nil {
			t.Errorf("s3Target(%q): %v", target, err)
		}
	}
}

func TestUploadBundleRejectsBadFieldsAndStatuses(t *testing.T) {
	c := newTestClient(t, newFakeAPI(t), &fakeS3{})
	for _, fields := range []map[string]string{{"bad name": "v"}, {"key": "a\r\nInjected: x"}} {
		if err := c.UploadBundle(context.Background(), &SignedUpload{URL: s3URL, Fields: fields}, writeBundleFile(t)); err == nil {
			t.Errorf("fields %v accepted", fields)
		}
	}
	c.Upload = &http.Client{Transport: &fakeS3{status: http.StatusForbidden}}
	if err := c.UploadBundle(context.Background(), &SignedUpload{URL: s3URL, Fields: map[string]string{}}, writeBundleFile(t)); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want the S3 status", err)
	}
	empty := filepath.Join(t.TempDir(), "empty.zip")
	os.WriteFile(empty, nil, 0o600)
	if err := c.UploadBundle(context.Background(), &SignedUpload{URL: s3URL, Fields: map[string]string{}}, empty); err == nil {
		t.Error("an empty bundle was uploaded")
	}
}

func TestSendReserveUploadFinalizeAsync(t *testing.T) {
	api, s3 := newFakeAPI(t), &fakeS3{}
	m := testMeta()
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
	api.on("POST", finalizePath, data(202, finalizedBody(m, true)))

	var steps []string
	res, err := newTestClient(t, api, s3).Send(context.Background(), writeBundleFile(t), m, Events{
		Reserved:   func(id string) { steps = append(steps, "reserved:"+id) },
		Uploading:  func() { steps = append(steps, "uploading") },
		Finalizing: func() { steps = append(steps, "finalizing") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Uploaded || res.Finalized.AnalysisJobID != jobID || res.Finalized.AnalysisState != AnalysisQueued {
		t.Errorf("result = %+v / %+v", res, res.Finalized)
	}
	if got := strings.Join(steps, ","); got != "reserved:"+publicationID+",uploading,finalizing" {
		t.Errorf("steps = %s", got)
	}
	var body map[string]bool
	json.Unmarshal(api.calls[1].body, &body)
	if !body["accepts_async"] {
		t.Errorf("finalize body = %s, want accepts_async true", api.calls[1].body)
	}
	if len(s3.requests) != 1 {
		t.Errorf("uploads = %d", len(s3.requests))
	}
}

func TestFinalizeAcceptsPlain200(t *testing.T) {
	api := newFakeAPI(t)
	m := testMeta()
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
	api.on("POST", finalizePath, data(200, finalizedBody(m, false)))
	res, err := newTestClient(t, api, &fakeS3{}).Send(context.Background(), writeBundleFile(t), m, Events{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Finalized.AnalysisJobID != "" {
		t.Errorf("job = %q, want none from a 200", res.Finalized.AnalysisJobID)
	}
}

func TestFinalized202WithoutJobIsRejected(t *testing.T) {
	api := newFakeAPI(t)
	m := testMeta()
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
	api.on("POST", finalizePath, data(202, finalizedBody(m, false)))
	if _, err := newTestClient(t, api, &fakeS3{}).Send(context.Background(), writeBundleFile(t), m, Events{}); err == nil {
		t.Fatal("a 202 without an analysis job was accepted")
	}
}

func TestFinalizedReplaySkipsUpload(t *testing.T) {
	api, s3 := newFakeAPI(t), &fakeS3{}
	m := testMeta()
	api.on("POST", reservePath, data(200, reserved(m, StateFinalized)))
	api.on("POST", finalizePath, data(200, finalizedBody(m, true)))
	res, err := newTestClient(t, api, s3).Send(context.Background(), writeBundleFile(t), m, Events{
		Uploading: func() { t.Error("a finalized replay must not upload") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Uploaded || len(s3.requests) != 0 || api.count("POST", finalizePath) != 1 {
		t.Errorf("uploaded=%v s3=%d finalize=%d", res.Uploaded, len(s3.requests), api.count("POST", finalizePath))
	}
}

func TestFinalizeRetriesTransientFailuresWithoutReuploading(t *testing.T) {
	api, s3 := newFakeAPI(t), &fakeS3{}
	m := testMeta()
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
	api.on("POST", finalizePath, reply{503, `{}`}, reply{502, `{}`}, reply{429, `{}`}, data(202, finalizedBody(m, true)))

	if _, err := newTestClient(t, api, s3).Send(context.Background(), writeBundleFile(t), m, Events{}); err != nil {
		t.Fatal(err)
	}
	if got := api.count("POST", finalizePath); got != 4 {
		t.Errorf("finalize calls = %d, want 4 (1 + 3 retries)", got)
	}
	if len(s3.requests) != 1 {
		t.Errorf("uploads = %d, want exactly 1", len(s3.requests))
	}
}

func TestFinalizeGivesUpAfterThreeRetries(t *testing.T) {
	api := newFakeAPI(t)
	m := testMeta()
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
	api.on("POST", finalizePath, reply{503, `{}`}, reply{503, `{}`}, reply{503, `{}`}, reply{503, `{}`}, reply{503, `{}`})
	_, err := newTestClient(t, api, &fakeS3{}).Send(context.Background(), writeBundleFile(t), m, Events{})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Fatalf("err = %v", err)
	}
	if got := api.count("POST", finalizePath); got != 4 {
		t.Errorf("finalize calls = %d, want 4", got)
	}
}

func TestAuthorizationRequiredNeedsA403WithTheCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
		code string
	}{
		{"error envelope", `{"error":{"code":"GITHUB_AUTHORIZATION_REQUIRED","details":{"authorization_url":"/github/authorize?installation_id=1"}}}`, true, "GITHUB_AUTHORIZATION_REQUIRED"},
		{"top-level code", `{"code":"GITHUB_AUTHORIZATION_REQUIRED"}`, true, "GITHUB_AUTHORIZATION_REQUIRED"},
		{"other code", `{"code":"GITHUB_ACCESS_DENIED"}`, false, "GITHUB_ACCESS_DENIED"},
		{"no code", `{}`, false, ""},
		{"malformed code", `{"code":"GITHUB_AUTHORIZATION_REQUIRED please visit evil.example"}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.on("POST", reservePath, reply{http.StatusForbidden, tc.body})
			_, err := newTestClient(t, api, &fakeS3{}).Reserve(context.Background(), testMeta())
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v", err)
			}
			if apiErr.Code != tc.code || apiErr.AuthorizationRequired() != tc.want {
				t.Errorf("code = %q, AuthorizationRequired = %v; want %q, %v", apiErr.Code, apiErr.AuthorizationRequired(), tc.code, tc.want)
			}
			if strings.Contains(err.Error(), "evil.example") || strings.Contains(err.Error(), "/github/authorize") {
				t.Errorf("error echoes server text: %v", err)
			}
		})
	}
	if (&Error{Status: http.StatusUnauthorized, Code: CodeAuthorizationRequired}).AuthorizationRequired() {
		t.Error("a 401 is not an authorization-required refusal")
	}
}

func TestFinalizeDoesNotRetryAuthOrConflict(t *testing.T) {
	for _, status := range []int{401, 403, 409, 404, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := newFakeAPI(t)
			m := testMeta()
			api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
			api.on("POST", finalizePath, reply{status, `{"code":"CONFLICT"}`}, data(200, finalizedBody(m, false)))
			if _, err := newTestClient(t, api, &fakeS3{}).Send(context.Background(), writeBundleFile(t), m, Events{}); err == nil {
				t.Fatal("want an error")
			}
			if got := api.count("POST", finalizePath); got != 1 {
				t.Errorf("finalize calls = %d, want 1 (not retried)", got)
			}
		})
	}
}

func TestFailedPublicationReplaysTheSameRevision(t *testing.T) {
	api := newFakeAPI(t)
	m := testMeta()
	s3 := &fakeS3{err: errors.New("S3 unavailable")}
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)), data(200, reserved(m, StateReserved)))
	api.on("POST", finalizePath, data(200, finalizedBody(m, true)))
	c := newTestClient(t, api, s3)
	path := writeBundleFile(t)

	if _, err := c.Send(context.Background(), path, m, Events{}); err == nil {
		t.Fatal("the failed upload must fail Send")
	}
	if api.count("POST", finalizePath) != 0 {
		t.Fatal("finalize ran after a failed upload")
	}
	s3.err = nil
	if _, err := c.Send(context.Background(), path, m, Events{}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	for _, c := range api.calls {
		if c.path == reservePath {
			var sent Metadata
			json.Unmarshal(c.body, &sent)
			if sent.Revision != m.Revision {
				t.Errorf("revision changed to %d", sent.Revision)
			}
		}
	}
}

func TestFinalizeRequiresConfirmedMatchingPublication(t *testing.T) {
	for field, value := range map[string]any{
		"state": StateReserved, "content_version": 0, "finalized_at": "",
		"publication_id": "55555555-5555-4555-8555-555555555555", "review_id": "55555555-5555-4555-8555-555555555555", "size": 9,
		"head_sha": strings.Repeat("b", 40), "revision": 8, "sha256": strings.Repeat("c", 64),
	} {
		t.Run(field, func(t *testing.T) {
			api := newFakeAPI(t)
			m := testMeta()
			body := finalizedBody(m, true)
			body[field] = value
			api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
			api.on("POST", finalizePath, data(202, body))
			_, err := newTestClient(t, api, &fakeS3{}).Send(context.Background(), writeBundleFile(t), m, Events{})
			if err == nil || (!strings.Contains(err.Error(), "confirm") && !strings.Contains(err.Error(), "mismatched")) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestFinalizeStopsWhenContextEnds(t *testing.T) {
	api := newFakeAPI(t)
	m := testMeta()
	api.on("POST", reservePath, data(201, reserved(m, StateReserved)))
	api.on("POST", finalizePath, reply{503, `{}`}, reply{503, `{}`})
	c := newTestClient(t, api, &fakeS3{})
	c.Backoff = []time.Duration{time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := c.Send(ctx, writeBundleFile(t), m, Events{}); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context deadline", err)
	}
}

func TestAnalysisStatus(t *testing.T) {
	analysis := func(state string) map[string]any {
		return map[string]any{
			"publication_id": publicationID, "job_id": jobID, "state": state, "pipeline_version": "1",
			"attempt_count": 1, "max_attempts": 5, "error_code": nil, "updated_at": "2026-10-11T00:00:00Z", "completed_at": nil,
		}
	}
	api := newFakeAPI(t)
	api.on("GET", analysisPath,
		data(200, analysis("running")), data(200, analysis("succeeded")),
		reply{404, `{"code":"analysis_not_found"}`}, reply{404, `{"code":"not_found"}`}, data(200, analysis("exploded")))
	c := newTestClient(t, api, &fakeS3{})

	a, err := c.AnalysisStatus(context.Background(), publicationID)
	if err != nil || a.State != AnalysisRunning || a.Done() {
		t.Fatalf("running: %+v, %v", a, err)
	}
	if a, err = c.AnalysisStatus(context.Background(), publicationID); err != nil || !a.Done() {
		t.Fatalf("succeeded: %+v, %v", a, err)
	}
	if _, err = c.AnalysisStatus(context.Background(), publicationID); !errors.Is(err, ErrNoAnalysis) {
		t.Errorf("analysis_not_found: err = %v", err)
	}
	if _, err = c.AnalysisStatus(context.Background(), publicationID); errors.Is(err, ErrNoAnalysis) || err == nil {
		t.Errorf("a plain 404 is not ErrNoAnalysis: %v", err)
	}
	if _, err = c.AnalysisStatus(context.Background(), publicationID); err == nil {
		t.Error("an unknown state was accepted")
	}
	if _, err = c.AnalysisStatus(context.Background(), "../../etc"); err == nil {
		t.Error("a malformed publication id was accepted")
	}
	if api.calls[0].auth != "Bearer "+testToken {
		t.Errorf("auth = %q", api.calls[0].auth)
	}
}

func TestAPICallsDoNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a redirect was followed with the Pair token")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	c, _ := NewClient(redirector.URL, testToken)
	if _, err := c.Reserve(context.Background(), testMeta()); err == nil {
		t.Error("a redirect response was treated as success")
	}
}
