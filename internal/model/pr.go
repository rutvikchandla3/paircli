package model

import "time"

// PR is the pull request under review.
type PR struct {
	Repo      string     `json:"repo"` // "owner/repo"
	Number    int        `json:"number"`
	URL       string     `json:"url"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	HeadRef   string     `json:"head_ref"`
	BaseRef   string     `json:"base_ref"`
	CreatedAt time.Time  `json:"created_at"`
	Commits   []Commit   `json:"commits"`
	Files     []DiffFile `json:"files"` // the PR's combined diff
}

// Commit is one PR commit. Files is its own patch, when fetched.
type Commit struct {
	SHA     string     `json:"sha"`
	Time    time.Time  `json:"time"`
	Message string     `json:"message"`
	Files   []DiffFile `json:"files,omitempty"`
}

// FileStatus is how a file changed in a diff.
type FileStatus string

const (
	StatusAdded    FileStatus = "added"
	StatusModified FileStatus = "modified"
	StatusDeleted  FileStatus = "deleted"
	StatusRenamed  FileStatus = "renamed"
)

// DiffFile is one file in a unified diff.
type DiffFile struct {
	Path    string     `json:"path"`               // new path ("" never; for deletions the old path)
	OldPath string     `json:"old_path,omitempty"` // set for renames
	Status  FileStatus `json:"status"`
	Binary  bool       `json:"binary,omitempty"`
	Hunks   []DiffHunk `json:"hunks,omitempty"`
}

// DiffHunk is one hunk of a DiffFile.
type DiffHunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Section  string     `json:"section,omitempty"` // text after the second @@
	Lines    []DiffLine `json:"lines"`
}

// Diff line kinds.
const (
	LineAdd = "add"
	LineDel = "del"
	LineCtx = "ctx"
)

// DiffLine is one line of a hunk. OldNo is 0 for added lines; NewNo is 0 for deleted lines.
type DiffLine struct {
	Kind  string `json:"kind"` // LineAdd | LineDel | LineCtx
	Text  string `json:"text"` // without the prefix character
	OldNo int    `json:"old_no,omitempty"`
	NewNo int    `json:"new_no,omitempty"`
}

// AddedLines returns every added line of the file, in order.
func (f *DiffFile) AddedLines() []DiffLine {
	var out []DiffLine
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineAdd {
				out = append(out, l)
			}
		}
	}
	return out
}

// RemovedLines returns every deleted line of the file, in order.
func (f *DiffFile) RemovedLines() []DiffLine {
	var out []DiffLine
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineDel {
				out = append(out, l)
			}
		}
	}
	return out
}

// File returns the PR file with the given repo-relative path, or nil.
func (p *PR) File(path string) *DiffFile {
	for i := range p.Files {
		if p.Files[i].Path == path {
			return &p.Files[i]
		}
	}
	return nil
}
