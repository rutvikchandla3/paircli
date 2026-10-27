package main

import (
	"errors"
	"io"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/deviceauth"
)

// ensureLogin returns the credential for server, running the device login
// first when there is none. A PAIRCLI_AUTH_TOKEN credential carries no server
// and is accepted for any.
func ensureLogin(server string, out, progress io.Writer) (deviceauth.StoredAuth, error) {
	server = strings.TrimRight(server, "/")
	if a, ok := deviceauth.Resolve(); ok && (a.ServerURL == "" || strings.TrimRight(a.ServerURL, "/") == server) {
		return a, nil
	}
	if err := loginRun([]string{"--server", server}, out, progress); err != nil {
		return deviceauth.StoredAuth{}, err
	}
	a, ok := deviceauth.Resolve()
	if !ok {
		return deviceauth.StoredAuth{}, errors.New("login completed without a usable credential")
	}
	return a, nil
}
