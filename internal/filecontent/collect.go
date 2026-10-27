package filecontent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/pr"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

// Options configures one Build.
type Options struct {
	Repo   string
	Number int
	PR     *model.PR

	// HeadSHA is the head the bundle is for. It is passed rather than read off
	// PR because GitHub caps its commit list, so a payload with no headRefOid
	// has the head only as its last commit — the value the scan folder and
	// bundle.json are named after. The manifest has to agree with them.
	HeadSHA string

	// Root is the local checkout, "" when there is none. Bytes are read from it
	// first when the object is present: it is exact, offline and free.
	Root string
	// Git overrides the reader used for Root, for tests.
	Git GitSource
	// Runner reads what the checkout cannot serve. nil turns the API off.
	Runner pr.Runner

	// Redactor redacts every byte written. nil = redact.Default(), which is
	// what a scan wants: the counts then land in the bundle's redaction
	// summary through the process-wide redactor the caller already reads.
	Redactor *redact.Redactor

	Limits Limits
	Now    time.Time
	Tool   string
}

// limits is the effective limit set.
func (o Options) limits() Limits {
	if o.Limits == (Limits{}) {
		return DefaultLimits()
	}
	return o.Limits
}

// Build reads every changed file it can and returns the manifest describing
// what it found. It never fails: a file that cannot be read is a skip with a
// reason, because a review that is missing one file's context is worth far more
// than a publication that did not happen.
func Build(o Options) *Manifest {
	m := newManifest(o)
	if o.PR == nil {
		return m
	}
	c := &collector{o: o, limits: o.limits(), man: m}
	c.resolveBase()

	before := c.redactor().Counts()
	for i := range o.PR.Files {
		m.Files = append(m.Files, c.file(&o.PR.Files[i]))
	}
	c.resolveCollisions()

	m.Redaction.Counts = delta(before, c.redactor().Counts())
	m.Totals = c.totals()
	return m
}

// collector carries the state one Build accumulates: what has been spent, and
// where bytes come from.
type collector struct {
	o      Options
	limits Limits
	man    *Manifest

	used    int64
	sides   int
	partial int
}

// resolveBase fixes the commit the old sides are read at.
//
// It is the merge base, never the base branch tip: GitHub renders `gh pr diff`
// as merge_base..head, so the hunks' own line numbers index the file at the
// merge base. A base tip carries every commit merged into the base branch since
// the PR forked, and segment ranges computed against it would silently point at
// other code. If the merge base cannot be resolved the old sides are omitted
// with no_base rather than guessed at.
//
// The API answers first because it is authoritative. A local merge-base against
// a stale branch, or against a base branch not fetched since a rebase, gives a
// commit GitHub did not diff against.
func (c *collector) resolveBase() {
	pr := c.o.PR
	head := c.headSHA()
	if pr == nil || pr.BaseRef == "" || head == "" {
		return
	}
	if c.hasAPI() {
		if sha, err := c.compareMergeBase(pr.BaseRef, head); err == nil {
			if sha = normaliseSHA(sha); sha != "" {
				c.setBase(sha, "api")
				return
			}
		}
	}
	if g := c.git(); g != nil {
		if sha, err := g.MergeBase(head, "origin/"+pr.BaseRef); err == nil {
			if sha = normaliseSHA(sha); sha != "" {
				c.setBase(sha, "git")
				return
			}
		}
	}
}

// headSHA is the head everything else is keyed to: the one the caller passed,
// which the scan folder and bundle.json are also named after, or the payload's
// own headRefOid. A payload with neither has no head to diff against.
func (c *collector) headSHA() string {
	if c.o.HeadSHA != "" {
		return c.o.HeadSHA
	}
	if c.o.PR != nil {
		return c.o.PR.HeadSHA
	}
	return ""
}

func (c *collector) setBase(sha, source string) {
	c.man.BaseSHA, c.man.BaseKind, c.man.BaseSource = sha, baseMergeBase, source
}

