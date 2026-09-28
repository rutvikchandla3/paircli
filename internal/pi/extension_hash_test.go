package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"strconv"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// extensionHandlers are the Pi events the extension must subscribe to
// (docs/plan/tasks/T21-pi-extension.md).
var extensionHandlers = []string{
	"session_start",
	"tool_call",
	"tool_execution_end",
	"before_agent_start",
	"agent_end",
	"session_shutdown",
}

// lineHashVectors are the vectors documented in T21. Go's model.LineHash and
// the extension's lineHash must both produce exactly these.
var lineHashVectors = []struct {
	in   string
	want string
}{
	{"", "cbf29ce484222325"},
	{"const max = 5;", "60ff39090249e173"},
	{"  return sendWithRetry(url, body);  ", "dfa2ccde93f1965a"},
	{"héllo wörld", "11824ab841812022"},
	{"\ttabbed\r", "0677650ad81e5a82"},
}

// extraHashCases are edge cases outside the documented table; they only have
// to agree between Go and the extension, not with a hardcoded value.
var extraHashCases = []string{
	"a",
	"  a  ",
	"a\t\t",
	"trailing spaces   ",
	"\r",
	"   ",
	"\t",
	"日本語のテキスト",
	"🚀 emoji",
	"mixed	 tabs\rand spaces   ",
	"// paircli-extension v1",
	"	indented\r",
	"no-trailing-whitespace",
}

func TestExtension_Hygiene(t *testing.T) {
	src := string(extensionSource)

	lines := strings.Split(src, "\n")
	if len(lines) == 0 || lines[0] != extensionMarker {
		t.Fatalf("first line = %q, want exactly %q", firstLine(src), extensionMarker)
	}

	// Every import must either be type-only or come from a node: built-in.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "import ") {
			continue
		}
		if strings.HasPrefix(trimmed, "import type ") {
			continue // erased at load time; no runtime dependency
		}
		if !strings.Contains(trimmed, `from "node:`) && !strings.Contains(trimmed, `from 'node:`) {
			t.Errorf("non-type import outside node: %q", trimmed)
		}
	}

	// The type-only import of ExtensionAPI is required by the contract.
	if !strings.Contains(src, `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";`) {
		t.Error("missing the type-only ExtensionAPI import")
	}

	for _, h := range extensionHandlers {
		if !strings.Contains(src, fmt.Sprintf("%q", h)) {
			t.Errorf("extension does not reference handler %q", h)
		}
	}

	// Records are written through appendEntry under the "paircli" customType.
	if !strings.Contains(src, `appendEntry("paircli"`) {
		t.Error(`extension does not call appendEntry("paircli", …)`)
	}
	if !strings.Contains(src, `harness: "pi"`) {
		t.Error(`extension does not set harness: "pi"`)
	}
}

func TestExtension_LineHashMatchesGo(t *testing.T) {
	// The documented vectors must match Go first: this catches Go-side drift
	// even when node is unavailable.
	for _, v := range lineHashVectors {
		if got := model.LineHash(v.in); got != v.want {
			t.Errorf("model.LineHash(%q) = %q, want %q", v.in, got, v.want)
		}
	}

	node := nodeForTypeStripping(t)
	if node == "" {
		t.Skip("node with --experimental-strip-types not available")
	}

	vectors := make([]string, 0, len(lineHashVectors)+len(extraHashCases))
	for _, v := range lineHashVectors {
		vectors = append(vectors, v.in)
	}
	vectors = append(vectors, extraHashCases...)

	dir := t.TempDir()
	extPath := filepath.Join(dir, "paircli.ts")
	if err := os.WriteFile(extPath, extensionSource, 0o644); err != nil {
		t.Fatal(err)
	}

	vecJSON, err := json.Marshal(vectors)
	if err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf(`import { lineHash } from "./paircli.ts";
const vectors = %s;
console.log(JSON.stringify(vectors.map(lineHash)));
`, vecJSON)

	scriptPath := filepath.Join(dir, "probe.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "--experimental-strip-types", scriptPath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("running node failed: %v\n%s", err, stderr)
	}

	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding node output %q: %v", out, err)
	}
	if len(got) != len(vectors) {
		t.Fatalf("node returned %d hashes, want %d", len(got), len(vectors))
	}

	for i, v := range vectors {
		want := model.LineHash(v)
		if got[i] != want {
			t.Errorf("lineHash(%q): node = %q, Go = %q", v, got[i], want)
		}
	}
}

// nodeForTypeStripping returns the path to a node binary that supports
// --experimental-strip-types (Node >= 22.6), or "" when none is available.
func nodeForTypeStripping(t *testing.T) string {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		return ""
	}
	major, minor, ok := parseNodeVersion(strings.TrimSpace(string(out)))
	if !ok {
		return ""
	}
	if major < 22 || (major == 22 && minor < 6) {
		return ""
	}

	// Confirm the flag actually works on this build rather than trusting the
	// version string alone.
	check := exec.Command(node, "--experimental-strip-types", "--eval", "process.exit(0)")
	if err := check.Run(); err != nil {
		return ""
	}
	return node
}

// parseNodeVersion parses a "v22.6.0" style version into major and minor.
func parseNodeVersion(v string) (major, minor int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// firstLine returns src up to the first newline.
func firstLine(src string) string {
	if i := strings.IndexByte(src, '\n'); i >= 0 {
		return src[:i]
	}
	return src
}
