package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rutvikchandla3/paircli/internal/capture"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/filecontent"
	"github.com/rutvikchandla3/paircli/internal/notify"
	"github.com/rutvikchandla3/paircli/internal/pr"
	"github.com/rutvikchandla3/paircli/internal/prcomment"
	"github.com/rutvikchandla3/paircli/internal/publish"
	"github.com/rutvikchandla3/paircli/internal/redact"
	"github.com/rutvikchandla3/paircli/internal/scan"
	"github.com/rutvikchandla3/paircli/internal/state"
)

// Exit codes of `paircli publish`: 0 and 1 are the usual; 2 means no login
// could be completed.
const exitNotLoggedIn = 2

// exitError carries a specific process exit code through the error return.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }
func (e *exitError) ExitCode() int { return e.code }

const (
	defaultAnalysisTimeout = 20 * time.Minute
	pollStart              = 5 * time.Second
	pollMax                = 15 * time.Second
	maxRecordedError       = 500
)

var headPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// publishArgs is a parsed `paircli publish` invocation.
type publishArgs struct {
	PRArg     string
	RepoDir   string
	Wait      bool
	Timeout   time.Duration
	DryRun    bool
	NoComment bool
	JSON      bool

	// NoFileContents turns off the whole-file contents a bundle normally
	// carries. The config key is the per-repo switch; this is the one-off.
	NoFileContents bool
}

// parsePublishArgs parses `publish <pr-number-or-url> [flags]`: the PR
// argument comes first, as with scan.
func parsePublishArgs(args []string) (publishArgs, error) {
	a := publishArgs{Timeout: defaultAnalysisTimeout}
	if len(args) < 1 {
		return a, errors.New("usage: paircli publish <pr-number-or-url> [flags]")
	}
	if strings.HasPrefix(args[0], "-") {
		return a, errors.New("usage: paircli publish <pr-number-or-url> [flags] (the PR argument comes first)")
	}
	a.PRArg = args[0]

	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the caller reports parse errors itself
	fs.StringVar(&a.RepoDir, "repo-dir", "", "repository directory (default: the current directory)")
	fs.BoolVar(&a.Wait, "wait", false, "wait until the published review is ready")
	fs.DurationVar(&a.Timeout, "timeout", defaultAnalysisTimeout, "how long --wait waits")
	fs.BoolVar(&a.DryRun, "dry-run", false, "build the bundle and print the plan; change nothing remote")
	fs.BoolVar(&a.NoComment, "no-comment", false, "do not post the Pair link comment on the PR")
	fs.BoolVar(&a.JSON, "json", false, "print the result as JSON on stdout")
	fs.BoolVar(&a.NoFileContents, "no-file-contents", false, "do not upload the changed files' contents")
	if err := fs.Parse(args[1:]); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return a, nil
}

// publishEnv is everything `paircli publish` reaches outside the process for,
// so tests can run it against fakes.
type publishEnv struct {
	runner     pr.Runner
	credential func(out, progress io.Writer) (server, token string, err error)
	newClient  func(server, token string) (*publish.Client, error)
	notify     func(title, message string) bool
	sleep      func(ctx context.Context, d time.Duration) error
	now        func() time.Time
	stdout     io.Writer
	stderr     io.Writer
}

func defaultPublishEnv() publishEnv {
	return publishEnv{
		runner:     pr.GH{},
		credential: loginCredential,
		newClient:  publish.NewClient,
		notify:     notify.Send,
		sleep:      sleepContext,
		now:        time.Now,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
	}
}

