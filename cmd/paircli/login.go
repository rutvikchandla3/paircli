package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/deviceauth"
)

// defaultServerURL is the backend the CLI logs into when neither --server nor
// PAIRCLI_SERVER_URL names one.
const defaultServerURL = "https://pair.hackerrank.com"

// serverURLEnv overrides defaultServerURL, below --server.
const serverURLEnv = "PAIRCLI_SERVER_URL"

// loginNewClient and loginOpenBrowser are the only ways `paircli login`
// touches the outside world beyond HTTP, so tests can run it offline.
var (
	loginNewClient   = deviceauth.NewClient
	loginOpenBrowser = deviceauth.OpenBrowser
)

// loginArgs is a parsed `paircli login` invocation.
type loginArgs struct {
	Server    string
	NoBrowser bool
	JSON      bool
	Timeout   time.Duration
}

// parseLoginArgs parses `login [flags]`.
func parseLoginArgs(args []string) (loginArgs, error) {
	a := loginArgs{Timeout: 15 * time.Minute}
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // runLogin reports parse errors itself
	fs.StringVar(&a.Server, "server", "", "backend base URL")
	fs.BoolVar(&a.NoBrowser, "no-browser", false, "do not open the browser")
	fs.BoolVar(&a.JSON, "json", false, "print the device code payload to stdout")
	fs.DurationVar(&a.Timeout, "timeout", 15*time.Minute, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return a, nil
}

// resolveServer applies the server precedence: --server, then
// PAIRCLI_SERVER_URL, then the built-in default.
func resolveServer(explicit string) string {
	if explicit != "" {
		return strings.TrimRight(explicit, "/")
	}
	if env := os.Getenv(serverURLEnv); env != "" {
		return strings.TrimRight(env, "/")
	}
	return defaultServerURL
}

// runLogin implements `paircli login`.
func runLogin(args []string) error {
	return loginRun(args, os.Stdout, os.Stderr)
}

// loginRun runs the device authorization flow, writing human output and the
// --json payload to out and progress to progress. Splitting the writers is
// what keeps `paircli login --json | jq` working.
func loginRun(args []string, out, progress io.Writer) error {
	a, err := parseLoginArgs(args)
	if err != nil {
		return err
	}
	server := resolveServer(a.Server)

	// Ctrl-C cancels the poll cleanly; the overall timeout bounds it too.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()

	client := loginNewClient(server)
	dc, err := client.RequestCode(ctx, "paircli "+version)
	if err != nil {
		return err
	}
	printDeviceCode(out, progress, dc, a.JSON)

	if !a.NoBrowser {
		if err := loginOpenBrowser(dc.VerificationURIComplete); err != nil {
			fmt.Fprintf(progress, "paircli: could not open the browser (%v); open %s yourself\n",
				err, dc.VerificationURIComplete)
		}
	}

	tok, err := client.PollToken(ctx, dc)
	if err != nil {
		var pe *deviceauth.PollError
		if errors.As(err, &pe) {
			switch pe.Kind {
			case deviceauth.PollDenied:
				return errors.New("login denied by user")
			case deviceauth.PollExpired:
				return errors.New("device code expired, run paircli login again")
			}
		}
		return err
	}

	// Save the credential before anything non-essential, so an interrupt
	// during the identity lookup cannot lose a token the user just approved.
	stored := deviceauth.StoredAuth{
		AccessToken: tok.AccessToken,
		TokenType:   tok.TokenType,
		ExpiresAt:   time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		ServerURL:   server,
	}
	path := deviceauth.DefaultFile()
	if err := deviceauth.Save(path, stored); err != nil {
		return err
	}

	// Best-effort: naming the account is a nicety, never a reason to fail a
	// login that already succeeded.
	user, meErr := client.Me(ctx, tok.AccessToken)
	display := user.DisplayName()
	if meErr == nil && display != "" {
		stored.User = display
		_ = deviceauth.Save(path, stored) // still non-fatal: only the label is lost
	}

	// With --json, "Logged in as" and the file path are progress, so stdout
	// stays the code payload.
	result := out
	if a.JSON {
		result = progress
	}
	if meErr != nil {
		fmt.Fprintf(result, "Logged in.\n")
	} else if display != "" {
		fmt.Fprintf(result, "Logged in as %s\n", display)
	} else {
		fmt.Fprintf(result, "Logged in.\n")
	}
	fmt.Fprintf(result, "Token saved to %s (0600, expires %s)\n",
		path, stored.ExpiresAt.UTC().Format("2006-01-02"))
	return nil
}

// printDeviceCode shows the one-time code and where to enter it. With --json
// the code payload is the stdout JSON and the human text moves to progress.
func printDeviceCode(out, progress io.Writer, dc deviceauth.DeviceCode, jsonOut bool) {
	if jsonOut {
		payload, err := json.MarshalIndent(map[string]any{
			"user_code":                 dc.UserCode,
			"verification_uri":          dc.VerificationURI,
			"verification_uri_complete": dc.VerificationURIComplete,
			"expires_in":                dc.ExpiresIn,
			"interval":                  dc.Interval,
		}, "", "  ")
		if err == nil {
			fmt.Fprintln(out, string(payload))
		}
		fmt.Fprintf(progress, "Enter code %s at %s\n", dc.UserCode, dc.VerificationURIComplete)
		return
	}
	fmt.Fprintf(out, "First copy your one-time code: %s\n", dc.UserCode)
	fmt.Fprintf(out, "Then visit: %s\n", dc.VerificationURIComplete)
	fmt.Fprintf(out, "Waiting for authorization (code expires in %s)...\n", (time.Duration(dc.ExpiresIn) * time.Second).String())
}
