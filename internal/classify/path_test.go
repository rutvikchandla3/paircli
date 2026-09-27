package classify

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
)

func TestPath(t *testing.T) {
	cfg := config.Default()

	tests := []struct {
		name string
		path string
		want []PathClass
	}{
		{
			name: "test file typescript",
			path: "src/main.test.ts",
			want: []PathClass{TestFile},
		},
		{
			name: "test directory",
			path: "test/unit/utils.ts",
			want: []PathClass{TestFile},
		},
		{
			name: "go test file",
			path: "pkg/foo_test.go",
			want: []PathClass{TestFile},
		},
		{
			name: "CI config github",
			path: ".github/workflows/test.yml",
			want: []PathClass{CIConfig, Sensitive},
		},
		{
			name: "manifest package.json",
			path: "package.json",
			want: []PathClass{Manifest, Sensitive},
		},
		{
			name: "lockfile",
			path: "package-lock.json",
			want: []PathClass{Lockfile, Generated},
		},
		{
			name: "secret env file",
			path: ".env",
			want: []PathClass{SecretFile, Sensitive},
		},
		{
			name: "env example not secret",
			path: ".env.example",
			want: []PathClass{Sensitive},
		},
		{
			name: "snapshot",
			path: "src/__snapshots__/test.snap",
			want: []PathClass{SnapshotFile, Generated},
		},
		{
			name: "docs markdown",
			path: "README.md",
			want: []PathClass{Docs},
		},
		{
			name: "docs directory",
			path: "docs/architecture.md",
			want: []PathClass{Docs},
		},
		{
			name: "generated protobuf",
			path: "proto/gen/api.pb.go",
			want: []PathClass{Generated},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Path(tt.path, cfg)
			if !pathClassesEqual(got, tt.want) {
				t.Errorf("Path(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestPathHas(t *testing.T) {
	classes := []PathClass{TestFile, CIConfig}

	if !Has(classes, TestFile) {
		t.Error("Has should find TestFile")
	}

	if Has(classes, Manifest) {
		t.Error("Has should not find Manifest")
	}
}

func pathClassesEqual(a, b []PathClass) bool {
	if len(a) != len(b) {
		return false
	}
	// Create maps for comparison since order may differ
	aMap := make(map[PathClass]bool)
	bMap := make(map[PathClass]bool)
	for _, c := range a {
		aMap[c] = true
	}
	for _, c := range b {
		bMap[c] = true
	}
	for c := range aMap {
		if !bMap[c] {
			return false
		}
	}
	for c := range bMap {
		if !aMap[c] {
			return false
		}
	}
	return true
}
