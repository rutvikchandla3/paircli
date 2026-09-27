package classify

import (
	"regexp"
	"strings"
)

// Package represents a parsed package with manager, name, and version.
type Package struct {
	Manager string
	Name    string
	Version string
}

// Packages extracts packages from an install command.
// For install segments, returns non-flag arguments after the subcommand as packages,
// skipping values of flags that take one.
func Packages(cmd string) []Package {
	segs := Segments(cmd)

	var result []Package
	for _, seg := range segs {
		// Check if this segment is an install command
		tokens := Tokens(seg)
		if len(tokens) == 0 {
			continue
		}

		if !isInstallSegment(strings.Join(tokens, " ")) {
			continue
		}

		// Get the manager (first token)
		manager := tokens[0]

		// Find where the package args start
		pkgStart := findPackageStart(tokens)
		if pkgStart < 0 || pkgStart >= len(tokens) {
			continue
		}

		// Collect package arguments, skipping flag values
		i := pkgStart
		for i < len(tokens) {
			token := tokens[i]

			// Skip flags and their values
			if strings.HasPrefix(token, "-") {
				flagsTakingValue := map[string]bool{
					"-r": true, "-c": true, "--registry": true, "--index-url": true,
					"-e": true, "--prefix": true, "--save-prefix": true,
				}
				if flagsTakingValue[token] && i+1 < len(tokens) {
					i += 2 // skip flag and its value
					continue
				}
				i++
				continue
			}

			// Parse package@version
			pkg := parsePackageVersion(token)
			pkg.Manager = manager
			result = append(result, pkg)
			i++
		}
	}

	return result
}

// ReadTargets returns non-flag arguments of read_file segments.
func ReadTargets(cmd string) []string {
	segs := Segments(cmd)

	var result []string
	for _, seg := range segs {
		tokens := Tokens(seg)
		if len(tokens) == 0 {
			continue
		}

		// Check if this is a read command
		readCmds := []string{"cat", "head", "tail", "less", "more", "bat", "nl"}
		isSedN := tokens[0] == "sed" && contains(tokens, "-n")

		isRead := false
		for _, cmd := range readCmds {
			if tokens[0] == cmd {
				isRead = true
				break
			}
		}

		if !isRead && !isSedN {
			continue
		}

		// Collect non-flag arguments, skipping the command name
		// For sed -n, skip the first non-flag arg (the script)
		skipFirst := isSedN
		for i := 1; i < len(tokens); i++ {
			token := tokens[i]
			if strings.HasPrefix(token, "-") {
				continue
			}
			if skipFirst {
				skipFirst = false
				continue
			}
			result = append(result, token)
		}
	}

	return result
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// RmTargets returns non-flag arguments of rm_rf segments.
func RmTargets(cmd string) []string {
	segs := Segments(cmd)

	var result []string
	for _, seg := range segs {
		tokens := Tokens(seg)
		if len(tokens) == 0 {
			continue
		}

		s := strings.Join(tokens, " ")

		// Check if this is an rm -rf command
		if !strings.HasPrefix(s, "rm") {
			continue
		}

		// Check for recursive flag
		if !hasRecursiveFlag(tokens) {
			continue
		}

		// Collect non-flag arguments
		for _, token := range tokens {
			if !strings.HasPrefix(token, "-") && token != "rm" {
				result = append(result, token)
			}
		}
	}

	return result
}

// Helper functions

func isInstallSegment(s string) bool {
	installPatterns := []string{
		`^npm\s+(i|install|add|ci)\b`,
		`^pnpm\s+(add|i|install)\b`,
		`^yarn\s+add\b`,
		`^yarn(\s+install)?$`,
		`^bun\s+(add|i|install)\b`,
		`^pip3?\s+install\b`,
		`^uv\s+(add|pip\s+install)\b`,
		`^poetry\s+add\b`,
		`^go\s+get\b`,
		`^go\s+install\s+\S+@`,
		`^cargo\s+(add|install)\b`,
		`^gem\s+install\b`,
		`^bundle\s+(add|install)\b`,
		`^brew\s+install\b`,
		`^apt(-get)?\s+install\b`,
	}

	for _, pattern := range installPatterns {
		if regexp.MustCompile(pattern).MatchString(s) {
			return true
		}
	}
	return false
}

func findPackageStart(tokens []string) int {
	if len(tokens) == 0 {
		return -1
	}

	manager := tokens[0]

	// Different managers have different subcommand structures
	switch manager {
	case "npm", "pnpm", "bun":
		// npm/pnpm/bun: npm install, npm i, npm add, npm ci
		// packages start after the subcommand
		for i := 1; i < len(tokens); i++ {
			if !strings.HasPrefix(tokens[i], "-") && !strings.Contains(tokens[i], "=") {
				if tokens[i] == "install" || tokens[i] == "add" || tokens[i] == "ci" || tokens[i] == "i" {
					return i + 1
				}
			}
		}
		// Fallback: return first non-flag after manager
		for i := 1; i < len(tokens); i++ {
			if !strings.HasPrefix(tokens[i], "-") {
				return i
			}
		}
		return -1

	case "yarn":
		// yarn add, yarn install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "add" {
				return i + 1
			}
			if tokens[i] == "install" {
				return -1 // bare yarn install has no packages
			}
		}
		return -1

	case "pip", "pip3":
		// pip install: packages come after install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "install" {
				return i + 1
			}
		}
		return -1

	case "uv":
		// uv add, uv pip install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "add" {
				return i + 1
			}
			if tokens[i] == "pip" && i+1 < len(tokens) && tokens[i+1] == "install" {
				return i + 2
			}
		}
		return -1

	case "poetry":
		// poetry add
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "add" {
				return i + 1
			}
		}
		return -1

	case "go":
		// go get, go install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "get" || tokens[i] == "install" {
				return i + 1
			}
		}
		return -1

	case "cargo":
		// cargo add, cargo install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "add" || tokens[i] == "install" {
				return i + 1
			}
		}
		return -1

	case "gem":
		// gem install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "install" {
				return i + 1
			}
		}
		return -1

	case "bundle":
		// bundle add, bundle install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "add" {
				return i + 1
			}
			if tokens[i] == "install" {
				return -1 // bare bundle install has no packages
			}
		}
		return -1

	case "brew":
		// brew install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "install" {
				return i + 1
			}
		}
		return -1

	case "apt", "apt-get":
		// apt install
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "install" {
				return i + 1
			}
		}
		return -1
	}

	return -1
}