// loginCredential returns the server and token to publish with, running the
// device login first when there is no usable credential.
func loginCredential(out, progress io.Writer) (string, string, error) {
	server := resolveServer("")
	auth, err := ensureLogin(server, out, progress)
	if err != nil {
		return "", "", err
	}
	if auth.ServerURL != "" {
		server = strings.TrimRight(auth.ServerURL, "/")
	}
	return server, auth.AccessToken, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// runPublish implements `paircli publish`.
func runPublish(args []string) error {
	a, err := parsePublishArgs(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return publishRun(ctx, defaultPublishEnv(), a)
}

// publisher is one run of `paircli publish` for one PR.
type publisher struct {
	env    publishEnv
	args   publishArgs
	owner  string
	repo   string
	number int
	dir    string // repo directory

	rec     state.Publication
	logFile *os.File
	logOut  io.Writer

	appendLog bool // a rerun adds to the log instead of replacing it

	client   *publish.Client
	uploaded bool   // the bundle is stored on Pair
	revision int64  // of that upload
	token    string // names this run's upload in state.Latest; "" when the claim could not be written
}

// maxReruns is how many extra full runs one process makes for pushes that
// arrived while it was publishing.
const maxReruns = 3

// publishRun publishes one PR's evidence: scan, capture, package, upload,
// finalize, comment, notify and, with --wait, follow the review until it is
// ready.
//
// At most one run per PR is publishing. A run that finds the PR's lock taken
// leaves a pending marker and exits successfully; the run holding the lock,
// once its steps are done and while it still holds the lock, takes the marker
// and runs the whole pipeline again (which resolves the head afresh, so a push
// made mid-run is published), at most maxReruns times. A rerun whose head has
// not changed since a successful run does nothing.
//
// The lock covers the publishing only: it is released as soon as the last
// upload is stored, before --wait follows the review, so a push during that
// wait publishes at once instead of leaving a marker. A newer upload of the
// same PR ends the older run's wait (see state.Latest).
func publishRun(ctx context.Context, env publishEnv, a publishArgs) error {
	dir := a.RepoDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	}
	slug, number, err := pr.ResolveArg(dir, a.PRArg)
	if err != nil {
		return err
	}
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok {
		return fmt.Errorf("publish: %q is not owner/repo", slug)
	}
	key := state.Key(owner, repo, number)

	lock, err := acquirePRLock(env, a, key, owner, repo, number, dir)
	if lock == nil || err != nil {
		return err
	}

	reruns := 0
	for {
		p := &publisher{env: env, args: a, owner: owner, repo: repo, number: number, dir: dir, appendLog: reruns > 0}
		p.openLog()
		runErr := p.run(ctx)
		if reruns < maxReruns && ctx.Err() == nil && p.rerunWanted(key, runErr) {
			p.closeLog()
			reruns++
			continue
		}
		lock.Release()

		// A push can land between the last marker check and the release; its
		// run found the lock taken, left a marker and gave up.
		if !a.DryRun && reruns < maxReruns && ctx.Err() == nil && state.HasPending(key) {
			// A lock taken by someone else means another run owns the PR now
			// and will take the marker.
			if relock, lockErr := state.TryLock(key); lockErr == nil {
				state.TakePending(key)
				lock = relock
				p.closeLog()
				reruns++
				continue
			}
		}
		return p.finish(ctx, runErr)
	}
}

// acquirePRLock takes the PR's lock. When another run holds it, a real run
// leaves a pending marker for the holder and tries once more, in case the
// holder released in between; a nil lock with a nil error means the request
// was handed to the holder.
func acquirePRLock(env publishEnv, a publishArgs, key, owner, repo string, number int, dir string) (*state.Lock, error) {
	lock, err := state.TryLock(key)
	if !errors.Is(err, state.ErrLocked) {
		return lock, err
	}
	if !a.DryRun {
		req := state.PendingRequest{PRURL: fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number), RepoDir: dir, RequestedAt: env.now().UTC()}
		if err := state.MarkPending(key, req); err != nil {
			return nil, err
		}
		if lock, err = state.TryLock(key); err == nil {
			state.TakePending(key) // this run is the one the marker asked for
			return lock, nil
		}
		if !errors.Is(err, state.ErrLocked) {
			return nil, err
		}
	}
	fmt.Fprintf(env.stdout, "paircli publish: %s/%s#%d is already being published by another run; it will publish again for the newest head\n", owner, repo, number)
	return nil, nil
}

