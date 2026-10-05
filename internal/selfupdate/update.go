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
	"time"
)

const (
	// DefaultManifestURL is the public CDN entrypoint updated by the release job.
	DefaultManifestURL = "https://downloads.pair.sh/paircli/latest.json"
	maxManifestSize    = 1 * 1024 * 1024
	maxBinarySize      = 100 * 1024 * 1024
	requestTimeout     = 30 * time.Second
	manifestSchema     = "paircli-release/v1"
)

// Artifact describes one platform-specific executable in the CDN manifest.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Manifest describes the latest paircli release available from the CDN.
type Manifest struct {
	Schema           string              `json:"schema"`
	Version          string              `json:"version"`
	MinimumVersion   string              `json:"minimum_version"`
	MinPluginVersion string              `json:"min_plugin_version"`
	Revoked          []string            `json:"revoked"`
	Artifacts        map[string]Artifact `json:"artifacts"`
}

// Options configures a self-update invocation.
type Options struct {
	CurrentVersion string
	ManifestURL    string
	ExecutablePath string
	Platform       string
	PluginVersion  string
	CheckOnly      bool
	AllowInsecure  bool
}

// Result describes whether an update was available or installed.
type Result struct {
	CurrentVersion string
	LatestVersion  string
	Updated        bool
	SkippedReason  string
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

	client := releaseHTTPClient()
	manifest, err := fetchManifest(ctx, client, manifestURL)
	if err != nil {
		return Result{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Result{}, err
	}
	if manifest.MinPluginVersion != "" && options.PluginVersion != "" && !versionAtLeast(options.PluginVersion, manifest.MinPluginVersion) {
		return Result{}, fmt.Errorf("paircli %s requires Pair plugin %s or newer", manifest.Version, manifest.MinPluginVersion)
	}
	result := Result{CurrentVersion: options.CurrentVersion, LatestVersion: manifest.Version}
	if isDevelopmentVersion(options.CurrentVersion) {
		result.SkippedReason = "development builds do not self-update"
		return result, nil
	}
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
	if isHomebrewManaged(executable) {
		return Result{}, fmt.Errorf("paircli is managed by Homebrew; update it with brew upgrade paircli")
	}
	if err := downloadAndReplace(ctx, client, artifact, executable); err != nil {
		return Result{}, err
	}
	result.Updated = true
	return result, nil
}

func releaseHTTPClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) == 0 {
				return nil
			}
			origin := previous[0].URL
			if request.URL.Scheme != origin.Scheme || request.URL.Host != origin.Host {
				return fmt.Errorf("release download redirected to a different origin")
			}
			return nil
		},
	}
}

func fetchManifest(ctx context.Context, client *http.Client, manifestURL string) (Manifest, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("create update manifest request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return Manifest{}, fmt.Errorf("fetch update manifest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("fetch update manifest: unexpected HTTP %d", response.StatusCode)
	}
	contentType := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	if contentType != "application/json" {
		return Manifest{}, fmt.Errorf("fetch update manifest: expected application/json, got %s", contentType)
	}
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(response.Body, maxManifestSize)).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode update manifest: %w", err)
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != manifestSchema {
		return fmt.Errorf("update manifest has unsupported schema %q", manifest.Schema)
	}
	if _, _, ok := parseVersion(manifest.Version); !ok {
		return fmt.Errorf("update manifest has invalid version %q", manifest.Version)
	}
	if manifest.MinimumVersion != "" {
		if _, _, ok := parseVersion(manifest.MinimumVersion); !ok {
			return fmt.Errorf("update manifest has invalid minimum_version %q", manifest.MinimumVersion)
		}
		if isNewer(manifest.MinimumVersion, manifest.Version) {
			return fmt.Errorf("update manifest minimum_version exceeds release version")
		}
	}
	for _, revoked := range manifest.Revoked {
		if revoked == manifest.Version {
			return fmt.Errorf("paircli release %s is revoked", manifest.Version)
		}
	}
	return nil
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

