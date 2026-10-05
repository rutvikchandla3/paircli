package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/rutvikchandla3/paircli/internal/deviceauth"
)

// logoutArgs is a parsed `paircli logout` invocation.
type logoutArgs struct {
	JSON bool
}

// parseLogoutArgs parses `logout [flags]`.
func parseLogoutArgs(args []string) (logoutArgs, error) {
	var a logoutArgs
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&a.JSON, "json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return a, nil
}

// runLogout implements `paircli logout`. It is idempotent: removing a token
// that is not there is reported, not treated as a failure.
func runLogout(args []string) error {
	return logoutRun(args, os.Stdout, os.Stderr)
}

func logoutRun(args []string, out, progress io.Writer) error {
	a, err := parseLogoutArgs(args)
	if err != nil {
		return err
	}

	path := deviceauth.DefaultFile()
	_, statErr := os.Stat(path)
	removed := statErr == nil
	if err := deviceauth.Remove(path); err != nil {
		return err
	}

	envSet := os.Getenv(deviceauth.EnvToken) != ""

	if a.JSON {
		payload, err := json.MarshalIndent(map[string]any{
			"removed":       removed,
			"path":          path,
			"env_token_set": envSet,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(payload))
	} else if removed {
		fmt.Fprintf(out, "Logged out (removed %s)\n", path)
	} else {
		fmt.Fprintf(out, "Already logged out (no token at %s)\n", path)
	}

	if envSet {
		fmt.Fprintf(progress, "paircli: %s is set and still takes precedence; unset it to fully log out\n",
			deviceauth.EnvToken)
	}
	return nil
}
