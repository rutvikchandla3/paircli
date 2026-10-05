// Package selfupdate downloads verified paircli releases from the public CDN.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// DefaultManifestURL is the public CDN entrypoint updated by the release job.
	DefaultManifestURL = "https://downloads.pair.sh/paircli/latest.json"
	maxBinarySize      = 100 * 1024 * 1024
)

// Artifact describes one platform-specific executable in the CDN manifest.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Manifest describes the latest paircli release available from the CDN.
type Manifest struct {
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

// Options configures a self-update invocation.
type Options struct {
	CurrentVersion string
	ManifestURL    string
	ExecutablePath string
	Platform       string
	CheckOnly      bool
	AllowInsecure  bool
}

// Result describes whether an update was available or installed.
type Result struct {
	CurrentVersion string
	LatestVersion  string
	Updated        bool
}

// Run checks the CDN manifest and atomically replaces the executable when newer.
func Run(ctx context.Context, options Options) (Result, error) {
	manifestURL := options.ManifestURL
	if manifestURL == "" {
		manifestURL = DefaultManifestURL
	}
	if err := validateURL(manifestURL, options.AllowInsecure); err != nil {
		return Result{}, fmt.Errorf("update manifest: %w", err)
	}

	manifest, err := fetchManifest(ctx, manifestURL)
	if err != nil {
		return Result{}, err
	}
	if manifest.Version == "" {
		return Result{}, fmt.Errorf("update manifest is missing version")
	}
	result := Result{CurrentVersion: options.CurrentVersion, LatestVersion: manifest.Version}
	if !isNewer(manifest.Version, options.CurrentVersion) {
		return result, nil
	}
	if options.CheckOnly {
		return result, nil
	}

	platform := options.Platform
	if platform == "" {
		platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	artifact, ok := manifest.Artifacts[platform]
	if !ok {
		return Result{}, fmt.Errorf("paircli %s is not available for %s", manifest.Version, platform)
	}
	if err := validateArtifact(artifact, options.AllowInsecure); err != nil {
		return Result{}, err
	}

	executable := options.ExecutablePath
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("locate current executable: %w", err)
		}
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	if err := downloadAndReplace(ctx, artifact, executable); err != nil {
		return Result{}, err
	}
	result.Updated = true
	return result, nil
}

func fetchManifest(ctx context.Context, manifestURL string) (Manifest, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("create update manifest request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Manifest{}, fmt.Errorf("fetch update manifest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("fetch update manifest: unexpected HTTP %d", response.StatusCode)
	}
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBinarySize)).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode update manifest: %w", err)
	}
	return manifest, nil
}

func validateArtifact(artifact Artifact, allowInsecure bool) error {
	if err := validateURL(artifact.URL, allowInsecure); err != nil {
		return fmt.Errorf("update binary: %w", err)
	}
	if len(artifact.SHA256) != sha256.Size*2 {
		return fmt.Errorf("update binary has an invalid SHA-256")
	}
	if _, err := hex.DecodeString(artifact.SHA256); err != nil {
		return fmt.Errorf("update binary has an invalid SHA-256")
	}
	return nil
}

func validateURL(value string, allowInsecure bool) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if allowInsecure && parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1") {
		return nil
	}
	return fmt.Errorf("must use HTTPS")
}

func downloadAndReplace(ctx context.Context, artifact Artifact, executable string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("create update download request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: unexpected HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBinarySize {
		return fmt.Errorf("download update: binary exceeds maximum size")
	}

	temporary, err := os.CreateTemp(filepath.Dir(executable), ".paircli-update-*")
	if err != nil {
		return fmt.Errorf("create update file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	hash := sha256.New()
	limited := io.LimitReader(response.Body, maxBinarySize+1)
	bytesWritten, copyErr := io.Copy(io.MultiWriter(temporary, hash), limited)
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("write update: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close update: %w", closeErr)
	}
	if bytesWritten > maxBinarySize {
		return fmt.Errorf("download update: binary exceeds maximum size")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return fmt.Errorf("download update: checksum verification failed")
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return fmt.Errorf("mark update executable: %w", err)
	}
	if err := os.Rename(temporaryPath, executable); err != nil {
		return fmt.Errorf("install update: %w", err)
	}
	return nil
}

func isNewer(candidate, current string) bool {
	if candidate == current {
		return false
	}
	candidateVersion, candidatePrerelease, candidateOK := parseVersion(candidate)
	currentVersion, currentPrerelease, currentOK := parseVersion(current)
	if !candidateOK || !currentOK {
		return candidate != current
	}
	for index := range candidateVersion {
		if candidateVersion[index] != currentVersion[index] {
			return candidateVersion[index] > currentVersion[index]
		}
	}
	if candidatePrerelease == currentPrerelease {
		return false
	}
	if candidatePrerelease == "" {
		return true
	}
	if currentPrerelease == "" {
		return false
	}
	return candidatePrerelease > currentPrerelease
}

func parseVersion(value string) ([3]int, string, bool) {
	var parsed [3]int
	trimmed := strings.TrimPrefix(value, "v")
	parts := strings.SplitN(trimmed, "-", 2)
	segments := strings.Split(parts[0], ".")
	if len(segments) != len(parsed) {
		return parsed, "", false
	}
	for index, segment := range segments {
		number, err := strconv.Atoi(segment)
		if err != nil || number < 0 {
			return parsed, "", false
		}
		parsed[index] = number
	}
	if len(parts) == 2 {
		return parsed, parts[1], parts[1] != ""
	}
	return parsed, "", true
}