// compareMergeBase asks GitHub for the merge base of two revisions: the
// compare endpoint reports the commit the PR diff is taken against, in one
// call, without needing any object locally.
func (c *collector) compareMergeBase(base, head string) (string, error) {
	out, err := c.o.Runner.Run("api", fmt.Sprintf("repos/%s/compare/%s...%s", c.o.Repo, base, head))
	if err != nil {
		return "", err
	}
	var body struct {
		MergeBase struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return "", fmt.Errorf("parsing gh api compare: %w", err)
	}
	return body.MergeBase.SHA, nil
}

// file reads every side one changed file calls for.
func (c *collector) file(f *model.DiffFile) ManifestFile {
	out := ManifestFile{
		Path: f.Path, OldPath: f.OldPath, Status: f.Status,
		Sides: map[string]Side{},
	}
	for _, side := range sidesFor(f.Status) {
		rev := c.man.HeadSHA
		if side == SideOld {
			rev = c.man.BaseSHA
		}
		if rev == "" {
			// The old side has no revision to be read at; the new one only
			// lacks one when the payload carried no head at all.
			reason := ReasonNoBase
			if side == SideNew {
				reason = ReasonMissing
			}
			out.Skipped = addSkip(out.Skipped, side, Skipped{Reason: reason})
			continue
		}
		name := side + "/" + pathFor(f, side)
		s, sk := c.readSide(f, side, rev, name)
		if sk.Reason != "" {
			out.Skipped = addSkip(out.Skipped, side, sk)
			continue
		}
		out.Sides[side] = s
	}
	return out
}

// readSide produces one side's content, or the reason it has none.
func (c *collector) readSide(f *model.DiffFile, side, rev, name string) (Side, Skipped) {
	path := pathFor(f, side)
	if reason := UnsafeName(name); reason != "" {
		return Side{}, Skipped{Reason: reason}
	}
	if extBlocked(path) {
		return Side{}, Skipped{Reason: ReasonMedia}
	}
	// The diff's own binary verdict is trusted only where it means what it
	// says. In the files-API fallback commitFileToDiffFile sets the flag for any
	// file whose patch GitHub omitted — a large text file included — and that
	// file is exactly the one whose hunks give a reader nothing to work with,
	// so it is worth trying to serve.
	if f.Binary && c.o.PR.FilesSource == model.SourceDiff {
		return Side{}, Skipped{Reason: ReasonBinary}
	}

	raw, source, err := c.fetch(rev, path)
	if err != nil {
		return Side{}, c.skipFor(err)
	}
	red := c.redactor().FileBytes(raw)
	// FileBytes holds the newline count, so the line structure is intact and a
	// hunk's line numbers still point at the same code. This is the cheap
	// assertion that it did: it should never fire, and a recorded skip beats a
	// file whose lines have silently moved under the reader.
	if strings.Count(string(red), "\n") != strings.Count(string(raw), "\n") {
		return Side{}, Skipped{Reason: ReasonLineShift}
	}
	if isBinary(red) {
		return Side{}, Skipped{Reason: ReasonBinary}
	}
	// The line count comes from the file, not from its redacted form: it is the
	// file the diff's own line numbers index, and redaction can end one line
	// short of it (see redact.FileBytes).
	return c.fit(f, side, name, countLines(string(raw)), red, source)
}

// fetch reads one file at one revision, checking its size before downloading
// it, and preferring the local checkout.
func (c *collector) fetch(rev, path string) ([]byte, string, error) {
	if g := c.git(); g != nil {
		size, sizeErr := g.BlobSize(rev, path)
		if sizeErr == nil {
			if size > c.limits.MaxDownloadBytes {
				return nil, "", downloadTooLarge{size: size}
			}
			if b, err := g.Blob(rev, path); err == nil {
				return b, "git", nil
			}
		}
	}
	if !c.hasAPI() {
		return nil, "", errNoSource
	}
	a := c.api()
	size, sha, body, inlined, err := a.contents(rev, path)
	if err != nil {
		return nil, "", err
	}
	if size > c.limits.MaxDownloadBytes {
		return nil, "", downloadTooLarge{size: size}
	}
	if inlined {
		return body, "api", nil
	}
	if sha == "" {
		return nil, "", errNotFound
	}
	// The contents endpoint inlines at most 1 MiB and reports an empty body
	// above it, so anything larger goes through the blobs endpoint, which
	// carries up to 100 MB.
	b, err := a.blob(sha)
	if err != nil {
		return nil, "", err
	}
	return b, "api", nil
}

