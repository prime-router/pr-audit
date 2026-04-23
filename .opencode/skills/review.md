---
name: review
description: Go code review for current branch changes. Triggers when user says "review", "code review", or "review code".
---

Go code review for current branch changes, focusing on coding standards, open-source conventions, and project-specific constraints.

## Input

The diff of the current git branch relative to the main branch. Fetched automatically; no user input required.

## Steps

### 1. Collect Changes

Run in parallel with Bash:
- `git diff main...HEAD --stat` — changed file list
- `git diff main...HEAD` — full diff
- `git log main...HEAD --oneline` — commit history

If the current branch cannot be determined, use `git diff HEAD~1..HEAD` to review the latest commit.

Also run project lint and tests:
- `make lint` — gofmt + go vet
- `make test` — go test -race -cover ./...

### 2. Automated Review (item-by-item checks)

Use a Task subagent to execute the following checklist against the diff. Give each item ✅ / ⚠️ / ❌ + explanation.

#### 2.1 Go Coding Standards (highest priority)

- ❌ **gofmt formatting**: All `.go` files must pass `gofmt -l .` check. Unformatted files get ❌ directly.
- ❌ **go vet**: All code must pass `go vet ./...`. Any vet-reported issue gets ❌ directly.
- ❌ **English comments**: All Go code comments (including `//`, `/* */`, doc comments) must be in English. Chinese comments get ❌.
- ⚠️ **Effective doc comments**: Exported functions/types/constants must have doc comments, and the first sentence should start with the declared name (e.g., `// ParseUsage extracts ...` not `// This function parses ...`).
- ⚠️ **Package doc comments**: Each package should have a package-level doc comment starting with `// Package xxx ...`.
- ⚠️ **Naming conventions**:
  - Exported names use PascalCase; unexported use camelCase.
  - Acronyms are all-uppercase or all-lowercase: `HTTP`, `ID`, `SHA256` (exported) / `http`, `id` (unexported), not `Http`, `Id`.
  - Interface names: single-method interfaces use `-er` suffix (`Reader`, `Hasher`); multi-method interfaces use descriptive nouns.
  - Boolean variables/fields: `hasX`, `isX`, or concise assertive style (`Present`, `Enabled`).
