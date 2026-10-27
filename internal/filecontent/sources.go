package filecontent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rutvikchandla3/paircli/internal/pr"
)

// GitSource reads objects out of a local checkout. It is an interface so the
// git path can be tested without one, and it returns an error when the object
// is not present locally, which is the signal to fall back to the API.
type GitSource interface {
	// BlobSize is the byte size of path at rev, without reading it.
	BlobSize(rev, path string) (int64, error)
	// Blob is the bytes of path at rev.
	Blob(rev, path string) ([]byte, error)
	// MergeBase is the merge base of two revisions.
	MergeBase(a, b string) (string, error)
}

// Git reads objects out of the checkout at Root.
type Git struct{ Root string }

// BlobSize runs `git cat-file -s <rev>:<path>`.
func (g Git) BlobSize(rev, path string) (int64, error) {
	out, err := g.run("cat-file", "-s", rev+":"+path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("git cat-file -s %s:%s: %w", rev, path, err)
	}
	return n, nil
}

// Blob runs `git cat-file blob <rev>:<path>`.
func (g Git) Blob(rev, path string) ([]byte, error) {
	return g.run("cat-file", "blob", rev+":"+path)
}

// MergeBase runs `git merge-base a b`, which needs both objects locally.
func (g Git) MergeBase(a, b string) (string, error) {
	out, err := g.run("merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g Git) run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", g.Root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// api reads file bytes through `gh api`.
type api struct {
	r    pr.Runner
	repo string
}

// contents reads one file through the contents endpoint: its size, its blob
// id, and its bytes when GitHub inlined them.
//
// It is one call, not a metadata call followed by a content call: the same
// response carries all three, and asking twice doubles both the round trips and
// the rate-limit cost on a large pull request.
func (a api) contents(rev, path string) (size int64, sha string, body []byte, inlined bool, err error) {
	out, err := a.r.Run("api", fmt.Sprintf("repos/%s/contents/%s?ref=%s", a.repo, escapePath(path), rev))
	if err != nil {
		return 0, "", nil, false, err
	}
	var reply struct {
		Size     int64  `json:"size"`
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return 0, "", nil, false, fmt.Errorf("parsing gh api contents for %s: %w", path, err)
	}
	if reply.Encoding != "base64" || reply.Content == "" {
		// Above 1 MiB the endpoint reports the size and the blob id and nothing
		// else, which is the whole point of reading the size first.
		return reply.Size, reply.SHA, nil, false, nil
	}
	b, err := decodeBase64(reply.Content)
	if err != nil {
		return 0, "", nil, false, err
	}
	return reply.Size, reply.SHA, b, true, nil
}

// blob returns the bytes of a file the contents endpoint declined to inline.
func (a api) blob(sha string) ([]byte, error) {
	out, err := a.r.Run("api", fmt.Sprintf("repos/%s/git/blobs/%s", a.repo, sha))
	if err != nil {
		return nil, err
	}
	var body struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("parsing gh api blob %s: %w", sha, err)
	}
	if body.Encoding != "base64" {
		return nil, fmt.Errorf("gh api blob %s: unexpected encoding %q", sha, body.Encoding)
	}
	return decodeBase64(body.Content)
}

// escapePath percent-escapes one repo path for a URL, keeping its separators.
func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func decodeBase64(s string) ([]byte, error) {
	// The API wraps its base64 at 60 characters.
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(s, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decoding base64 content: %w", err)
	}
	return b, nil
}

// isBinary reports whether content is not text worth sending: a NUL byte near
// the start, or bytes that are not valid UTF-8. It is decided from the bytes
// and never from the diff's own guess, which means different things depending
// on the endpoint that produced it.
func isBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	return !utf8.Valid(b)
}

// mediaExtensions are never fetched. An image, a recording, a font, an archive,
// a document, compiled output or a database holds nothing a reader can be
// guided through, and fetching one puts a blob in the bundle for no gain.
//
// SVG is deliberately absent: it is text, and it can carry script and hidden
// text that a reading guide exists to flag. So are .pem and .key, which are
// text and must be redacted rather than skipped.
var mediaExtensions = map[string]bool{
	// images
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "bmp": true,
	"ico": true, "tiff": true, "tif": true, "heic": true, "heif": true, "avif": true,
	"psd": true, "ai": true, "eps": true,
	// audio and video
	"mp3": true, "wav": true, "ogg": true, "oga": true, "flac": true, "m4a": true,
	"aac": true, "opus": true, "mp4": true, "m4v": true, "mov": true, "avi": true,
	"mkv": true, "webm": true, "wmv": true, "flv": true, "mpg": true, "mpeg": true,
	"3gp": true,
	// fonts
	"woff": true, "woff2": true, "ttf": true, "otf": true, "eot": true,
	// archives and packages
	"zip": true, "tar": true, "gz": true, "tgz": true, "bz2": true, "xz": true,
	"zst": true, "7z": true, "rar": true, "jar": true, "war": true, "apk": true,
	"deb": true, "rpm": true, "dmg": true, "iso": true,
	// documents
	"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true, "ppt": true,
	"pptx": true, "odt": true, "ods": true, "odp": true, "rtf": true, "pages": true,
	"numbers": true, "keynote": true,
	// compiled code
	"exe": true, "dll": true, "so": true, "dylib": true, "a": true, "o": true,
	"obj": true, "lib": true, "class": true, "pyc": true, "pyo": true, "wasm": true,
	"bin": true,
	// databases and model weights
	"sqlite": true, "sqlite3": true, "db": true, "db3": true, "mdb": true,
	"accdb": true, "realm": true, "onnx": true, "pt": true, "pth": true, "h5": true,
	"safetensors": true, "gguf": true, "ckpt": true, "npy": true, "npz": true,
	"parquet": true,
}

// extBlocked reports whether a path's extension puts it on the deny list.
func extBlocked(path string) bool {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 || dot == len(path)-1 {
		return false
	}
	ext := path[dot+1:]
	// A path with no separator after the dot has no extension to speak of, so
	// only the last segment counts.
	if strings.ContainsAny(ext, "/\\") {
		return false
	}
	return mediaExtensions[strings.ToLower(ext)]
}

// maxRecordedError is how much of a failure the manifest keeps, matching the
// publication log's own limit.
const maxRecordedError = 500

// errNotFound is a file that is not at the revision it was asked for.
var errNotFound = errors.New("no such file at that revision")

// errNoSource is a file neither the checkout nor the GitHub API could serve.
var errNoSource = errors.New("no local object and no GitHub API to read it from")

// downloadTooLarge is a file whose reported size is over the download ceiling.
// It is raised before a byte of it is fetched, so an oversized blob never
// crosses the wire to be rejected on arrival.
type downloadTooLarge struct{ size int64 }

func (e downloadTooLarge) Error() string {
	return fmt.Sprintf("file is %d bytes, over the download ceiling", e.size)
}

// UnsafeName reports why name cannot be used as an entry inside changed-files/,
// or "" when it can.
//
// It is exported because internal/publish reads these names back out of a
// manifest on disk, and a manifest is a file anything could have written: the
// name is joined to a path, so it is checked again there rather than trusted.
func UnsafeName(name string) string {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "//") ||
		strings.ContainsAny(name, "\\\x00") {
		return ReasonUnsafePath
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return ReasonUnsafePath
		}
		if len(part) > 1 && part[1] == ':' {
			return ReasonUnsafePath
		}
	}
	return ""
}

// isNotFound reports whether an error from gh means the file is absent rather
// than that the call failed.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(msg, "Not Found")
}
