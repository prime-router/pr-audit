# AGENTS.md

## Project

Go CLI (`github.com/primerouter/pr-audit`) that audits PrimeRouter LLM-gateway response integrity via a three-tier trust model (L1 local hash, L2 vendor dashboard, L3 replay). Only L1 (`verify` command) is implemented today; L3 (`replay`) is planned for v0.2.

## Commands

```bash
make build          # go build with version ldflags → ./pr-audit
make test           # go test -race -cover ./...
make lint           # go vet + gofmt -l check (no external linter)
make fmt            # gofmt -w .
make cover          # coverage report
make gocloc         # line count (needs gocloc installed)
```

Run a single test: `go test -race -run TestName ./internal/verify/`

## Architecture

- `cmd/pr-audit/` — entrypoint (`main.go`) + `verify` subcommand wiring (`verify.go`)
- `internal/verify/` — core L1 pipeline: parse, hash, check
- `internal/vendor/` — vendor detection (header-first, body-model-fallback) + dashboard URLs
- `internal/output/` — human and JSON renderers
- `internal/model/` — shared types only, no behavior
- `testdata/mocks/` — fixture files for CLI-level testing

## Constraints

- **< 2000 Go code lines** enforced by CI (`gocloc` job). Keep it minimal. Every new line counts.
- Zero third-party dependencies (go.mod has none). Do not add one without strong reason.
- Go 1.22; no generics or newer stdlib features beyond that version.

## Exit codes (load-bearing)

| Code | Meaning |
|------|---------|
| 0 | L1 pass or L1 unavailable |
| 10 | L1 fail (hash mismatch / bad algorithm) |
| 11 | Reserved (strict mode, not yet used) |
| 20 | Input parse error |
| 99 | Internal error |

## Output wording rules

- **Never** output `VERIFIED`. L1 pass = `SELF-CONSISTENT` only. This is a core trust-model constraint (`docs/trust-model.md` §3.4).
- Self-consistent ≠ honest. CLI output must always clarify what L1 can and cannot prove.
- L1 fail must not suggest next steps — the user should stop and investigate.

## Testing

- Tests live alongside code (`*_test.go` in the same package).
- Integration tests use `t.TempDir()` + `writeFixture()` to build header/body files on disk, then call `verify.Run()`.
- Fixtures in `testdata/mocks/` are for CLI-level manual testing, not imported by Go tests.
- Always run with `-race` flag (CI and Makefile both require it).

## CI

Three OS matrix (ubuntu, macos, windows). Two jobs: `build-test` (build + test) and `lint` (gofmt + go vet). Separate `gocloc` job enforces the line count.
