package verification

import (
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type ver6 struct{}

func init() { engine.Register(ver6{}) }

func (ver6) ID() string { return "VER-6" }

func (d ver6) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}

	sig := engine.NewSignal(d.ID())

	var findings []model.Finding
	var images, devServers, localRequests int
	var openErrors []map[string]any

	// Track latest diagnostics per PR file
	diagnosticsPerFile := make(map[string]*engine.Item)

	// Process timeline items
	for _, it := range c.Timeline {
		e := it.E

		// Image events
		if e.Kind == model.KindImage && e.Image != nil {
			images++
			text := "Screenshot captured at " + engine.Clock(e.TS)
			if e.Image.Tool != "" {
				text += " via `" + e.Image.Tool + "`"
			}
			text += "."

			finding := model.Finding{
				Summary:  text,
				Severity: model.StateInfo,
				Evidence: []model.Evidence{c.Evidence(it, "")},
				Data:     map[string]any{"ref": e.Image.Ref},
			}
			findings = append(findings, finding)
		}

		// Dev server and local requests
		if e.Kind == model.KindCommand && e.Command != nil {
			classes := classify.Command(e.Command.Cmd, c.Config.ExtraChecks)
			for _, cls := range classes {
				if cls == classify.DevServer {
					devServers++
					text := "Dev server started: `" + classify.ShortCmd(e.Command.Cmd) + "` at " + engine.Clock(e.TS) + "."
					finding := model.Finding{
						Summary:  text,
						Severity: model.StateInfo,
						Evidence: []model.Evidence{c.Evidence(it, e.Command.Cmd)},
					}
					findings = append(findings, finding)
					break
				}
				if cls == classify.LocalHTTP {
					localRequests++
					status := "unknown"
					if e.Command.Status != "" {
						status = string(e.Command.Status)
					}
					text := "Local request: `" + classify.ShortCmd(e.Command.Cmd) + "` (" + status + ") at " + engine.Clock(e.TS) + "."
					finding := model.Finding{
						Summary:  text,
						Severity: model.StateInfo,
						Evidence: []model.Evidence{c.Evidence(it, e.Command.Cmd)},
					}
					findings = append(findings, finding)
					break
				}
			}
		}

		// Track diagnostics per file
		if e.Kind == model.KindDiagnostics && e.Diagnostics != nil {
			relPath := e.Diagnostics.RelPath
			if relPath != "" && c.IsPRFile(relPath) {
				diagnosticsPerFile[relPath] = &it
			}
		}
	}

	// Process open errors (latest diagnostics per PR file with errors).
	//
	// The files are visited in sorted order rather than by ranging the map
	// directly: Go randomizes map iteration, which would order both the
	// findings and Data["open_errors"] differently on every run and break the
	// guarantee that identical input produces byte-identical output. The bug
	// only shows up with two or more files carrying errors, which is why a
	// one-file fixture never caught it.
	files := make([]string, 0, len(diagnosticsPerFile))
	for file := range diagnosticsPerFile {
		files = append(files, file)
	}
	sort.Strings(files)

	for _, file := range files {
		itemPtr := diagnosticsPerFile[file]
		diag := itemPtr.E.Diagnostics
		if diag.Errors > 0 {
			openErrors = append(openErrors, map[string]any{"file": file, "errors": diag.Errors})

			errMsg := ""
			if len(diag.Messages) > 0 {
				errMsg = diag.Messages[0]
			}
			if errMsg == "" {
				errMsg = "error"
			}

			text := "`" + file + "` still had " + engine.Plural(diag.Errors, "error", "errors") + " at " + engine.Clock(itemPtr.E.TS) + ": " + model.Clip(errMsg, 80)

			finding := model.Finding{
				Summary:  text,
				Severity: model.StateAlert,
				Anchors: []model.Anchor{
					{File: file},
				},
				Evidence: []model.Evidence{c.Evidence(*itemPtr, errMsg)},
			}
			findings = append(findings, finding)
		}
	}

	// Set summary and state
	if len(openErrors) > 0 {
		sig.State = model.StateAlert
		sig.Summary = engine.Plural(len(openErrors), "PR file", "PR files") + " still had editor errors at the end of the session."
	} else if images > 0 || devServers > 0 || localRequests > 0 {
		sig.State = model.StateInfo
		parts := []string{}
		if images > 0 {
			parts = append(parts, engine.Plural(images, "screenshot", "screenshots"))
		}
		if devServers > 0 {
			parts = append(parts, engine.Plural(devServers, "dev server run", "dev server runs"))
		}
		if localRequests > 0 {
			parts = append(parts, engine.Plural(localRequests, "local request", "local requests"))
		}
		sig.Summary = strings.Join(parts, ", ") + " captured."
	} else {
		sig.State = model.StateInfo
		sig.Summary = "No screenshots, dev servers or local requests were captured."
	}

	sig.Findings = findings
	sig.Data = map[string]any{
		"images":         images,
		"dev_servers":    devServers,
		"local_requests": localRequests,
		"open_errors":    openErrors,
	}

	return sig
}
