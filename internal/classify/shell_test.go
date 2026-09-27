package classify

import (
	"testing"
)

func TestUnwrap(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "no wrapper",
			cmd:  "npm test",
			want: "npm test",
		},
		{
			name: "bash -c with single quotes",
			cmd:  "bash -c 'npm test'",
			want: "npm test",
		},
		{
			name: "bash -lc with single quotes",
			cmd:  "bash -lc 'npm test'",
			want: "npm test",
		},
		{
			name: "sh -c with double quotes",
			cmd:  `sh -c "go build"`,
			want: "go build",
		},
		{
			name: "nested escape",
			cmd:  "bash -c 'echo \\'hello\\''",
			want: "echo 'hello'",
		},
		{
			name: "zsh -c",
			cmd:  "zsh -c 'ls -la'",
			want: "ls -la",
		},
		{
			name: "full path",
			cmd:  "/bin/bash -c 'pwd'",
			want: "pwd",
		},
		{
			name: "usr bin",
			cmd:  "/usr/bin/bash -lc 'pwd'",
			want: "pwd",
		},
		{
			name: "repeated wraps",
			cmd:  "bash -c 'bash -c \"npm test\"'",
			want: "npm test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unwrap(tt.cmd)
			if got != tt.want {
				t.Errorf("Unwrap(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestSegments(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{
			name: "single segment",
			cmd:  "npm test",
			want: []string{"npm test"},
		},
		{
			name: "and operator",
			cmd:  "npm install && npm test",
			want: []string{"npm install", "npm test"},
		},
		{
			name: "or operator",
			cmd:  "npm test || echo failed",
			want: []string{"npm test", "echo failed"},
		},
		{
			name: "pipe",
			cmd:  "cat file | head -20",
			want: []string{"cat file", "head -20"},
		},
		{
			name: "semicolon",
			cmd:  "cd app; npm install; npm test",
			want: []string{"cd app", "npm install", "npm test"},
		},
		{
			name: "quoted ampersand",
			cmd:  "echo 'a && b' && npm test",
			want: []string{"echo 'a && b'", "npm test"},
		},
		{
			name: "dollar paren nesting",
			cmd:  "echo $(git log && echo done) && npm test",
			want: []string{"echo $(git log && echo done)", "npm test"},
		},
		{
			name: "newlines",
			cmd:  "npm install\nnpm test",
			want: []string{"npm install", "npm test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Segments(tt.cmd)
			if !stringsEqual(got, tt.want) {
				t.Errorf("Segments(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		name string
		seg  string
		want []string
	}{
		{
			name: "simple",
			seg:  "npm install package@1.0",
			want: []string{"npm", "install", "package@1.0"},
		},
		{
			name: "quoted args",
			seg:  `echo "hello world"`,
			want: []string{"echo", "hello world"},
		},
		{
			name: "single quote",
			seg:  "echo 'hello world'",
			want: []string{"echo", "hello world"},
		},
		{
			name: "escaped quote",
			seg:  "echo 'it\\'s working'",
			want: []string{"echo", "it\\'s working"},
		},
		{
			name: "dollar var",
			seg:  "echo $HOME",
			want: []string{"echo", "$HOME"},
		},
		{
			name: "multiple spaces",
			seg:  "npm    install    package",
			want: []string{"npm", "install", "package"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokens(tt.seg)
			if !stringsEqual(got, tt.want) {
				t.Errorf("Tokens(%q) = %v, want %v", tt.seg, got, tt.want)
			}
		})
	}
}

func TestShortCmd(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "short command",
			cmd:  "npm test",
			want: "npm test",
		},
		{
			name: "long command",
			cmd:  "this is a very long command that should be clipped at eighty characters and only show the first part",
			want: "this is a very long command that should be clipped at eighty characters and only…",
		},
		{
			name: "wrapped command",
			cmd:  "bash -c 'npm install && npm test'",
			want: "npm install && npm test",
		},
		{
			name: "extra whitespace",
			cmd:  "npm    install    --save    package",
			want: "npm install --save package",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortCmd(tt.cmd)
			if got != tt.want {
				t.Errorf("ShortCmd(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
