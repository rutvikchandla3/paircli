# T09 — LLM providers

**Model:** sonnet · **Wave:** B · **Depends on:** T01 · **Milestone:** M3

## Goal

A small provider layer the judge (T23) uses: the Anthropic Messages API, the
local `claude` CLI, and a fake for tests.

## Read first

- `docs/plan/CONTRACTS.md` "internal/llm"
- `internal/config/config.go` (`config.LLM`, `DefaultModel`)
- **If your environment provides a `claude-api` skill, load it before writing
  the Anthropic client** and follow it for request shape, headers, model IDs
  and error handling. Otherwise use https://docs.anthropic.com/ (Messages API).

## Files you own

- `internal/llm/*.go` + tests + `testdata/`

## API (exact)

```go
type Request struct {
	Model     string
	System    string
	Prompt    string
	MaxTokens int
}
type Response struct {
	Text                      string
	InputTokens, OutputTokens int
}
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}
func New(cfg config.LLM) (Provider, error)
type Fake struct {
	Replies []string
	Calls   []Request
}
```

## Providers

**`New(cfg)`**: `Provider` `""` or `"none"` → `(nil, nil)`. `"anthropic"` →
requires `ANTHROPIC_API_KEY`, else error `llm: ANTHROPIC_API_KEY is not set`.
`"claude-cli"` → requires the `claude` binary (`PAIRCLI_CLAUDE_BIN` overrides
the path; else `exec.LookPath("claude")`). Anything else → error. Empty
`req.Model` falls back to `cfg.Model`, then `config.DefaultModel`.

**Anthropic (`anthropic.go`)**
- `POST {base}/v1/messages`, base = `$ANTHROPIC_BASE_URL` or `https://api.anthropic.com`.
- Headers: `x-api-key`, `anthropic-version: 2023-06-01`, `content-type: application/json`.
- Body: `{"model", "max_tokens" (default 4096), "system", "messages":[{"role":"user","content": prompt}]}`.
- Response: concatenate `content[]` blocks of type `text`; tokens from `usage.input_tokens` / `usage.output_tokens`.
- Retries: on 429, 500, 502, 503, 504, 529 and network errors, up to 3
  retries with backoff 1 s, 2 s, 4 s, honoring a `retry-after` header (seconds)
  when present. Non-retryable 4xx → error with status and the API's
  `error.message`.
- Per-call timeout 180 s via context. Respect the caller's context cancellation.
- Never log the API key or request body.

**Claude CLI (`claudecli.go`)**
- Run `<bin> -p --output-format json --model <model> --max-turns 1` with the
  prompt on stdin and `--append-system-prompt <system>` when System is set.
  Before finalizing flags, run `claude --help` locally and adjust to the
  installed version (document what you used in a comment).
- Parse stdout JSON: text from `result`; tokens from `usage.input_tokens` and
  `usage.output_tokens` when present. `is_error: true` → error with `result`.
- Timeout 300 s. Non-zero exit → error including the first 500 bytes of stderr.

**Fake (`fake.go`)**: `Complete` appends the request to `Calls` and returns
`Replies` in order; when exhausted returns an error `llm: fake has no more replies`.

## Tests

- `TestNew_None`, `TestNew_AnthropicNeedsKey`, `TestNew_Unknown`, `TestNew_ModelFallback`.
- `TestAnthropic_Success` (httptest server checks headers, body, returns two text blocks + usage).
- `TestAnthropic_RetryThenSuccess` (529 then 200; use a test hook to shrink backoff).
- `TestAnthropic_RetryAfterHeader`.
- `TestAnthropic_NonRetryable` (400 with error message).
- `TestAnthropic_ContextCancel`.
- `TestClaudeCLI_Success` / `TestClaudeCLI_Error` using `PAIRCLI_CLAUDE_BIN`
  pointing at a script in `testdata/` that checks its args and prints canned JSON.
- `TestFake`.

## Acceptance

```sh
go test ./internal/llm/...
```
