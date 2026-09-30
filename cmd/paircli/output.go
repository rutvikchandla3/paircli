package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// printResult reports one run's summary, and with jsonOut the whole report on
// stdout. The summary moves to stderr in that case, so stdout stays valid JSON
// for `paircli ... --json | jq`.
func printResult(res *scan.Result, jsonOut bool) error {
	cov := res.Report.Coverage
	summary := fmt.Sprintf("paircli: wrote %s — %d sessions, %s of changed lines explained, %d alerts",
		res.OutDir, cov.Sessions, engine.Pct(cov.LinesExplained, cov.LinesTotal), res.Alerts)
	if !jsonOut {
		fmt.Println(summary)
		return nil
	}
	fmt.Fprintln(os.Stderr, summary)
	out, err := json.MarshalIndent(res.Report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
