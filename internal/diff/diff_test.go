package diff

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
)

func TestParse_Modified(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"index 1111111..2222222 100644",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,3 +1,4 @@ func main() {",
		" package main",
		"-var x = 1",
		"+var x = 2",
		"+var y = 3",
		" ",
		"@@ -10,2 +11,2 @@",
		"-old tail",
		"+new tail",
		" end",
		"",
	}, "\n")

	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	f := files[0]
	if f.Path != "main.go" || f.Status != model.StatusModified {
		t.Fatalf("Path/Status = %q/%q, want main.go/modified", f.Path, f.Status)
	}
	if len(f.Hunks) != 2 {
		t.Fatalf("len(Hunks) = %d, want 2", len(f.Hunks))
	}

	h0 := f.Hunks[0]
	if h0.OldStart != 1 || h0.OldLines != 3 || h0.NewStart != 1 || h0.NewLines != 4 {
		t.Fatalf("hunk0 header = %+v", h0)
	}
	if h0.Section != "func main() {" {
		t.Fatalf("hunk0 Section = %q", h0.Section)
	}
	wantLines := []model.DiffLine{
		{Kind: model.LineCtx, Text: "package main", OldNo: 1, NewNo: 1},
		{Kind: model.LineDel, Text: "var x = 1", OldNo: 2},
		{Kind: model.LineAdd, Text: "var x = 2", NewNo: 2},
		{Kind: model.LineAdd, Text: "var y = 3", NewNo: 3},
		{Kind: model.LineCtx, Text: "", OldNo: 3, NewNo: 4},
	}
	if len(h0.Lines) != len(wantLines) {
		t.Fatalf("len(h0.Lines) = %d, want %d: %+v", len(h0.Lines), len(wantLines), h0.Lines)
	}
	for i, want := range wantLines {
		if h0.Lines[i] != want {
			t.Errorf("h0.Lines[%d] = %+v, want %+v", i, h0.Lines[i], want)
		}
	}

	h1 := f.Hunks[1]
	if h1.OldStart != 10 || h1.OldLines != 2 || h1.NewStart != 11 || h1.NewLines != 2 {
		t.Fatalf("hunk1 header = %+v", h1)
	}
	if h1.Section != "" {
		t.Fatalf("hunk1 Section = %q, want empty", h1.Section)
	}
	wantLines1 := []model.DiffLine{
		{Kind: model.LineDel, Text: "old tail", OldNo: 10},
		{Kind: model.LineAdd, Text: "new tail", NewNo: 11},
		{Kind: model.LineCtx, Text: "end", OldNo: 11, NewNo: 12},
	}
	for i, want := range wantLines1 {
		if h1.Lines[i] != want {
			t.Errorf("h1.Lines[%d] = %+v, want %+v", i, h1.Lines[i], want)
		}
	}
}

func TestParse_AddedDeleted(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/new.txt b/new.txt",
		"new file mode 100644",
		"index 0000000..1111111",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1,2 @@",
		"+line one",
		"+line two",
		"diff --git a/old.txt b/old.txt",
		"deleted file mode 100644",
		"index 2222222..0000000",
		"--- a/old.txt",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-gone one",
		"-gone two",
		"",
	}, "\n")

	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}

	added := files[0]
	if added.Status != model.StatusAdded || added.Path != "new.txt" {
		t.Errorf("added = %+v", added)
	}
	if len(added.Hunks) != 1 || added.Hunks[0].OldLines != 0 {
		t.Errorf("added hunks = %+v", added.Hunks)
	}

	deleted := files[1]
	if deleted.Status != model.StatusDeleted || deleted.Path != "old.txt" {
		t.Errorf("deleted = %+v, want Path to be the old path", deleted)
	}
}

