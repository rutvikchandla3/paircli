// Package hooklog is the shared hook record log: paircli hook handlers
// (internal/claudecode, internal/codex; the Pi extension writes its own
// records into the transcript instead, see internal/pi.HookRecords) append
// one model.HookRecord per line to ~/.paircli/events/<harness>/<date>.jsonl.
// internal/link reads that log and merges it into sessions as OriginHook
// events before internal/engine runs.
package hooklog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// eventsDirEnv overrides DefaultDir, used by tests so they never touch the
// real ~/.paircli/events.
const eventsDirEnv = "PAIRCLI_EVENTS_DIR"

// DefaultDir returns $PAIRCLI_EVENTS_DIR if set, else ~/.paircli/events.
func DefaultDir() string {
	if d := os.Getenv(eventsDirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paircli", "events")
}

// Append writes one HookRecord as a JSON line to
// dir/<harness>/<rec.TS UTC as 2006-01-02>.jsonl, creating directories as
// needed. rec.V is set to model.HookRecordVersion when zero.
func Append(dir string, rec model.HookRecord) error {
	if rec.V == 0 {
		rec.V = model.HookRecordVersion
	}

	harnessDir := filepath.Join(dir, string(rec.Harness))
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(harnessDir, rec.TS.UTC().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = f.Write(line)
	return err
}

// Read reads every dir/<h>/*.jsonl file whose date is not before since's UTC
// date, decodes each line (skipping malformed ones), drops records with an
// empty SessionID or a TS before since (zero since = all), and groups the
// rest by SessionID, each group sorted by TS then Event.
func Read(dir string, h model.Harness, since time.Time) (map[string][]model.HookRecord, error) {
	harnessDir := filepath.Join(dir, string(h))
	files, err := filepath.Glob(filepath.Join(harnessDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	sinceUTC := since.UTC()
	sinceDate := sinceUTC.Format("2006-01-02")

	out := map[string][]model.HookRecord{}
	for _, path := range files {
		date := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !since.IsZero() && date < sinceDate {
			continue
		}
		if err := readFile(path, sinceUTC, since.IsZero(), out); err != nil {
			continue // best-effort: skip unreadable files
		}
	}

	for id, recs := range out {
		sort.SliceStable(recs, func(i, j int) bool {
			if !recs[i].TS.Equal(recs[j].TS) {
				return recs[i].TS.Before(recs[j].TS)
			}
			return recs[i].Event < recs[j].Event
		})
		out[id] = recs
	}
	return out, nil
}

func readFile(path string, since time.Time, allTime bool, out map[string][]model.HookRecord) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec model.HookRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.SessionID == "" {
			continue
		}
		if !allTime && rec.TS.Before(since) {
			continue
		}
		out[rec.SessionID] = append(out[rec.SessionID], rec)
	}
	return scanner.Err()
}