// fit writes a side whole when it fits, crops it to the windows around its
// changes when it does not, and reports it skipped when even that will not do.
func (c *collector) fit(f *model.DiffFile, side, name string, fullLines int, red []byte, source string) (Side, Skipped) {
	lines := splitLines(string(red))
	fullBytes := len(red)
	remaining := c.limits.MaxTotalBytes - c.used
	sidesLeft := c.limits.MaxSides - c.sides

	if int64(fullBytes) <= c.limits.MaxFileBytes && int64(fullBytes) <= remaining && sidesLeft > 0 {
		// A file that fits can still hold a wall: a minified bundle or a JSON
		// blob on one line is unreadable whatever its total size, so the
		// per-line cap applies to a whole side exactly as it does to a cropped
		// one.
		if longestLine(lines) > c.limits.MaxLineBytes {
			return Side{}, Skipped{Reason: ReasonTooLarge, Limit: LimitLineBytes}
		}
		s := Side{
			File: name, SHA256: sha256Hex(red), Bytes: fullBytes, Lines: fullLines,
			Ranges: [][2]int{}, FullLines: fullLines, FullBytes: fullBytes,
			Source: source, data: red,
		}
		c.spend(int64(fullBytes), false)
		return s, Skipped{}
	}

	// Redaction can leave the text one line shorter than the file it came from,
	// so a window the file's numbering produced is clipped to what is actually
	// there before anything is sliced or counted.
	ranges := clampRanges(windows(f, side, fullLines, c.limits.WindowMargin), len(lines))
	if len(ranges) == 0 {
		// No hunk to anchor a window on. A side with no hunks is all of the
		// file or none of it, and it did not fit whole.
		switch {
		case int64(fullBytes) > c.limits.MaxFileBytes:
			return Side{}, Skipped{Reason: ReasonTooLarge, Limit: LimitFileBytes, Bytes: int64(fullBytes)}
		case sidesLeft <= 0:
			return Side{}, Skipped{Reason: ReasonBudget, Limit: LimitSides, Bytes: int64(fullBytes)}
		default:
			return Side{}, Skipped{Reason: ReasonBudget, Limit: LimitTotalBytes, Bytes: int64(fullBytes)}
		}
	}

	cropped := sliceLines(lines, ranges)
	if int64(len(cropped)) > c.limits.MaxFileBytes {
		return Side{}, Skipped{Reason: ReasonTooLarge, Limit: LimitFileBytes, Bytes: int64(fullBytes)}
	}
	if longestKeptLine(lines, ranges) > c.limits.MaxLineBytes {
		// A minified bundle or a one-line JSON blob: the windows are the whole
		// file, which is the wall the cap exists to refuse.
		return Side{}, Skipped{Reason: ReasonTooLarge, Limit: LimitLineBytes}
	}
	if sidesLeft <= 0 {
		return Side{}, Skipped{Reason: ReasonBudget, Limit: LimitSides, Bytes: int64(len(cropped))}
	}
	if int64(len(cropped)) > remaining {
		return Side{}, Skipped{Reason: ReasonBudget, Limit: LimitTotalBytes, Bytes: int64(len(cropped))}
	}

	limit := LimitFileBytes
	if int64(fullBytes) <= c.limits.MaxFileBytes {
		limit = LimitTotalBytes // it fit the file cap; the running total cropped it
	}
	s := Side{
		File: name, SHA256: sha256Hex(cropped), Bytes: len(cropped), Lines: keptLines(ranges),
		Partial: true, Ranges: ranges, Limit: limit,
		FullLines: fullLines, FullBytes: fullBytes, Source: source, data: cropped,
	}
	c.spend(int64(len(cropped)), true)
	return s, Skipped{}
}