func parsePackageVersion(pkgStr string) Package {
	pkg := Package{}

	// Check for version specifiers by manager
	// npm/yarn/pnpm: name@version or @scope/name@version
	// pip: name==version, name>=version, name[extra]==version
	// go: module@v1.2.3
	// cargo: name@1.2

	// Try npm format first (@scope/name@version or name@version)
	if strings.Contains(pkgStr, "@") {
		if strings.HasPrefix(pkgStr, "@") {
			// @scope/name or @scope/name@version
			// Split by "/" first
			slashIdx := strings.Index(pkgStr, "/")
			if slashIdx > 0 {
				scope := pkgStr[:slashIdx]            // @scope
				rest := pkgStr[slashIdx+1:]           // name or name@version
				atIdx := strings.LastIndex(rest, "@") // Find last @ for version
				if atIdx > 0 {
					pkg.Name = scope + "/" + rest[:atIdx]
					pkg.Version = rest[atIdx+1:]
				} else {
					pkg.Name = scope + "/" + rest
				}
				return pkg
			}
		}
		// name@version or name@version@version (rare)
		atIdx := strings.LastIndex(pkgStr, "@")
		if atIdx > 0 {
			pkg.Name = pkgStr[:atIdx]
			pkg.Version = pkgStr[atIdx+1:]
			return pkg
		}
	}

	// Try pip format (name==version or name[extra]==version)
	if strings.Contains(pkgStr, "==") || strings.Contains(pkgStr, ">=") || strings.Contains(pkgStr, "<=") {
		idx := strings.IndexAny(pkgStr, "=<>")
		if idx > 0 {
			pkg.Name = pkgStr[:idx]
			pkg.Version = pkgStr[idx:]
			// Remove extra syntax [...]
			if idx := strings.Index(pkg.Name, "["); idx > 0 {
				pkg.Name = pkg.Name[:idx]
			}
			return pkg
		}
	}

	// No version found
	pkg.Name = pkgStr
	return pkg
}
