package testkit

import (
	"fmt"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// PB builds a *model.PR for tests.
type PB struct {
	pr *model.PR
}

// PR starts a new PR builder. Defaults: URL "https://github.com/<repo>/pull/<n>",
// Title "Test PR", HeadRef "feat/test", BaseRef "main".
func PR(repo string, number int) *PB {
	return &PB{pr: &model.PR{
		Repo:    repo,
		Number:  number,
		URL:     fmt.Sprintf("https://github.com/%s/pull/%d", repo, number),
		Title:   "Test PR",
		HeadRef: "feat/test",
		BaseRef: "main",
	}}
}

// file returns the DiffFile for path, creating it with status "modified" if
// this is the first hunk touching it.
func (p *PB) file(path string) *model.DiffFile {
	for i := range p.pr.Files {
		if p.pr.Files[i].Path == path {
			return &p.pr.Files[i]
		}
	}
	p.pr.Files = append(p.pr.Files, model.DiffFile{Path: path, Status: model.StatusModified})
	return &p.pr.Files[len(p.pr.Files)-1]
}

// Add appends one hunk of added lines to path, numbered from startLine.
func (p *PB) Add(path string, startLine int, lines ...string) *PB {
	f := p.file(path)
	var dl []model.DiffLine
	no := startLine
	for _, l := range lines {
		dl = append(dl, model.DiffLine{Kind: model.LineAdd, Text: l, NewNo: no})
		no++
	}
	f.Hunks = append(f.Hunks, model.DiffHunk{NewStart: startLine, NewLines: len(lines), Lines: dl})
	return p
}

// Remove appends one hunk of deleted lines to path, numbered from oldStart.
func (p *PB) Remove(path string, oldStart int, lines ...string) *PB {
	f := p.file(path)
	var dl []model.DiffLine
	no := oldStart
	for _, l := range lines {
		dl = append(dl, model.DiffLine{Kind: model.LineDel, Text: l, OldNo: no})
		no++
	}
	f.Hunks = append(f.Hunks, model.DiffHunk{OldStart: oldStart, OldLines: len(lines), Lines: dl})
	return p
}

// Status sets path's FileStatus.
func (p *PB) Status(path string, st model.FileStatus) *PB {
	p.file(path).Status = st
	return p
}

// Commit adds a commit at hh:mm on 2026-09-27 with no files.
func (p *PB) Commit(sha, hhmm string) *PB {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic("testkit: Commit: bad time " + hhmm + ": " + err.Error())
	}
	ts := dayBase.Add(time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute)
	p.pr.Commits = append(p.pr.Commits, model.Commit{SHA: sha, Time: ts})
	return p
}

// Body sets the PR body text.
func (p *PB) Body(text string) *PB {
	p.pr.Body = text
	return p
}

// Build returns the built PR.
func (p *PB) Build() *model.PR {
	return p.pr
}