func TestParse_Rename(t *testing.T) {
	// Rename with no content change.
	in1 := strings.Join([]string{
		"diff --git a/old.txt b/new.txt",
		"similarity index 100%",
		"rename from old.txt",
		"rename to new.txt",
		"",
	}, "\n")
	files, err := Parse(in1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	f := files[0]
	if f.Status != model.StatusRenamed || f.Path != "new.txt" || f.OldPath != "old.txt" {
		t.Errorf("no-content rename = %+v", f)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("expected no hunks, got %+v", f.Hunks)
	}

	// Rename with content change.
	in2 := strings.Join([]string{
		"diff --git a/old.txt b/new.txt",
		"similarity index 90%",
		"rename from old.txt",
		"rename to new.txt",
		"index 1111111..2222222 100644",
		"--- a/old.txt",
		"+++ b/new.txt",
		"@@ -1,2 +1,2 @@",
		"-hello",
		"+hello world",
		" world",
		"",
	}, "\n")
	files2, err := Parse(in2)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files2) != 1 {
		t.Fatalf("len(files2) = %d, want 1", len(files2))
	}
	f2 := files2[0]
	if f2.Status != model.StatusRenamed || f2.Path != "new.txt" || f2.OldPath != "old.txt" {
		t.Errorf("content rename = %+v", f2)
	}
	if len(f2.Hunks) != 1 || len(f2.Hunks[0].Lines) != 3 {
		t.Errorf("content rename hunks = %+v", f2.Hunks)
	}
}

func TestParse_Binary(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/image.png b/image.png",
		"index 1111111..2222222 100644",
		"Binary files a/image.png and b/image.png differ",
		"diff --git a/other.bin b/other.bin",
		"new file mode 100644",
		"index 0000000..3333333",
		"GIT binary patch",
		"literal 10",
		"Qc$@(Pf#Uz01^NI",
		"",
		"diff --git a/plain.txt b/plain.txt",
		"index 4444444..5555555 100644",
		"--- a/plain.txt",
		"+++ b/plain.txt",
		"@@ -1 +1 @@",
		"-a",
		"+b",
		"",
	}, "\n")

	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3: %+v", len(files), files)
	}
	if !files[0].Binary || len(files[0].Hunks) != 0 || files[0].Path != "image.png" {
		t.Errorf("files[0] = %+v", files[0])
	}
	if !files[1].Binary || len(files[1].Hunks) != 0 || files[1].Status != model.StatusAdded || files[1].Path != "other.bin" {
		t.Errorf("files[1] = %+v", files[1])
	}
	if files[2].Binary || len(files[2].Hunks) != 1 {
		t.Errorf("files[2] = %+v", files[2])
	}
}

func TestParse_NoNewlineMarker(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"index 1111111..2222222 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,1 +1,1 @@",
		"-old",
		"\\ No newline at end of file",
		"+new",
		"\\ No newline at end of file",
		"",
	}, "\n")

	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("files = %+v", files)
	}
	lines := files[0].Hunks[0].Lines
	if len(lines) != 2 {
		t.Fatalf("lines = %+v, want 2 (no-newline markers ignored)", lines)
	}
	if lines[0].Kind != model.LineDel || lines[0].Text != "old" {
		t.Errorf("lines[0] = %+v", lines[0])
	}
	if lines[1].Kind != model.LineAdd || lines[1].Text != "new" {
		t.Errorf("lines[1] = %+v", lines[1])
	}
}

func TestParse_QuotedPath(t *testing.T) {
	in := strings.Join([]string{
		`diff --git "a/my file.txt" "b/my file.txt"`,
		"index 1111111..2222222 100644",
		`--- "a/my file.txt"`,
		`+++ "b/my file.txt"`,
		"@@ -1,2 +1,2 @@",
		"-hello",
		"+hello world",
		" world",
		"",
	}, "\n")

	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if files[0].Path != "my file.txt" {
		t.Errorf("Path = %q, want %q", files[0].Path, "my file.txt")
	}
}

func TestParse_MissingCounts(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"index 1111111..2222222 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -3 +3 @@",
		"-x",
		"+y",
		"",
	}, "\n")

	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("files = %+v", files)
	}
	h := files[0].Hunks[0]
	if h.OldStart != 3 || h.OldLines != 1 || h.NewStart != 3 || h.NewLines != 1 {
		t.Errorf("hunk = %+v, want OldStart=3 OldLines=1 NewStart=3 NewLines=1", h)
	}
}

func TestParse_Empty(t *testing.T) {
	files, err := Parse("")
	if err != nil {
		t.Fatalf("Parse(\"\") err = %v, want nil", err)
	}
	if files != nil {
		t.Fatalf("Parse(\"\") files = %+v, want nil", files)
	}

	files2, err2 := Parse("   \n  \n")
	if err2 != nil || files2 != nil {
		t.Fatalf("Parse(whitespace) = %+v, %v, want nil, nil", files2, err2)
	}
}

func TestParse_Malformed(t *testing.T) {
	_, err := Parse("this is not a diff\njust some text\n")
	if err == nil {
		t.Fatal("Parse(garbage) err = nil, want non-nil")
	}
}
