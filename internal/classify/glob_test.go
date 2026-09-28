package classify

import (
	"testing"
)

func TestGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{
			name:    "exact match",
			pattern: "package.json",
			path:    "package.json",
			want:    true,
		},
		{
			name:    "exact no match",
			pattern: "package.json",
			path:    "package-lock.json",
			want:    false,
		},
		{
			name:    "star wildcard",
			pattern: "*.ts",
			path:    "main.ts",
			want:    true,
		},
		{
			name:    "star no cross slash",
			pattern: "*.tf",
			path:    "a/b.tf",
			want:    false,
		},
		{
			name:    "double star at start",
			pattern: "**/x.go",
			path:    "x.go",
			want:    true,
		},
		{
			name:    "double star nested",
			pattern: "**/x.go",
			path:    "a/b/x.go",
			want:    true,
		},
		{
			name:    "double star at end",
			pattern: "deploy/**",
			path:    "deploy/a/b",
			want:    true,
		},
		{
			name:    "double star directory",
			pattern: ".github/workflows/**",
			path:    ".github/workflows/test.yml",
			want:    true,
		},
		{
			name:    "question mark",
			pattern: "test?.go",
			path:    "test1.go",
			want:    true,
		},
		{
			name:    "question mark no match",
			pattern: "test?.go",
			path:    "test.go",
			want:    false,
		},
		{
			name:    "complex path",
			pattern: "**/test/**/*.ts",
			path:    "src/test/unit/utils.ts",
			want:    true,
		},
		{
			name:    "character class range matches",
			pattern: "src/[a-c].go",
			path:    "src/b.go",
			want:    true,
		},
		{
			name:    "character class range misses outside",
			pattern: "src/[a-c].go",
			path:    "src/d.go",
			want:    false,
		},
		{
			name:    "character class digit range",
			pattern: "v[0-9].md",
			path:    "v7.md",
			want:    true,
		},
		{
			name:    "negated class excludes",
			pattern: "src/[!a].go",
			path:    "src/a.go",
			want:    false,
		},
		{
			name:    "negated class includes",
			pattern: "src/[!a].go",
			path:    "src/b.go",
			want:    true,
		},
		{
			name:    "caret negation",
			pattern: "src/[^a].go",
			path:    "src/a.go",
			want:    false,
		},
		{
			name:    "trailing dash is literal",
			pattern: "src/[a-].go",
			path:    "src/-.go",
			want:    true,
		},
		{
			name:    "literal class still matches",
			pattern: "src/[ab].go",
			path:    "src/b.go",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Glob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("Glob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