func downloadAndReplace(ctx context.Context, client *http.Client, artifact Artifact, executable string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("create update download request: %w", err)
	}
	response, err := client.Do(request)
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
	return compareVersions(candidate, current) > 0
}

func compareVersions(candidate, current string) int {
	candidateVersion, candidatePrerelease, candidateOK := parseVersion(candidate)
	currentVersion, currentPrerelease, currentOK := parseVersion(current)
	if !candidateOK || !currentOK {
		return 0
	}
	for index := range candidateVersion {
		if candidateVersion[index] != currentVersion[index] {
			if candidateVersion[index] > currentVersion[index] {
				return 1
			}
			return -1
		}
	}
	return comparePrerelease(candidatePrerelease, currentPrerelease)
}

func comparePrerelease(candidate, current string) int {
	if candidate == current {
		return 0
	}
	if candidate == "" {
		return 1
	}
	if current == "" {
		return -1
	}
	candidateParts := strings.Split(candidate, ".")
	currentParts := strings.Split(current, ".")
	for index := 0; index < len(candidateParts) && index < len(currentParts); index++ {
		candidatePart := candidateParts[index]
		currentPart := currentParts[index]
		candidateNumber, candidateIsNumber := numericIdentifier(candidatePart)
		currentNumber, currentIsNumber := numericIdentifier(currentPart)
		switch {
		case candidateIsNumber && currentIsNumber:
			if candidateNumber > currentNumber {
				return 1
			}
			if candidateNumber < currentNumber {
				return -1
			}
		case candidateIsNumber:
			return -1
		case currentIsNumber:
			return 1
		case candidatePart > currentPart:
			return 1
		case candidatePart < currentPart:
			return -1
		}
	}
	if len(candidateParts) > len(currentParts) {
		return 1
	}
	if len(candidateParts) < len(currentParts) {
		return -1
	}
	return 0
}

func versionAtLeast(actual, required string) bool {
	return actual == required || isNewer(actual, required)
}

func parseVersion(value string) ([3]int, string, bool) {
	var parsed [3]int
	trimmed := strings.TrimPrefix(value, "v")
	buildParts := strings.SplitN(trimmed, "+", 2)
	if len(buildParts) == 2 {
		if buildParts[1] == "" {
			return parsed, "", false
		}
		for _, identifier := range strings.Split(buildParts[1], ".") {
			if identifier == "" || !validPrereleaseIdentifier(identifier) {
				return parsed, "", false
			}
		}
	}
	trimmed = buildParts[0]
	parts := strings.SplitN(trimmed, "-", 2)
	segments := strings.Split(parts[0], ".")
	if len(segments) != len(parsed) {
		return parsed, "", false
	}
	for index, segment := range segments {
		if len(segment) > 1 && segment[0] == '0' {
			return parsed, "", false
		}
		number, err := strconv.Atoi(segment)
		if err != nil || number < 0 {
			return parsed, "", false
		}
		parsed[index] = number
	}
	if len(parts) == 2 {
		if parts[1] == "" {
			return parsed, "", false
		}
		for _, identifier := range strings.Split(parts[1], ".") {
			if identifier == "" || !validPrereleaseIdentifier(identifier) || (allDigits(identifier) && len(identifier) > 1 && identifier[0] == '0') {
				return parsed, "", false
			}
		}
		return parsed, parts[1], true
	}
	return parsed, "", true
}

func numericIdentifier(value string) (int, bool) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	return number, err == nil
}

func validPrereleaseIdentifier(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && character != '-' {
			return false
		}
	}
	return true
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func isDevelopmentVersion(value string) bool {
	_, prerelease, ok := parseVersion(value)
	if !ok {
		return false
	}
	for _, identifier := range strings.Split(prerelease, ".") {
		if identifier == "dev" {
			return true
		}
	}
	return false
}

func isHomebrewManaged(executable string) bool {
	cleaned := filepath.Clean(executable)
	return strings.HasPrefix(cleaned, "/opt/homebrew/Cellar/") || strings.HasPrefix(cleaned, "/usr/local/Cellar/")
}