// spend charges a written side to the running totals.
func (c *collector) spend(bytes int64, partial bool) {
	c.used += bytes
	c.sides++
	if partial {
		c.partial++
	}
}

// resolveCollisions drops a side whose entry name another side already took,
// differing only by case. The service rejects a whole bundle over a
// case-insensitive duplicate entry, so the later path loses rather than the
// publication. The order is the manifest's own, so which one loses is fixed.
func (c *collector) resolveCollisions() {
	seen := map[string]bool{}
	for i := range c.man.Files {
		f := &c.man.Files[i]
		for _, side := range []string{SideNew, SideOld} {
			s, ok := f.Sides[side]
			if !ok {
				continue
			}
			key := strings.ToLower(s.File)
			if !seen[key] {
				seen[key] = true
				continue
			}
			delete(f.Sides, side)
			f.Skipped = addSkip(f.Skipped, side, Skipped{Reason: ReasonCollision})
			c.used -= int64(s.Bytes)
			c.sides--
			if s.Partial {
				c.partial--
			}
		}
	}
}

// totals summarises the artifact the manifest describes.
func (c *collector) totals() Totals {
	t := Totals{Files: len(c.man.Files), SidesWritten: c.sides, SidesPartial: c.partial, Bytes: c.used}
	for _, f := range c.man.Files {
		t.SidesSkipped += len(f.Skipped)
	}
	return t
}

func (c *collector) skipFor(err error) Skipped {
	var tooBig downloadTooLarge
	switch {
	case errors.As(err, &tooBig):
		return Skipped{Reason: ReasonTooLarge, Limit: LimitDownloadBytes, Bytes: tooBig.size}
	case errors.Is(err, errNotFound), isNotFound(err):
		return Skipped{Reason: ReasonMissing}
	default:
		return Skipped{Reason: ReasonError, Error: clip(redact.Text(err.Error()), maxRecordedError)}
	}
}

func (c *collector) redactor() *redact.Redactor {
	if c.o.Redactor != nil {
		return c.o.Redactor
	}
	return redact.Default()
}

func (c *collector) git() GitSource {
	if c.o.Git != nil {
		return c.o.Git
	}
	if c.o.Root == "" {
		return nil
	}
	return Git{Root: c.o.Root}
}

func (c *collector) hasAPI() bool { return c.o.Runner != nil && c.o.Repo != "" }

func (c *collector) api() api { return api{r: c.o.Runner, repo: c.o.Repo} }

// sidesFor is the set of sides a file's status calls for. New comes first, so
// the running totals are charged in a fixed order.
func sidesFor(status model.FileStatus) []string {
	switch status {
	case model.StatusAdded:
		return []string{SideNew}
	case model.StatusDeleted:
		return []string{SideOld}
	default:
		// modified, renamed, and anything a future status adds.
		return []string{SideNew, SideOld}
	}
}

// pathFor is the repo path a side lives at: the old path for the old side of a
// rename, otherwise the file's own path (which is already the old one for a
// deletion).
func pathFor(f *model.DiffFile, side string) string {
	if side == SideOld && f.OldPath != "" {
		return f.OldPath
	}
	return f.Path
}

func addSkip(m map[string]Skipped, side string, sk Skipped) map[string]Skipped {
	if m == nil {
		m = map[string]Skipped{}
	}
	m[side] = sk
	return m
}

// normaliseSHA lowercases a commit id and rejects anything that is not one.
func normaliseSHA(sha string) string {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) != 40 {
		return ""
	}
	for _, r := range sha {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return ""
		}
	}
	return sha
}

// delta is what a redaction pass added to the counters, for the manifest's own
// summary. bundle.json carries the merged total.
func delta(before, after map[string]int) map[string]int {
	out := map[string]int{}
	for kind, n := range after {
		if d := n - before[kind]; d > 0 {
			out[kind] = d
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clip shortens a recorded message the way the publication log does, on a rune
// boundary so the manifest stays valid UTF-8.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
