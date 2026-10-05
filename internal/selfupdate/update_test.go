package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunReplacesBinaryWithVerifiedNewerRelease(t *testing.T) {
	t.Parallel()
	binary := []byte("new paircli binary")
	checksum := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest.json":
			fmt.Fprintf(writer, `{"version":"0.2.0","artifacts":{"darwin-arm64":{"url":%q,"sha256":%q}}}`,
				serverURL(request)+"/paircli", hex.EncodeToString(checksum[:]))
		case "/paircli":
			_, _ = writer.Write(binary)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "paircli")
	if err := os.WriteFile(target, []byte("old paircli binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		ManifestURL:    server.URL + "/latest.json",
		ExecutablePath: target,
		Platform:       "darwin-arm64",
		AllowInsecure:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.LatestVersion != "0.2.0" {
		t.Fatalf("unexpected update result: %#v", result)
	}
	installed, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != string(binary) {
		t.Fatalf("installed binary = %q, want %q", installed, binary)
	}
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest.json":
			fmt.Fprintf(writer, `{"version":"0.2.0","artifacts":{"darwin-arm64":{"url":%q,"sha256":"%s"}}}`,
				serverURL(request)+"/paircli", "0000000000000000000000000000000000000000000000000000000000000000")
		case "/paircli":
			_, _ = writer.Write([]byte("unexpected"))
		}
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "paircli")
	if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		ManifestURL:    server.URL + "/latest.json",
		ExecutablePath: target,
		Platform:       "darwin-arm64",
		AllowInsecure:  true,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want checksum failure")
	}
	installed, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(installed) != "current" {
		t.Fatalf("binary changed after failed update: %q", installed)
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.2.0", "0.2.0-dev", true},
		{"0.2.0-dev", "0.2.0", false},
	}
	for _, test := range tests {
		if got := isNewer(test.candidate, test.current); got != test.want {
			t.Errorf("isNewer(%q, %q) = %t, want %t", test.candidate, test.current, got, test.want)
		}
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