- ⚠️ **Error handling**:
  - Do not ignore errors (`_ = doSomething()` requires a comment explaining why, otherwise ❌).
  - Error variable naming: `ErrXxx` (exported) / `errXxx` (unexported).
  - Custom error types use `errors.New` or `fmt.Errorf`, not `errors.Errorf` (doesn't exist).
  - Error messages should not start with a capital letter or end with punctuation (`fmt.Errorf("bad header %q", h)` not `fmt.Errorf("Bad header %q.", h)`).
- ⚠️ **Panic restrictions**: Library code must not `panic`. Only `main` and top-level CLI handlers may use `log.Fatal`/`os.Exit`. Any `panic` in `internal/` packages requires strong justification.
- ⚠️ **Context passing**: If a new function involves I/O or long-running operations, the first parameter should be `ctx context.Context`.

#### 2.2 Go Open-Source Project Conventions

- ❌ **go.mod tidy**: After `go mod tidy`, `go.sum` should not have extraneous entries. New dependencies must be justified.
- ⚠️ **MIT License header**: The project is MIT licensed. Check if anyone has added non-MIT license statements in code file headers.
- ⚠️ **README updates**: If new CLI subcommands are added or user-visible behavior changes, the README should be updated accordingly.
- ⚠️ **CHANGELOG**: If the change is semver-level (new command, breaking change), consider whether a CHANGELOG or release note is needed.
- ⚠️ **.gitignore**: Newly generated files (binaries, coverage files, etc.) should be covered by `.gitignore`.

#### 2.3 Go Code Quality

- ⚠️ **Export control**: Do newly exported symbols (PascalCase) really need to be exported? If only used within the same package, make them unexported.
- ⚠️ **Struct field tag consistency**: JSON tag style should match existing code (`json:"name,omitempty"`). Don't omit `omitempty` for new fields where zero values are meaningless.
- ⚠️ **Constants vs magic strings**: Repeatedly used strings should be extracted as constants (e.g., header name `x-upstream-sha256`).
- ⚠️ **Slice pre-allocation**: Slices with known size should use `make([]T, 0, n)` to pre-allocate, avoiding repeated expansion from `append`.
- ⚠️ **Defer in loops**: `defer` inside a loop defers execution until the function returns, which may cause resource leaks. Check for this pattern.
- ⚠️ **Goroutine leaks**: New goroutines must have a clear exit mechanism (context cancel / done channel).

#### 2.4 Testing Standards

- ⚠️ **Test file style**: Test files should be in the same package as the code under test (`*_test.go` in the same directory). Do not create `tests/` subdirectories.
- ⚠️ **Test function naming**: `Test{Feature}_{Scenario}`, e.g., `TestRun_L1Pass`, `TestParseHashHeader_Malformed`.
- ⚠️ **Table-driven tests**: Multi-scenario tests should prefer `[]struct{ want ... }` + `t.Run` sub-test pattern.
- ⚠️ **Test isolation**: Use `t.TempDir()` to create temporary files; do not write to project directories. Do not depend on test execution order.
- ⚠️ **Race conditions**: Tests must pass with `-race`. Do not use `time.Sleep` for synchronization.
- ⚠️ **New logic must have tests**: New branches, error paths, and edge conditions should have corresponding test cases.

#### 2.5 Project-Specific Constraints (Trust Model)

- ❌ **No `VERIFIED`**: Search new/modified strings for `VERIFIED` (any case combination) — it is a violation. L1 pass = `SELF-CONSISTENT`, nothing else.
- ❌ **No implying "verified"**: Output text must not imply that L1 can prove honesty. Check for words like `proved`, `confirmed`, `trusted`, `guaranteed`.
- ⚠️ **L1 failure must not suggest next steps**: The `OutcomeL1Fail` branch must not output `NextSteps`.
- ⚠️ **self-consistent ≠ honest**: Any new human-readable output mentioning L1 pass must include "does not prove the body equals what the upstream vendor actually returned" or equivalent wording.
- ⚠️ **Exit code semantics**: 0 = L1 pass/unavailable, 10 = L1 fail, 20 = parse error, 99 = internal. New exit codes require updating AGENTS.md first.
- ⚠️ **model package is data-only**: `internal/model/` may only define types (structs + constants + enums), no business logic functions.

#### 2.6 Security

- ❌ **No secrets/credentials**: The diff must not contain hardcoded API keys, tokens, or passwords.
- ❌ **No path injection**: File path parameters should come from CLI flags; do not concatenate user-controllable strings for `os.Open`.

### 3. Generate Review Report

Output format:

```markdown
# Code Review

## Summary
- Branch: {branch}
- Changes: {N} files, +{A} / -{D} lines
- Conclusion: ✅ Pass / ⚠️ Needs discussion / ❌ Must fix

## Check Results

| Category | Check | Result | Notes |
|----------|-------|--------|-------|
| Go coding standards | gofmt | ✅/⚠️/❌ | ... |

## Issues

### ❌ {Issue title}
- **File**: `{path}:{line}`
- **Reason**: {why it's a problem}
- **Suggestion**: {how to fix it}

### ⚠️ {Suggestion title}
- **File**: `{path}:{line}`
- **Reason**: {why it's suggested}
- **Suggestion**: {how to improve}

## Good Practices
- {worthwhile implementation choices}
```

### 4. If there are ❌ Issues

Clearly tell the user which ❌ must be fixed before merging and which ⚠️ are open to discussion. Do not automatically modify code.

## Notes

- Do not write code; only review.
- If the user asks for code changes, switch to normal development mode (not this skill).
- Review standards are based on Go community conventions + AGENTS.md + `docs/trust-model.md`, not personal preference.