// rerunWanted takes the pending marker and says whether to run again. A run
// that ended in a login failure is not repeated, and neither is a head that a
// successful run has already published.
func (p *publisher) rerunWanted(key string, runErr error) bool {
	if p.args.DryRun {
		return false
	}
	if _, ok := state.TakePending(key); !ok {
		return false
	}
	var coded *exitError
	if errors.As(runErr, &coded) {
		return false
	}
	if runErr == nil && p.rec.HeadSHA != "" {
		if head, err := pr.HeadSHA(p.env.runner, p.owner+"/"+p.repo, p.number); err == nil && head == p.rec.HeadSHA {
			p.logf("another publish was requested, but the head %s is already published", head[:12])
			return false
		}
	}
	p.logf("another publish was requested while this one ran; publishing again")
	return true
}

func (p *publisher) prURL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", p.owner, p.repo, p.number)
}

// openLog tees the run's log lines into a per-PR file under the log dir, which
// the publication record points at. A log that cannot be opened is skipped.
func (p *publisher) openLog() {
	w := p.env.stdout
	if p.args.JSON {
		w = p.env.stderr // stdout stays the one JSON document
	}
	p.logOut = w
	dir := state.LogDir()
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path := filepath.Join(dir, "publish-"+state.Key(p.owner, p.repo, p.number)+".log")
	// O_APPEND even for the truncating open: an older run still following its
	// review keeps this file open and must not write at its old offset.
	mode := os.O_CREATE | os.O_WRONLY | os.O_APPEND | os.O_TRUNC
	if p.appendLog {
		mode = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(path, mode, 0o600)
	if err != nil {
		return
	}
	p.logFile = f
	p.rec.Log = path
	p.logOut = io.MultiWriter(w, f)
}

func (p *publisher) closeLog() {
	if p.logFile != nil {
		p.logFile.Close()
	}
}

func (p *publisher) logf(format string, args ...any) {
	fmt.Fprintf(p.logOut, "%s paircli publish: %s\n", p.env.now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// detailf writes a line to the run's log file only, never to the terminal: it
// is for what concerns Pair's background work, which the publisher is not
// told about.
func (p *publisher) detailf(format string, args ...any) {
	if p.logFile == nil {
		return
	}
	fmt.Fprintf(p.logFile, "%s paircli publish: %s\n", p.env.now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func (p *publisher) key() string { return state.Key(p.owner, p.repo, p.number) }

// save writes the publication record. A dry run leaves the real record alone,
// and a failed write is logged, never fatal.
func (p *publisher) save() {
	if p.args.DryRun {
		return
	}
	if err := state.WritePublication(p.rec); err != nil {
		p.logf("could not write the publication record: %v", err)
	}
}

// step records that the run has reached a new step.
func (p *publisher) step(name string) {
	p.rec.Step = name
	p.logf("%s", name)
	p.save()
}

// fail records the failure, tells the user and returns the error to exit with.
func (p *publisher) fail(err error) error {
	msg := redact.Text(err.Error())
	if len(msg) > maxRecordedError {
		msg = msg[:maxRecordedError]
	}
	p.rec.State, p.rec.Error = state.StateFailed, msg
	p.save()
	p.logf("failed at %s: %s", p.rec.Step, msg)
	if !p.args.DryRun {
		p.env.notify("Pair review failed", fmt.Sprintf("PR #%d could not be published. Run paircli status for details.", p.number))
	}
	return err
}

func (p *publisher) run(ctx context.Context) error {
	p.rec = state.Publication{
		PRURL: p.prURL(), Owner: p.owner, Repo: p.repo, Number: p.number,
		State: state.StateRunning, Step: state.StepScan, Log: p.rec.Log,
		ReviewURL: state.ReviewURL(p.owner, p.repo, p.number), StartedAt: p.env.now().UTC(),
	}
	p.logf("publishing %s", p.rec.PRURL)
	if note := legacyScanNote(p.dir, scan.RepoRoot(p.dir, p.owner+"/"+p.repo)); note != "" {
		p.logf("%s", note)
	}
	p.save()

	server, client, err := p.connect()
	if err != nil {
		return err
	}

	p.step(state.StepScan)
	redact.Default().ResetCounts()
	// Loaded here rather than left to scan.Run because the bundle's contents
	// depend on it too, and the two must agree: a subtree the scan wrote is
	// only uploaded when the same option asked for it.
	cfg, err := config.Load(scan.RepoRoot(p.dir, p.owner+"/"+p.repo))
	if err != nil {
		return p.fail(err)
	}
	withContents := cfg.ChangedFileContents() && !p.args.NoFileContents
	res, err := scan.Run(ctx, scan.Options{
		Repo:                p.owner + "/" + p.repo,
		Number:              p.number,
		CWD:                 p.dir,
		Runner:              p.env.runner,
		Config:              cfg,
		Snapshot:            true,
		ChangedFileContents: withContents,
		Version:             version,
	})
	if err != nil {
		return p.fail(err)
	}
	if !headPattern.MatchString(res.Head) {
		return p.fail(fmt.Errorf("the PR head %q is not a commit SHA", res.Head))
	}
	p.rec.HeadSHA = res.Head
	p.logf("scanned %s: %d linked sessions", res.OutDir, len(res.Report.Sessions))
	if len(res.Report.Sessions) == 0 {
		p.logf("no agent session on this machine is linked to the PR (%d from its dates were set aside); a session links "+
			"when it ran `git commit` with one of the PR's commit messages, was on the PR's branch, or edited its files, "+
			"and only Claude Code, Codex and Pi sessions are read", res.Report.Coverage.DroppedSessions)
	}

	p.step(state.StepCapture)
	capRedactor := redact.New()
	staged, err := capture.Stage(capture.Options{
		Head:     res.Head,
		Scope:    server + "|" + state.Key(p.owner, p.repo, p.number),
		Sessions: res.Sessions,
		Redactor: capRedactor,
		Now:      p.env.now(),
	})
	if err != nil {
		p.logf("raw sessions skipped: %v", err)
		staged = nil
	}
	if staged != nil {
		for _, note := range staged.Notes {
			p.logf("capture: %s", note)
		}
	}

	p.step(state.StepPackage)
	outDir, err := os.MkdirTemp("", "paircli-bundle-")
	if err != nil {
		return p.fail(err)
	}
	if !p.args.DryRun {
		defer os.RemoveAll(outDir)
	}
	bundle, err := publish.BuildBundle(publish.BundleOptions{
		Repo:          p.owner + "/" + p.repo,
		Number:        p.number,
		Head:          res.Head,
		ScanDir:       res.OutDir,
		Raw:           staged,
		CaptureCounts: capRedactor.Counts(),
		ScanCounts:    redact.Default().Counts(),
		Version:       version,
		Now:           p.env.now(),
		OutDir:        outDir,
		ChangedFiles:  withContents,
	})
	if err != nil {
		return p.fail(err)
	}
	for _, note := range bundle.Notes {
		p.logf("bundle: %s", note)
	}
	p.logf("bundle %s: %d bytes, sha256 %s", bundle.Path, bundle.Size, bundle.SHA256)

	if p.args.DryRun {
		return p.printPlan(server, res, staged, bundle)
	}
	return p.upload(ctx, client, server, staged, bundle)
}

// connect resolves the server and the client. A dry run needs no login and
// reaches no server, so it gets neither.
func (p *publisher) connect() (string, *publish.Client, error) {
	if p.args.DryRun {
		return resolveServer(""), nil, nil
	}
	server, token, err := p.env.credential(p.logOut, p.logOut)
	if err != nil {
		p.rec.State, p.rec.Error = state.StateFailed, "not logged in: "+redact.Text(err.Error())
		p.save()
		p.logf("login failed: %v", err)
		return "", nil, &exitError{code: exitNotLoggedIn, err: fmt.Errorf("not logged in: %w", err)}
	}
	client, err := p.env.newClient(server, token)
	if err != nil {
		return "", nil, p.fail(err)
	}
	p.client = client
	return server, client, nil
}

// upload sends the bundle, then does everything that follows a published
// bundle: cursors, comment, notification and, with --wait, the analysis.
func (p *publisher) upload(ctx context.Context, client *publish.Client, server string, staged *capture.Staged, bundle *publish.Bundle) error {
	p.step(state.StepUpload)
	revision, err := publish.Revision(server, p.rec.HeadSHA, bundle.SHA256, bundle.Size)
	if err != nil {
		return p.fail(err)
	}
	sent, err := client.Send(ctx, bundle.Path, publish.Metadata{
		Owner: p.owner, Repo: p.repo, PullNumber: p.number, HeadSHA: p.rec.HeadSHA,
		SHA256: bundle.SHA256, Size: bundle.Size, Revision: revision,
	}, publish.Events{
		Reserved: func(id string) {
			p.rec.PublicationID = id
			p.save()
		},
		Finalizing: func() { p.step(state.StepFinalize) },
	})
	if err != nil {
		return p.fail(p.withAuthorizationLink(err))
	}

	fin := sent.Finalized
	p.rec.PublicationID = fin.PublicationID
	p.rec.AnalysisJobID, p.rec.AnalysisState = fin.AnalysisJobID, fin.AnalysisState
	p.rec.State, p.rec.Error = state.StateUploaded, ""
	if fin.AnalysisJobID != "" {
		p.rec.State, p.rec.Step = state.StateProcessing, state.StepAnalysis
	}
	p.claimLatest()
	p.logf("published revision %d as %s (content version %d)", revision, fin.PublicationID, fin.ContentVersion)

	p.commitCursors(staged, bundle)
	p.comment()
	p.env.notify(fmt.Sprintf("PR #%d uploaded to Pair", p.number), p.rec.ReviewURL)
	p.logf("review: %s", p.rec.ReviewURL)

	p.uploaded, p.revision = true, revision
	if p.args.Wait {
		return nil // publishRun follows once it has released the PR's lock
	}
	return p.printResult(revision)
}

// claimLatest makes this run's upload the PR's newest, which ends any older
// run still following its review, and saves the publication record in the same
// step so that an older run cannot write over it afterwards.
func (p *publisher) claimLatest() {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		p.save()
		return
	}
	p.token = hex.EncodeToString(raw[:])
	err := state.ClaimLatest(p.key(), state.Latest{Token: p.token, PublicationID: p.rec.PublicationID, HeadSHA: p.rec.HeadSHA}, p.save)
	if err != nil {
		p.token = ""
		p.detailf("could not record the newest upload: %v", err)
	}
}

// withAuthorizationLink adds where to authorize the Pair GitHub App when Pair
// refused the publication for that reason. The link is built from the PR's
// owner, repo and number, never from the response: pair.sh's PR page is where
// the user is prompted to authorize.
func (p *publisher) withAuthorizationLink(err error) error {
	var apiErr *publish.Error
	if !errors.As(err, &apiErr) || !apiErr.AuthorizationRequired() {
		return err
	}
	return fmt.Errorf("%w; authorize the Pair GitHub App for %s/%s at %s, then publish again",
		err, p.owner, p.repo, state.ReviewURL(p.owner, p.repo, p.number))
}

// commitCursors advances the capture cursors now that the bundle is stored. A
// failure only means the next publication repeats some bytes.
func (p *publisher) commitCursors(staged *capture.Staged, bundle *publish.Bundle) {
	if staged == nil || staged.Empty() {
		return
	}
	if !bundle.RawIncluded {
		p.logf("capture: raw sessions were left out, so their cursors stay where they were")
		return
	}
	if err := staged.Commit(); err != nil {
		p.logf("capture: could not advance cursors: %v", err)
	}
}

// comment posts the Pair link on the PR. It is a convenience: a failure is
// logged and nothing more.
func (p *publisher) comment() {
	if p.args.NoComment {
		return
	}
	result, err := prcomment.Upsert(p.env.runner, p.owner, p.repo, p.number, p.rec.ReviewURL)
	if err != nil {
		p.logf("PR comment skipped: %v", err)
		return
	}
	p.logf("PR comment %s", result)
}

// finish ends a run once its last upload is stored and the PR's lock is
// released: with --wait it follows the review until it is ready.
func (p *publisher) finish(ctx context.Context, runErr error) error {
	defer p.closeLog()
	if runErr != nil || !p.uploaded || !p.args.Wait {
		return runErr
	}
	switch p.waitAnalysis(ctx) {
	case waitInterrupted:
		return errors.New("interrupted while waiting for the review")
	case waitFinished:
		p.reviewReady()
	}
	return p.printResult(p.revision)
}

// waitOutcome is how following the analysis ended.
type waitOutcome int

const (
	// waitFinished: nothing more to wait for. The analysis succeeded or
	// failed, there is none, the wait timed out or Pair could not be asked.
	// The publisher is told the same thing for all of them.
	waitFinished waitOutcome = iota
	// waitSuperseded: a newer upload of the PR took over.
	waitSuperseded
	// waitInterrupted: the process was told to stop.
	waitInterrupted
)

// waitAnalysis polls the analysis job until it finishes, --timeout passes or a
// newer upload of the PR takes over. Nothing it learns reaches the terminal or
// a notification: the details go to the run's log file only.
func (p *publisher) waitAnalysis(ctx context.Context) waitOutcome {
	wctx, cancel := context.WithTimeout(ctx, p.args.Timeout)
	defer cancel()
	p.logf("waiting for the review to be ready (up to %s)", p.args.Timeout)

	interval := pollStart
	for {
		if !state.IsLatest(p.key(), p.token) {
			p.detailf("a newer publish of this PR took over; no longer following this one")
			return waitSuperseded
		}
		an, err := p.client.AnalysisStatus(wctx, p.rec.PublicationID)
		var apiErr *publish.Error
		switch {
		case errors.Is(err, publish.ErrNoAnalysis):
			p.detailf("the server reports no analysis job for this publication")
			return waitFinished
		case wctx.Err() != nil:
			return p.waitEnded(ctx)
		case errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && p.rec.AnalysisJobID == "":
			// Finalize named no analysis job and the server has no such
			// route: an older backend that stores the evidence and nothing more.
			p.detailf("the server has no analysis for this publication")
			return waitFinished
		case errors.As(err, &apiErr) && apiErr.Transient():
			p.detailf("analysis status unavailable (%v); retrying", err)
		case err != nil:
			p.detailf("analysis status could not be read: %v", redact.Text(err.Error()))
			return waitFinished
		default:
			changed := an.JobID != p.rec.AnalysisJobID || an.State != p.rec.AnalysisState
			p.rec.AnalysisJobID, p.rec.AnalysisState = an.JobID, an.State
			if an.Done() {
				p.logAnalysisEnd(an)
				return waitFinished
			}
			if changed {
				state.WithLatest(p.key(), p.token, p.save)
			}
		}
		if err := p.env.sleep(wctx, interval); err != nil {
			return p.waitEnded(ctx)
		}
		if interval = interval * 3 / 2; interval > pollMax {
			interval = pollMax
		}
	}
}

// waitEnded says why the wait's context ended: the process was interrupted, or
// --timeout passed.
func (p *publisher) waitEnded(ctx context.Context) waitOutcome {
	if ctx.Err() != nil {
		return waitInterrupted
	}
	p.detailf("the analysis had not finished after %s", p.args.Timeout)
	return waitFinished
}

// logAnalysisEnd records how the analysis ended, with the error code of a
// failed one, in the log file.
func (p *publisher) logAnalysisEnd(an *publish.Analysis) {
	if an.State == publish.AnalysisSucceeded {
		p.detailf("analysis succeeded")
		return
	}
	p.detailf("analysis failed (%s)", analysisErrorCode(an))
}

// analysisErrorCode is the code a failed analysis carries, or "unknown".
func analysisErrorCode(an *publish.Analysis) string {
	if an.ErrorCode != nil && *an.ErrorCode != "" {
		return *an.ErrorCode
	}
	return "unknown"
}

// reviewReady marks the review ready and sends the one notification that says
// so, unless a newer upload of the PR took over in the meantime: that run then
// tells the user itself. Marking and the decision share one critical section
// with state.ClaimLatest, so two runs cannot both notify for the same upload.
func (p *publisher) reviewReady() {
	ready := state.WithLatest(p.key(), p.token, func() {
		p.rec.State, p.rec.Error = state.StateReady, ""
		p.save()
	})
	if !ready {
		p.detailf("a newer publish of this PR took over; no longer following this one")
		return
	}
	p.env.notify("Pair review ready", p.rec.ReviewURL)
	p.logf("review ready: %s", p.rec.ReviewURL)
}

// publishSummary is the JSON a run prints.
type publishSummary struct {
	PRURL         string         `json:"pr_url"`
	ReviewURL     string         `json:"review_url"`
	HeadSHA       string         `json:"head_sha"`
	State         string         `json:"state"`
	DryRun        bool           `json:"dry_run"`
	PublicationID string         `json:"publication_id,omitempty"`
	AnalysisJobID string         `json:"analysis_job_id,omitempty"`
	AnalysisState string         `json:"analysis_state,omitempty"`
	Revision      int64          `json:"revision,omitempty"`
	Server        string         `json:"server,omitempty"`
	Bundle        *bundleSummary `json:"bundle,omitempty"`
}

type bundleSummary struct {
	Path         string              `json:"path"`
	SizeBytes    int64               `json:"size_bytes"`
	SHA256       string              `json:"sha256"`
	Entries      int                 `json:"entries"`
	RawFiles     int                 `json:"raw_files"`
	RawIncluded  bool                `json:"raw_included"`
	Redactions   map[string]int      `json:"redactions"`
	Notes        []string            `json:"notes,omitempty"`
	ChangedFiles *filecontent.Totals `json:"changed_files,omitempty"`
}

func summarize(b *publish.Bundle, staged *capture.Staged, changed *filecontent.Totals) *bundleSummary {
	s := &bundleSummary{
		Path: b.Path, SizeBytes: b.Size, SHA256: b.SHA256, Entries: len(b.Entries),
		RawIncluded: b.RawIncluded, Redactions: b.Counts, Notes: b.Notes,
		ChangedFiles: changed,
	}
	if b.RawIncluded && staged != nil {
		s.RawFiles = len(staged.Files)
	}
	return s
}

func (p *publisher) printJSON(s publishSummary) error {
	enc := json.NewEncoder(p.env.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// printPlan reports what a real run would publish.
func (p *publisher) printPlan(server string, res *scan.Result, staged *capture.Staged, bundle *publish.Bundle) error {
	s := publishSummary{
		PRURL: p.rec.PRURL, ReviewURL: p.rec.ReviewURL, HeadSHA: p.rec.HeadSHA,
		State: "dry-run", DryRun: true, Server: server, Bundle: summarize(bundle, staged, res.ChangedFiles),
	}
	if p.args.JSON {
		return p.printJSON(s)
	}
	w := p.env.stdout
	fmt.Fprintf(w, "paircli: dry run for %s\n", s.PRURL)
	fmt.Fprintf(w, "  head:        %s\n", s.HeadSHA)
	fmt.Fprintf(w, "  scan folder: %s\n", res.OutDir)
	fmt.Fprintf(w, "  bundle:      %s (%d bytes, %d entries, %d raw-session files)\n", bundle.Path, bundle.Size, len(bundle.Entries), s.Bundle.RawFiles)
	fmt.Fprintf(w, "  redactions:  %s\n", formatCounts(bundle.Counts))
	if c := s.Bundle.ChangedFiles; c != nil {
		fmt.Fprintf(w, "  changed:     %d files, %d sides (%d partial, %d skipped), %d bytes\n",
			c.Files, c.SidesWritten, c.SidesPartial, c.SidesSkipped, c.Bytes)
	}
	fmt.Fprintf(w, "  would publish to %s and link %s\n", server, s.ReviewURL)
	return nil
}

func (p *publisher) printResult(revision int64) error {
	if p.args.JSON {
		return p.printJSON(publishSummary{
			PRURL: p.rec.PRURL, ReviewURL: p.rec.ReviewURL, HeadSHA: p.rec.HeadSHA, State: p.rec.State,
			PublicationID: p.rec.PublicationID, AnalysisJobID: p.rec.AnalysisJobID, AnalysisState: p.rec.AnalysisState,
			Revision: revision,
		})
	}
	_, err := fmt.Fprintf(p.env.stdout, "paircli: published %s (%s)\n", p.rec.ReviewURL, stateLabel(p.rec))
	return err
}

func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, len(kinds))
	for i, kind := range kinds {
		parts[i] = fmt.Sprintf("%s=%d", kind, counts[kind])
	}
	return strings.Join(parts, " ")
}
