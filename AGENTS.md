# AGENTS.md

## Project

Go CLI (`github.com/primerouter/pr-audit`) that audits PrimeRouter LLM-gateway response integrity via a three-tier trust model:

- **L1 — local self-consistency**: `verify` command. SHA256 of the saved body vs. PrimeRouter's `x-upstream-sha256` header.
- **L2 — external attestation**: dashboard URL + trace-id hint, surfaced by both commands.
- **L3 — end-to-end replay**: `replay` command. Re-runs the request directly against the upstream vendor with the user's own key and reconciles deterministic fields (`prompt_tokens`, `model`).

Both `verify` and `replay` are first-class Cobra subcommands.

## Commands

```bash
make build  # go build with version ldflags → ./pr-audit
make test   # go test -race -cover ./...
make lint   # go vet + gofmt -l check (no external linter)
make fmt    # gofmt -w .
make cover  # coverage report
make gocloc # line count (informational only; needs gocloc installed)
```

Run a single test: `go test -race -run TestName ./internal/replay/`

## Architecture

- `cmd/pr-audit/` — entrypoint (`main.go`) + Cobra subcommands `verify.go`, `replay.go`
- `internal/verify/` — L1 pipeline: parse, hash, check
- `internal/replay/` — L3 pipeline: routing, vendor reconcilers (`openai.go`/`anthropic.go`/`gemini.go`), shared HTTP, degradation helpers
- `internal/vendor/` — vendor detection, dashboard URLs, count-tokens endpoint registry
- `internal/output/` — human and JSON renderers (one renderer pair, both subcommands share it)
- `internal/model/` — shared types only, no behavior
- `testdata/mocks/` — fixture files for CLI-level testing

## Constraints

- Third-party dependencies are minimal: `cobra` (CLI), `tiktoken-go` + `tiktoken-go-loader` (offline OpenAI tokeniser). Do not add more without strong justification.
- Go 1.22; no generics or newer stdlib features beyond that version.
- `vendor-key` (the user's upstream API key) MUST never be serialised into the JSON or printed in human output. The `model.Result` type intentionally does not have a field for it.

## Exit codes (load-bearing)

| Code | Meaning |
|------|---------|
| 0 | L1 pass (`verify`), or L1 + L3 reconciled / L3 skipped / L3 degraded (`replay`) |
| 10 | L1 fail (hash mismatch / bad algorithm) — `replay` returns this and skips L3 |
| 20 | Input parse error (missing files, malformed `request.json`, bad flags) |
| 31 | L3 network: DNS resolution failed |
| 32 | L3 network: TLS handshake / certificate failed |
| 33 | L3 network: upstream timeout or 5xx |
| 40 | L3 fail (prompt_tokens or model mismatch against upstream) |
| 99 | Internal error |

## Output wording rules

- **Never** output `VERIFIED`. L1 pass = `SELF-CONSISTENT`. L3 pass = `NO EVIDENCE OF TAMPERING`. Both are documented in `docs/trust-model.md` §3.4.
- Self-consistent ≠ honest, and `NO EVIDENCE OF TAMPERING` is the strongest verdict pr-audit can produce — it does not prove the assistant text is uncensored.
- L1 fail and L3 fail must not suggest next steps; the user should stop and investigate.
- The human header is `pr-audit v<VERSION>` regardless of subcommand.

## L3 strategy routing

| Vendor | Strategy | Vendor key required? |
|--------|----------|----------------------|
| `openai`, `azure-openai` | `tiktoken_offline` | No |
| `anthropic` | `count_tokens_api` | Yes |
| `gemini` | `count_tokens_api` | Yes |
| `zhipu`, `deepseek`, `moonshot`, unknown | `skipped` | n/a |

OpenAI requests carrying tools degrade to `structural` (model-only check) because tiktoken cannot reproduce vendor-private tool overhead.

Anthropic reconciliation MUST sum `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` against `count_tokens` — this is the single most bug-prone line in the project.

## Testing

- Tests live alongside code (`*_test.go` in the same package).
- L3 vendor reconcilers are tested against `httptest.Server` — never the real upstream API.
- Integration tests use `t.TempDir()` to build header / body / request files on disk, then call `verify.Run()` or `replay.Run()`.
- Always run with `-race` flag (CI and Makefile both require it).

## CI

Three OS matrix (ubuntu, macos, windows). Two jobs: `build-test` (build + test) and `lint` (gofmt + go vet). The hard `< 2000 LOC` gocloc gate has been removed for v0.2 — `make gocloc` still works locally for tracking, but CI no longer fails on line count.
