package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReplacesBinaryWithVerifiedNewerRelease(t *testing.T) {
	t.Parallel()
	binary := []byte("new paircli binary")
	checksum := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest.json":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"schema":"paircli-release/v1","version":"0.2.0","minimum_version":"0.1.0","revoked":[],"artifacts":{"darwin-arm64":{"url":%q,"sha256":%q}}}`,
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
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"schema":"paircli-release/v1","version":"0.2.0","minimum_version":"0.1.0","revoked":[],"artifacts":{"darwin-arm64":{"url":%q,"sha256":"%s"}}}`,
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

func TestRunRejectsNonJSONManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte("<html>not a release manifest</html>"))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		ManifestURL:    server.URL + "/latest.json",
		AllowInsecure:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "expected application/json") {
		t.Fatalf("Run() error = %v, want content-type failure", err)
	}
}

func TestRunRejectsIncompatiblePlugin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"paircli-release/v1","version":"0.2.0","minimum_version":"0.1.0","min_plugin_version":"0.2.0","revoked":[],"artifacts":{}}`))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		ManifestURL:    server.URL + "/latest.json",
		PluginVersion:  "0.1.0",
		AllowInsecure:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "requires Pair plugin 0.2.0") {
		t.Fatalf("Run() error = %v, want plugin compatibility failure", err)
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
		{"0.2.0-rc.10", "0.2.0-rc.9", true},
		{"0.2.0-rc.9", "0.2.0-rc.10", false},
	}
	for _, test := range tests {
		if got := isNewer(test.candidate, test.current); got != test.want {
			t.Errorf("isNewer(%q, %q) = %t, want %t", test.candidate, test.current, got, test.want)
		}
	}
}

func TestRunSkipsDevelopmentBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"paircli-release/v1","version":"0.2.0","minimum_version":"0.1.0","revoked":[],"artifacts":{}}`))
	}))
	defer server.Close()

	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.2.0-dev",
		ManifestURL:    server.URL + "/latest.json",
		AllowInsecure:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedReason == "" {
		t.Fatal("development build was not skipped")
	}
}

func TestRunRejectsRevokedRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"paircli-release/v1","version":"0.2.0","minimum_version":"0.1.0","revoked":["0.2.0"],"artifacts":{}}`))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{CurrentVersion: "0.1.0", ManifestURL: server.URL + "/latest.json", AllowInsecure: true})
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("Run() error = %v, want revoked release failure", err)
	}
}

func TestRunRejectsHomebrewManagedBinary(t *testing.T) {
	checksum := sha256.Sum256([]byte("new paircli binary"))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest.json":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"schema":"paircli-release/v1","version":"0.2.0","minimum_version":"0.1.0","revoked":[],"artifacts":{"darwin-arm64":{"url":%q,"sha256":%q}}}`,
				serverURL(request)+"/paircli", hex.EncodeToString(checksum[:]))
		case "/paircli":
			_, _ = writer.Write([]byte("new paircli binary"))
		}
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		ManifestURL:    server.URL + "/latest.json",
		ExecutablePath: "/opt/homebrew/Cellar/paircli/0.1.0/bin/paircli",
		Platform:       "darwin-arm64",
		AllowInsecure:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("Run() error = %v, want Homebrew failure", err)
	}
}

func TestIsAllowedReleaseRedirect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		origin string
		target string
		want   bool
	}{
		{
			name:   "same origin",
			origin: "https://github.com/rutvikchandla3/paircli/releases/latest/download/latest.json",
			target: "https://github.com/rutvikchandla3/paircli/releases/download/v0.1.0/latest.json",
			want:   true,
		},
		{
			name:   "github release asset",
			origin: "https://github.com/rutvikchandla3/paircli/releases/latest/download/latest.json",
			target: "https://release-assets.githubusercontent.com/github-production-release-asset/example",
			want:   true,
		},
		{
			name:   "unexpected host",
			origin: "https://github.com/rutvikchandla3/paircli/releases/latest/download/latest.json",
			target: "https://downloads.example.test/paircli/latest.json",
			want:   false,
		},
		{
			name:   "custom manifest cannot redirect",
			origin: "https://mirror.example.test/paircli/latest.json",
			target: "https://release-assets.githubusercontent.com/github-production-release-asset/example",
			want:   false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			origin, err := url.Parse(test.origin)
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(test.target)
			if err != nil {
				t.Fatal(err)
			}
			if got := isAllowedReleaseRedirect(origin, target); got != test.want {
				t.Fatalf("isAllowedReleaseRedirect(%q, %q) = %t, want %t", test.origin, test.target, got, test.want)
			}
		})
	}
}

func TestIsAcceptedManifestContentType(t *testing.T) {
	t.Parallel()
	origin, err := url.Parse("https://github.com/rutvikchandla3/paircli/releases/latest/download/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	releaseAsset, err := url.Parse("https://release-assets.githubusercontent.com/github-production-release-asset/example")
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := url.Parse("https://mirror.example.test/latest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !isAcceptedManifestContentType(origin, releaseAsset, "application/octet-stream") {
		t.Fatal("GitHub Release octet-stream manifest was rejected")
	}
	if isAcceptedManifestContentType(mirror, mirror, "application/octet-stream") {
		t.Fatal("custom octet-stream manifest was accepted")
	}
	if !isAcceptedManifestContentType(mirror, mirror, "application/json") {
		t.Fatal("JSON manifest was rejected")
	}
}

func TestParseVersionRejectsInvalidSemVer(t *testing.T) {
	for _, value := range []string{"0.2", "0.2.0-rc.01", "0.2.0-", "0.2.0+", "01.2.0"} {
		if _, _, ok := parseVersion(value); ok {
			t.Errorf("parseVersion(%q) succeeded, want failure", value)
		}
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
