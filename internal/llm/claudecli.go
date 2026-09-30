package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// claudeCLITimeout bounds a single `claude` invocation.
const claudeCLITimeout = 300 * time.Second

// claudeCLIProvider shells out to a local `claude` binary in print mode.
//
// Flags were checked against `claude --version` 2.1.283 (`claude --help`,
// run while writing this file). That version has no `--max-turns` flag, so
// the spec's suggested "--max-turns 1" is omitted; a judge prompt declares
// no tools, so a single response is expected from `-p` regardless. Flags
// used: `-p --output-format json --model <model>`, plus
// `--append-system-prompt <system>` when Request.System is set. The prompt
// is written to stdin.
type claudeCLIProvider struct {
	bin   string
	model string

	// run executes the command; overridable in tests to point at a fixture
	// script instead of a real `claude` binary.
	run func(ctx context.Context, bin string, args []string, stdin string) (stdout, stderr []byte, err error)
}

func newClaudeCLI(bin string, cfg config.LLM) *claudeCLIProvider {
	return &claudeCLIProvider{bin: bin, model: cfg.Model, run: runCLI}
}

// runCLI runs bin with args, feeding stdin, and returns its streams. It is the
// shared shell-out of the provider CLIs (claude-cli, pi); tests point the
// providers' own run field at a fixture script instead.
func runCLI(ctx context.Context, bin string, args []string, stdin string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Name implements Provider.
func (p *claudeCLIProvider) Name() string { return "claude-cli" }

// claudeCLIResult is the shape of `claude -p --output-format json`'s stdout.
type claudeCLIResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete implements Provider. It never logs the request body.
func (p *claudeCLIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	model := resolveModel(req.Model, p.model)
	args := []string{"-p", "--output-format", "json", "--model", model}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}

	callCtx, cancel := context.WithTimeout(ctx, claudeCLITimeout)
	defer cancel()

	stdout, stderr, err := p.run(callCtx, p.bin, args, req.Prompt)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		msg := stderr
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return Response{}, fmt.Errorf("llm: claude-cli: %w: %s", err, msg)
	}

	var parsed claudeCLIResult
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		return Response{}, fmt.Errorf("llm: claude-cli: decode output: %w", err)
	}
	if parsed.IsError {
		return Response{}, fmt.Errorf("llm: claude-cli: %s", parsed.Result)
	}
	return Response{
		Text:         parsed.Result,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}, nil
}
