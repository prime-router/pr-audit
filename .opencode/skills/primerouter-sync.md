---
name: primerouter-sync
description: Check the impact of current code changes on the PrimeRouter integration contract and generate change notifications. Triggers when user says "primerouter sync", "integration impact", "contract change", "sync primerouter", or "notify primerouter".
---

Check the impact of current code changes on the PrimeRouter integration contract and generate change notifications.

## Input

Code changes on the current git branch (fetched automatically).

## Steps

### 1. Collect Changes

Run in parallel with Bash:
- `git diff main...HEAD --stat` — changed file list
- `git diff main...HEAD` — full diff
- `git log main...HEAD --oneline` — commit history

If the current branch cannot be determined, use `git diff HEAD~1..HEAD` to analyze the latest commit.

### 2. Read Contract Document

Read `docs/primerouter-integration.md` in full as the contract baseline.

### 3. Item-by-Item Impact Check

For each changed file in the diff, check against the mapping table below to see if it hits a PrimeRouter integration point. If it does, record the impact item and the corresponding integration.md section.

#### 3.1 Code Area → Contract Section Mapping

| Code Area | Change Types to Monitor | Action When Hit | integration.md Section |
|---|---|---|---|
| `internal/vendor/detect.go` vendor constants (L16-23) | Add/delete/rename vendor constants | Notify PrimeRouter to sync `x-upstream-vendor` allowed values list + update §4.3 enum table + new vendor needs channel→vendor mapping confirmation | §4.3 |
| `internal/vendor/detect.go` `allowed` map (L26-29) | Add/delete keys | Same as above (the whitelist is the authoritative source for client-side validation; must match server-side) | §4.3 |
| `internal/vendor/detect.go` `vendorFromModel()` (L70-94) | Add/modify model prefix matching rules | Notify PrimeRouter of the vendor for new model prefixes + confirm the channel's `x-upstream-vendor` value matches the client-side | §4.3 |
| `internal/verify/verify.go` three header keys (L59,68,104) | Modify header key strings | **Breaking change**: must coordinate synchronized cutover with PrimeRouter | §4.1-4.3 |
| `internal/verify/hash.go` `ParseHashHeader()` (L41-52) | Modify `sha256:` prefix or hex format | **Breaking change**: server-side generation format must be modified in sync | §4.2 |
| `internal/verify/hash.go` algorithm support (L39-40) | Add new algorithm (e.g., `sha3-256`) | Notify PrimeRouter of new algorithm computation method and header prefix + dual-algorithm transition period needed | §4.2, §3 Constraint 1 |
| `internal/verify/verify.go` exit codes (L137,148,156,164,169) | Add/modify exit codes | Notify PrimeRouter (they may integrate pr-audit into CI and depend on exit codes) | — |
| `internal/model/types.go` Outcome enum (L65-68) | Add/modify outcome enum values | Notify PrimeRouter (if their CI parses JSON output) + confirm no `VERIFIED` output | §9 |
| `internal/model/types.go` TrustLevel enum (L54-57) | Add/modify trust levels | Notify PrimeRouter to understand the new level + if L2/L3 involved, PrimeRouter needs to provide corresponding capability | §2 |
| `internal/verify/verify.go` check functions | Add/rename checks | Notify PrimeRouter CI maintainers (if they consume JSON output) | — |
| `internal/verify/usage.go` `ParseUsage()` (L15-63) | Add vendor-specific usage fields | Notify PrimeRouter to confirm the vendor's usage field naming + align with §8 integration test samples | §8 |
| `internal/vendor/dashboard.go` `DashboardURL()` (L11-33) | Add/modify vendor dashboard URLs | Ask PrimeRouter to confirm whether dashboard deep-link format is correct | — |
| `internal/verify/verify.go` `SchemaVersion` (L15) | Bump version number | **Breaking change**: notify PrimeRouter that JSON schema has been upgraded | — |
| SSE verify logic (v0.1.1, not yet implemented) | Any implementation change | Must strictly align with §6 `pr_audit` event format + server-side needs synchronized deployment | §6 |
| `internal/output/human.go` verdict wording (L87-105) | Modify verdict/next-step wording | If wording affects trust commitment semantics, notify PrimeRouter + must never produce `VERIFIED` | §9 |
| `Content-Encoding` handling | Add decompression logic | Must align with §3 Constraint 3 + notify PrimeRouter to confirm `Accept-Encoding: identity` strategy | §3 Constraint 3 |

#### 3.2 High-Risk Change Assessment

The following changes **cannot be pushed unilaterally** and require PrimeRouter to go live in sync:

| Change | Client Impact | Server Impact |
|---|---|---|
| Header name change | Cannot find header → L1 unavailable false positive | Emitted header goes unconsumed |
| Hash header value format change | Parse failure → L1 fail false positive | Generated format unrecognized |
| New hash algorithm | New prefix parsing | New algorithm computation + signing |
| Schema version bump | JSON field changes | CI parsing may break |
| SSE `pr_audit` event launch | Needs to parse new event format | Needs to generate new event |

#### 3.3 Absolute Prohibitions

- Output contains `VERIFIED` (any case) → violates trust-model §3.4
- L1 failure branch outputs NextSteps → violates design constraint
- Output implies L1 can prove honesty (`proved`, `confirmed`, `trusted`, `guaranteed`) → violates trust model

### 4. Generate Impact Report

Output format:

```markdown
# PrimeRouter Integration Impact Report

## Summary
- Branch: {branch}
- Changes: {N} files, +{A} / -{D} lines
- Impact Level: 🟢 No impact / 🟡 Needs notification / 🔴 Needs synchronized launch

## Impact Items

| # | Changed File | Code Area | Impact Type | integration.md Section | Action |
|---|---|---|---|---|---|
| 1 | ... | ... | Breaking/Notification/None | §N | ... |

## High-Risk Changes (Requires PrimeRouter Synchronized Launch)

(Write "None" if none)

| Change | Coordination Method | Suggested Timeline |
|---|---|---|
| ... | ... | ... |

## Notification Template

(If there are notification or high-risk changes, fill in the template below for the user to send directly)

Subject: [pr-audit] Change Notice — {brief description}

Change details:
- Client-side pr-audit will change in v0.x.x: {description}
- Affects integration.md §N: {corresponding contract point}

Breaking assessment:
- [ ] Yes, breaking change (requires synchronized launch)
- [ ] No, backward compatible

PrimeRouter needs to:
- {action}

Planned timeline:
- Client release: YYYY-MM-DD
- Server needs to be ready: YYYY-MM-DD
- Grayscale verification period: ...

Verification method:
- {how to confirm both sides are aligned}

## Prohibition Checks

- [ ] No `VERIFIED` in output
- [ ] No NextSteps in L1 failure branch
- [ ] Output does not imply L1 can prove honesty
```

### 5. If 🟢 No Impact

Briefly inform the user "Current changes do not affect the PrimeRouter integration contract" without generating a full report.

## Notes

- Do not write code; only perform impact analysis.
- If the user asks for code changes, switch to normal development mode (not this skill).
- Line numbers in the mapping table may shift as code evolves; match by file content instead.
- `docs/primerouter-integration.md` is the authoritative contract source; analysis should be based on its actual content.