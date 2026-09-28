package intent

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type int4 struct{}

func init() { engine.Register(int4{}) }

func (int4) ID() string { return "INT-4" }

func (d int4) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	// Collect instructions events
	instructions := c.Of(model.KindInstructions)

	if len(instructions) == 0 {
		sig.State = model.StateInfo
		sig.Summary = "No instruction files were recorded."
		sig.Data = map[string]any{"files": []any{}}
		return sig
	}

	// Deduplicate by (display path, Hash)
	type fileKey struct {
		displayPath string
		hash        string
	}

	seenFiles := make(map[fileKey]bool)
	var dedupeOrder []fileKey
	fileDataMap := make(map[fileKey]*map[string]any)

	for _, it := range instructions {
		i := it.E.Instructions

		// Determine display path
		displayPath := i.RelPath
		if displayPath == "" {
			displayPath = engine.Home(i.Path)
		}

		key := fileKey{displayPath, i.Hash}

		if !seenFiles[key] {
			seenFiles[key] = true
			dedupeOrder = append(dedupeOrder, key)

			// Create data entry
			fileData := map[string]any{
				"path":     displayPath,
				"scope":    i.Scope,
				"hash":     i.Hash,
				"sessions": []string{it.S.Ref()},
			}
			fileDataMap[key] = &fileData
		} else {
			// Add session to existing file entry
			sessions := (*fileDataMap[key])["sessions"].([]string)
			// Check if session already in list
			found := false
			for _, s := range sessions {
				if s == it.S.Ref() {
					found = true
					break
				}
			}
			if !found {
				sessions = append(sessions, it.S.Ref())
				(*fileDataMap[key])["sessions"] = sessions
			}
		}
	}

	// Process deduplicated files and create findings
	for _, it := range instructions {
		i := it.E.Instructions

		displayPath := i.RelPath
		if displayPath == "" {
			displayPath = engine.Home(i.Path)
		}

		key := fileKey{displayPath, i.Hash}

		// Only create finding for first occurrence of this (path, hash) pair
		if !seenFiles[key] {
			continue
		}
		seenFiles[key] = false // Mark as processed

		var findingSummary string
		if i.FirstLine != "" {
			findingSummary = fmt.Sprintf("Loaded `%s` (%s): %s",
				displayPath, i.Scope, model.Clip(i.FirstLine, 80))
		} else {
			findingSummary = fmt.Sprintf("Loaded `%s` (%s)",
				displayPath, i.Scope)
		}

		finding := model.Finding{
			Summary:  findingSummary,
			Severity: model.StateInfo,
			Evidence: []model.Evidence{c.Evidence(it, i.FirstLine)},
		}

		sig.Findings = append(sig.Findings, finding)
	}

	// Build file list for data
	var fileList []map[string]any
	for _, key := range dedupeOrder {
		fileList = append(fileList, *fileDataMap[key])
	}

	// Build signal summary
	fileCount := len(dedupeOrder)
	var displayPaths []string
	for i, key := range dedupeOrder {
		if i >= 3 {
			break
		}
		displayPaths = append(displayPaths, key.displayPath)
	}

	summaryParts := []string{
		engine.Plural(fileCount, "instruction file", "instruction files"),
		"in effect:",
		strings.Join(displayPaths, ", "),
	}
	if fileCount > 3 {
		summaryParts = append(summaryParts, ", …")
	}
	summaryText := strings.Join(summaryParts, " ") + "."

	sig.State = model.StateInfo
	sig.Summary = summaryText
	sig.Data = map[string]any{"files": fileList}

	return sig
}
