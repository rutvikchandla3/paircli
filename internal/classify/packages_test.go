package classify

import (
	"testing"
)

func TestPackages(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []Package
	}{
		{
			name: "npm with version",
			cmd:  "npm i p-retry@6.2.1 -D",
			want: []Package{
				{Manager: "npm", Name: "p-retry", Version: "6.2.1"},
			},
		},
		{
			name: "pnpm with scope",
			cmd:  "pnpm add @scope/pkg@^1 other",
			want: []Package{
				{Manager: "pnpm", Name: "@scope/pkg", Version: "^1"},
				{Manager: "pnpm", Name: "other", Version: ""},
			},
		},
		{
			name: "pip with requirements file",
			cmd:  "pip install -r req.txt flask",
			want: []Package{
				{Manager: "pip", Name: "flask", Version: ""},
			},
		},
		{
			name: "go get",
			cmd:  "go get github.com/x/y@v1.2.3",
			want: []Package{
				{Manager: "go", Name: "github.com/x/y", Version: "v1.2.3"},
			},
		},
		{
			name: "npm install no packages",
			cmd:  "npm install",
			want: []Package{},
		},
		{
			name: "pip install with version",
			cmd:  "pip install requests==2.32.0",
			want: []Package{
				{Manager: "pip", Name: "requests", Version: "==2.32.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Packages(tt.cmd)
			if !packagesEqual(got, tt.want) {
				t.Errorf("Packages(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestReadTargets(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{
			name: "cat file",
			cmd:  "cat src/a.ts",
			want: []string{"src/a.ts"},
		},
		{
			name: "head with pipe",
			cmd:  "cat file | head -20",
			want: []string{"file"},
		},
		{
			name: "sed -n",
			cmd:  "sed -n 1,10p file.txt",
			want: []string{"file.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReadTargets(tt.cmd)
			if !stringsEqual(got, tt.want) {
				t.Errorf("ReadTargets(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestRmTargets(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{
			name: "rm -rf multiple",
			cmd:  "rm -rf dist node_modules",
			want: []string{"dist", "node_modules"},
		},
		{
			name: "rm without recursive",
			cmd:  "rm file.txt",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RmTargets(tt.cmd)
			if !stringsEqual(got, tt.want) {
				t.Errorf("RmTargets(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func packagesEqual(a, b []Package) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Manager != b[i].Manager || a[i].Name != b[i].Name || a[i].Version != b[i].Version {
			return false
		}
	}
	return true
}
