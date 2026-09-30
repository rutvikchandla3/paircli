package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// piCLITimeout bounds a single `pi` invocation.
const piCLITimeout = 300 * time.Second

// piProvider shells out to a local `pi` binary in non-interactive mode.
//
// Flags were checked against `pi --version` 0.87.1 (`pi --help`, run while
// writing this file). Flags used: `-p --mode json --no-session --no-tools`,
// plus `--model` when a model is configured and `--append-system-prompt` when
// the request carries a system prompt. The prompt is written to stdin, where
// pi reads it as the message.
//
// --no-session matters for more than tidiness: pi stores sessions under
// ~/.pi/agent/sessions, which paircli reads as harness data on the next scan,
// and a judge run must not become a session the scan attributes lines to.
// --no-tools keeps a judge prompt — which only reads the fact bundle it was
// given — from wandering into the repository.
type piProvider struct {
	bin   string
	model string

	// run executes the command; overridable in tests to point at a fixture
	// script instead of a real `pi` binary.
	run func(ctx context.Context, bin string, args []string, stdin string) (stdout, stderr []byte, err error)
}

func newPi(bin string, cfg config.LLM) *piProvider {
	return &piProvider{bin: bin, model: cfg.Model, run: runCLI}
}

// Name implements Provider.
func (p *piProvider) Name() string { return "pi" }

// Complete implements Provider. It never logs the request body.
func (p *piProvider) Complete(ctx context.Context, req Request) (Response, error) {
	args := []string{"-p", "--mode", "json", "--no-session", "--no-tools"}
	if model := piModel(req.Model, p.model); model != "" {
		args = append(args, "--model", model)
	}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}

	callCtx, cancel := context.WithTimeout(ctx, piCLITimeout)
	defer cancel()

	stdout, stderr, err := p.run(callCtx, p.bin, args, req.Prompt)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("llm: pi: %w: %s", err, cliStderr(stderr))
	}
	reply, ok := parsePiStream(stdout)
	if !ok {
		return Response{}, fmt.Errorf("llm: pi: no assistant reply in output: %s", cliStderr(stderr))
	}
	if reply.Err != "" {
		return Response{}, fmt.Errorf("llm: pi: %s", reply.Err)
	}
	return Response{Text: reply.Text, InputTokens: reply.Input, OutputTokens: reply.Output}, nil
}

// piModel returns the model to pass to pi, or "" to leave the choice to pi.
//
// config.DefaultModel names a Claude model, which pi is not required to serve
// and would reject, so the shared fallback is deliberately not applied here:
// only an explicit model is passed — the request's, or llm.model set to
// something other than that default.
func piModel(reqModel, cfgModel string) string {
	if reqModel != "" {
		return reqModel
	}
	if cfgModel != "" && cfgModel != config.DefaultModel {
		return cfgModel
	}
	return ""
}

// cliStderr renders a subprocess's stderr for an error message, bounded so a
// chatty binary cannot flood the report.
func cliStderr(stderr []byte) string {
	msg := bytes.TrimSpace(stderr)
	if len(msg) == 0 {
		return "no output"
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return string(msg)
}

// piReply is what one pi run reported: the assistant's text, or the error the
// turn ended with, plus the token counts.
type piReply struct {
	Text   string
	Err    string
	Input  int
	Output int
}

// piEvent is one object of pi's --mode json stream. The stream is JSON Lines —
// session, agent_start, turn_start, message_start, message_end, turn_end,
// agent_end, agent_settled — and the reply is the last assistant message in
// it. A turn the provider refused ends with stopReason "error" and an
// errorMessage, and can still exit 0.
type piEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		StopReason   string          `json:"stopReason"`
		ErrorMessage string          `json:"errorMessage"`
		Usage        *struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"usage"`
	} `json:"message"`
}

// parsePiStream extracts the last assistant message from pi's event stream. It
// reports false when the stream carried no assistant message at all, which is
// a failure however cleanly pi exited.
func parsePiStream(out []byte) (piReply, bool) {
	var reply piReply
	found := false
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		// Non-JSON lines (a runtime warning on stdout, say) are skipped rather
		// than failing the parse: the events are what matter.
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev piEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		m := ev.Message
		if m == nil || m.Role != "assistant" {
			continue
		}
		if ev.Type != "message_end" && ev.Type != "turn_end" {
			continue
		}
		found = true
		reply = piReply{Text: piContentText(m.Content)}
		if m.StopReason == "error" {
			reply.Err = m.ErrorMessage
			if reply.Err == "" {
				reply.Err = "the model reported an error"
			}
		}
		if m.Usage != nil {
			reply.Input, reply.Output = m.Usage.Input, m.Usage.Output
		}
	}
	return reply, found
}

// piContentText joins the text parts of a pi message body, which is either a
// plain string or a list of typed parts.
func piContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
