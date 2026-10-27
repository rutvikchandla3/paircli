package publish

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Publication states the server reports.
const (
	StateReserved  = "reserved"
	StateFinalized = "finalized"
)

// Analysis states the server reports for a publication's analysis job.
const (
	AnalysisQueued    = "queued"
	AnalysisRunning   = "running"
	AnalysisSucceeded = "succeeded"
	AnalysisFailed    = "failed"
)

// Publication is the reservation the server returns.
type Publication struct {
	PublicationID string        `json:"publication_id"`
	ReviewID      string        `json:"review_id"`
	Owner         string        `json:"owner"`
	Repo          string        `json:"repo"`
	PullNumber    int           `json:"pull_number"`
	HeadSHA       string        `json:"head_sha"`
	Revision      int64         `json:"revision"`
	SHA256        string        `json:"sha256"`
	Size          int64         `json:"size"`
	State         string        `json:"state"`
	Upload        *SignedUpload `json:"upload"`
}

// SignedUpload is the presigned S3 POST form.
type SignedUpload struct {
	Method string            `json:"method"`
	URL    string            `json:"url"`
	Fields map[string]string `json:"fields"`
}

// Finalized is the server's confirmation that the evidence is stored.
type Finalized struct {
	PublicationID  string `json:"publication_id"`
	ReviewID       string `json:"review_id"`
	HeadSHA        string `json:"head_sha"`
	Revision       int64  `json:"revision"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	State          string `json:"state"`
	FinalizedAt    string `json:"finalized_at"`
	ContentVersion int    `json:"content_version"`
	AnalysisJobID  string `json:"analysis_job_id"`
	AnalysisState  string `json:"analysis_state"`
}

// Analysis is the state of a publication's analysis job.
type Analysis struct {
	PublicationID   string  `json:"publication_id"`
	JobID           string  `json:"job_id"`
	State           string  `json:"state"`
	PipelineVersion string  `json:"pipeline_version"`
	AttemptCount    int     `json:"attempt_count"`
	MaxAttempts     int     `json:"max_attempts"`
	ErrorCode       *string `json:"error_code"`
	UpdatedAt       string  `json:"updated_at"`
	CompletedAt     *string `json:"completed_at"`
}

// Done reports whether the job has reached a final state.
func (a Analysis) Done() bool { return a.State == AnalysisSucceeded || a.State == AnalysisFailed }

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	s3Host      = regexp.MustCompile(`(^|\.)s3[.-]`)
	fieldName   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// validUUID reports whether s is a canonical, non-nil UUID.
func validUUID(s string) bool {
	return uuidPattern.MatchString(s) && strings.Trim(s, "0-") != ""
}

const (
	invalidPublication = "Pair returned an invalid publication response"
	mismatched         = "Pair returned mismatched publication metadata"
	invalidUpload      = "Pair returned an invalid publication upload"
)

// Reserve asks for an upload slot for the bundle described by meta.
func (c *Client) Reserve(ctx context.Context, meta Metadata) (*Publication, error) {
	_, data, err := c.do(ctx, http.MethodPost, "/api/v1/reviews/publications/upload-url", meta, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	var p Publication
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, errors.New(invalidPublication)
	}
	if err := validateReservation(&p, meta); err != nil {
		return nil, err
	}
	return &p, nil
}

// validateReservation checks a reservation against the request it answers.
func validateReservation(p *Publication, meta Metadata) error {
	if !validUUID(p.PublicationID) || !validUUID(p.ReviewID) {
		return errors.New(mismatched)
	}
	if p.HeadSHA != meta.HeadSHA || p.Revision != meta.Revision || p.SHA256 != meta.SHA256 || p.Size != meta.Size {
		return errors.New(mismatched)
	}
	if p.PullNumber != meta.PullNumber || strings.TrimSpace(p.Owner) == "" || strings.TrimSpace(p.Repo) == "" {
		return errors.New(invalidUpload)
	}
	if p.State == StateFinalized && p.Upload == nil {
		return nil
	}
	if p.State != StateReserved || p.Upload == nil || p.Upload.Method != http.MethodPost {
		return errors.New(invalidUpload)
	}
	return validateUploadFields(p.Upload.Fields, meta)
}

// validateUploadFields requires the signed form to be exactly the upload this
// client asked for: its object key, content type and SHA-256 checksum.
func validateUploadFields(fields map[string]string, meta Metadata) error {
	digest, err := hex.DecodeString(meta.SHA256)
	if err != nil || len(digest) != 32 {
		return errors.New(invalidUpload)
	}
	key := regexp.MustCompile(fmt.Sprintf(`^v1/reviews/[1-9][0-9]*/%d/%s/bundles/([0-9a-fA-F-]{36})/%d/%s\.zip$`,
		meta.PullNumber, regexp.QuoteMeta(meta.HeadSHA), meta.Revision, regexp.QuoteMeta(meta.SHA256)))
	match := key.FindStringSubmatch(fields["key"])
	if match == nil || !validUUID(match[1]) {
		return errors.New(invalidUpload)
	}
	if fields["Content-Type"] != "application/zip" ||
		fields["x-amz-checksum-algorithm"] != "SHA256" ||
		fields["x-amz-checksum-sha256"] != base64.StdEncoding.EncodeToString(digest) {
		return errors.New(invalidUpload)
	}
	return nil
}

// UploadBundle POSTs the bundle file to the signed S3 form. The form must name
// an HTTPS S3 host; the Pair token is never sent to it.
func (c *Client) UploadBundle(ctx context.Context, signed *SignedUpload, bundlePath string) error {
	target, err := s3Target(signed.URL)
	if err != nil {
		return err
	}
	info, err := os.Stat(bundlePath)
	if err != nil {
		return err
	}
	size := info.Size()
	if size < 1 || size > MaxBundleBytes {
		return errors.New("bundle exceeds the 100 MiB upload limit")
	}

	boundary, err := newBoundary()
	if err != nil {
		return err
	}
	head, err := multipartHead(signed.Fields, boundary)
	if err != nil {
		return err
	}
	tail := []byte("\r\n--" + boundary + "--\r\n")
	length := int64(len(head)) + size + int64(len(tail))
	if length > MaxBundleBytes+1024*1024 {
		return errors.New("bundle multipart upload is too large")
	}

	file, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer file.Close()
	body := io.MultiReader(bytes.NewReader(head), io.LimitReader(file, size), bytes.NewReader(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return err
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	resp, err := c.Upload.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{cause: sanitize(err)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponse))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	}
	return fmt.Errorf("S3 rejected the bundle (HTTP %d)", resp.StatusCode)
}

// s3Target returns rawURL when it is an HTTPS S3 endpoint and nothing else.
func s3Target(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	host := ""
	if err == nil {
		host = u.Hostname()
	}
	if err != nil || u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") || u.User != nil ||
		!strings.HasSuffix(host, ".amazonaws.com") || !s3Host.MatchString(host) {
		return "", errors.New("Pair returned a non-S3 HTTPS upload URL")
	}
	return u.String(), nil
}

func newBoundary() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "pair-" + hex.EncodeToString(raw), nil
}

// multipartHead is every form field, in sorted order, followed by the header of
// the trailing "file" part, which S3 requires to come last.
func multipartHead(fields map[string]string, boundary string) ([]byte, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var b bytes.Buffer
	for _, name := range names {
		value := fields[name]
		if !fieldName.MatchString(name) || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("Pair returned an invalid S3 form field")
		}
		fmt.Fprintf(&b, "--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n", boundary, name, value)
	}
	fmt.Fprintf(&b, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"bundle.zip\"\r\nContent-Type: application/zip\r\n\r\n", boundary)
	return b.Bytes(), nil
}

// Finalize confirms the upload. It asks for the asynchronous answer (202 with
// the analysis job) but also accepts a plain 200. A transient failure is
// retried on c.Backoff; the bundle is never uploaded again by this call.
func (c *Client) Finalize(ctx context.Context, p *Publication, meta Metadata) (*Finalized, error) {
	path := "/api/v1/reviews/publications/" + p.PublicationID + "/finalize"
	var lastErr error
	for attempt := 0; ; attempt++ {
		status, data, err := c.do(ctx, http.MethodPost, path, map[string]bool{"accepts_async": true}, http.StatusOK, http.StatusAccepted)
		if err == nil {
			return validateFinalized(status, data, p, meta)
		}
		var apiErr *Error
		if !errors.As(err, &apiErr) || !apiErr.Transient() || attempt >= len(c.Backoff) {
			return nil, err
		}
		lastErr = err
		if err := sleep(ctx, c.Backoff[attempt]); err != nil {
			return nil, fmt.Errorf("%w (last error: %v)", err, lastErr)
		}
	}
}

func validateFinalized(status int, data json.RawMessage, p *Publication, meta Metadata) (*Finalized, error) {
	var f Finalized
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, errors.New(invalidPublication)
	}
	if !validUUID(f.PublicationID) || !validUUID(f.ReviewID) ||
		f.HeadSHA != meta.HeadSHA || f.Revision != meta.Revision || f.SHA256 != meta.SHA256 || f.Size != meta.Size {
		return nil, errors.New(mismatched)
	}
	if f.PublicationID != p.PublicationID || f.ReviewID != p.ReviewID || f.State != StateFinalized ||
		f.FinalizedAt == "" || f.ContentVersion < 1 {
		return nil, errors.New("Pair did not confirm finalized review evidence")
	}
	if status == http.StatusAccepted && (!validUUID(f.AnalysisJobID) || f.AnalysisState == "") {
		return nil, errors.New("Pair accepted finalization without an analysis job")
	}
	if f.AnalysisJobID != "" && !validUUID(f.AnalysisJobID) {
		return nil, errors.New(invalidPublication)
	}
	return &f, nil
}

// ErrNoAnalysis means the publication has no analysis job.
var ErrNoAnalysis = errors.New("Pair has no analysis for this publication")

// AnalysisStatus reads the state of a publication's analysis job.
func (c *Client) AnalysisStatus(ctx context.Context, publicationID string) (*Analysis, error) {
	if !validUUID(publicationID) {
		return nil, errors.New("invalid publication id")
	}
	_, data, err := c.do(ctx, http.MethodGet, "/api/v1/reviews/publications/"+publicationID+"/analysis", nil, http.StatusOK)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code == "analysis_not_found" {
			return nil, ErrNoAnalysis
		}
		return nil, err
	}
	var a Analysis
	if err := json.Unmarshal(data, &a); err != nil || !validUUID(a.JobID) {
		return nil, errors.New(invalidPublication)
	}
	switch a.State {
	case AnalysisQueued, AnalysisRunning, AnalysisSucceeded, AnalysisFailed:
	default:
		return nil, errors.New(invalidPublication)
	}
	return &a, nil
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Events lets a caller follow Send's progress.
type Events struct {
	Reserved   func(publicationID string)
	Uploading  func()
	Finalizing func()
}

// Result is a finished publication.
type Result struct {
	Publication *Publication
	Finalized   *Finalized
	Uploaded    bool // false when the server already held the bundle
}

// Send publishes the bundle at path: reserve, upload (skipped when the server
// already finalized this exact bundle), finalize.
func (c *Client) Send(ctx context.Context, path string, meta Metadata, ev Events) (*Result, error) {
	pub, err := c.Reserve(ctx, meta)
	if err != nil {
		return nil, err
	}
	if ev.Reserved != nil {
		ev.Reserved(pub.PublicationID)
	}
	res := &Result{Publication: pub}
	if pub.State == StateReserved {
		if ev.Uploading != nil {
			ev.Uploading()
		}
		if err := c.UploadBundle(ctx, pub.Upload, path); err != nil {
			return nil, err
		}
		res.Uploaded = true
	}
	if ev.Finalizing != nil {
		ev.Finalizing()
	}
	res.Finalized, err = c.Finalize(ctx, pub, meta)
	if err != nil {
		return nil, err
	}
	return res, nil
}
