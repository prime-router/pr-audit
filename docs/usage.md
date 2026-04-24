# Usage Guide — L1, L2, L3 Verification

`pr-audit` audits PrimeRouter LLM-gateway responses using a three-tier trust
model. This guide walks through every level end-to-end with copy-paste
commands, expected output, and the exact exit codes you should script
against.

> **Mental model.** Each tier provides strictly stronger evidence than
> the one below it. None of them — not even L3 — proves PrimeRouter is
> "honest"; they only prove that specific kinds of misbehaviour did
> not happen for *this* response. Read [`trust-model.md`](./trust-model.md)
> for the underlying threat model.

---

## Table of contents

- [0. Install](#0-install)
- [1. Capture a response](#1-capture-a-response)
- [2. L1 — Local self-consistency](#2-l1--local-self-consistency)
- [3. L2 — External attestation](#3-l2--external-attestation)
- [4. L3 — End-to-end replay](#4-l3--end-to-end-replay)
- [5. Per-vendor L3 notes](#5-per-vendor-l3-notes)
- [6. JSON output for CI](#6-json-output-for-ci)
- [7. Exit code reference](#7-exit-code-reference)
- [8. Troubleshooting](#8-troubleshooting)

---

## 0. Install

Build from source (requires Go 1.22+):

```bash
git clone https://github.com/primerouter/pr-audit.git
cd pr-audit
make build              # produces ./pr-audit
./pr-audit --help       # confirms verify + replay subcommands are wired in
```

> **Common pitfall.** `go run cmd/pr-audit/main.go -h` only compiles
> the entry file, so the `verify` and `replay` subcommands appear
> missing. Always either `make build` or `go run ./cmd/pr-audit ...`
> (note the directory path, not the file path).

---

## 1. Capture a response

`pr-audit` is **offline** — it never calls PrimeRouter on your behalf.
You first save a real PrimeRouter response to disk, then audit the
saved file. Two equally supported layouts:

### 1a. Combined headers + body (`curl -i`)

```bash
curl -i -X POST 'https://www.primerouter.xyz/v1/chat/completions' \
  -H "Authorization: Bearer $PRIMEROUTER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "hello"}]
  }' \
  -o response.txt
```

### 1b. Split headers and body (`curl -D ... -o ...`)

```bash
curl -X POST 'https://www.primerouter.xyz/v1/chat/completions' \
  -H "Authorization: Bearer $PRIMEROUTER_KEY" \
  -H 'Content-Type: application/json' \
  -d @request.json \
  -D headers.txt -o body.json
```

For L3 you must **also** save the request body byte-for-byte as
`request.json` so replay can reissue the exact same prompt against the
upstream vendor:

```bash
cat > request.json <<'JSON'
{
  "model": "gpt-4o-mini",
  "messages": [{"role": "user", "content": "hello"}]
}
JSON
```

The same `request.json` works for both L1 (ignored) and L3 (mandatory).

---

## 2. L1 — Local self-consistency

**What it proves:** PrimeRouter's declared `x-upstream-sha256` matches
the body bytes it actually delivered to you. Rules out accidental
corruption and one obvious class of tampering.

**What it does NOT prove:** that PrimeRouter did not first alter the
upstream body and *then* recompute the hash. L1 is the floor, not the
ceiling.

### Run

```bash
# combined mode
./pr-audit verify --response response.txt

# split mode
./pr-audit verify --headers headers.txt --body body.json
```

### Expected output (pass)

```text
pr-audit v0.1.0

[L1 · Self-consistency checks]
[✓] Evidence headers present
    resolved_vendor: openai
    x-upstream-sha256: sha256:e86eb651...
    x-upstream-trace-id: req_01HZ...
    x-upstream-vendor: openai
[✓] Body SHA256 matches declared value
    computed: sha256:e86eb651...
    declared: sha256:e86eb651...
[✓] Usage field parsed from body
    completion_tokens: 1
    prompt_tokens: 8
    total_tokens: 9

Result: SELF-CONSISTENT
  PrimeRouter's declared hash matches the body it delivered.
  This rules out accidental corruption, but does NOT prove the body
  equals what the upstream vendor actually returned.

  vendor=openai  model=gpt-4o-mini  trace-id=req_01HZ...

⚠  To obtain stronger evidence:

  [L2 · External attestation]
    Open the vendor dashboard and confirm this request exists:
      https://platform.openai.com/logs?request_id=req_01HZ...

Exit code: 0 (L1 passed)
```

### What each L1 check means

| Check | Meaning when `[✓]` | Meaning when `[✗]` |
|---|---|---|
| `Evidence headers present` | All three `x-upstream-*` headers shipped | Server-side auditing is partially / not enabled — output stays exit 0 with `L1 UNAVAILABLE` |
| `Body SHA256 matches declared value` | Locally-computed sha256 equals the `x-upstream-sha256` header | **Tampering or corruption.** Stop and investigate; do **not** treat as a transient error |
| `Usage field parsed from body` | Usage block is well-formed | Body is malformed JSON or doesn't contain a usage field |

### Failure modes

- **`Result: L1 FAIL` (exit 10)** — the body bytes do not match the
  declared hash. Preserve the file, then escalate to L2 (manual) and
  L3 (automated) before concluding it was malicious.
- **`Result: L1 UNAVAILABLE` (exit 0)** — `x-upstream-sha256` was not
  emitted at all. Not a tampering signal; just means PrimeRouter has
  not enabled per-response attestation for this account/route.

---

## 3. L2 — External attestation

**What it proves:** the trace-id PrimeRouter advertised actually exists
in the upstream vendor's own logs. This is the only tier that uses
*evidence the gateway cannot fabricate*, because the dashboard is owned
by the vendor, not PrimeRouter.

**What it does NOT prove:** that the response body you received equals
the body the vendor returned for that trace.

### How to use it

L2 is intentionally a **manual** step — there is no `pr-audit l2`
command. Both `verify` and `replay` print the dashboard URL for you in
their `⚠ To obtain stronger evidence:` block. Click it (or copy-paste)
and confirm:

1. The trace ID exists in the vendor's request log.
2. The timestamp matches when you made the call.
3. The model field on the dashboard equals what PrimeRouter reported.
4. (Bonus, vendor-dependent) The `prompt_tokens` on the dashboard
   matches PrimeRouter's `usage`.

### Vendor dashboard URL templates

| Vendor | Dashboard URL pattern |
|---|---|
| OpenAI | `https://platform.openai.com/logs?request_id=<trace-id>` |
| Anthropic | `https://console.anthropic.com/dashboard` (filter by request id) |
| Gemini | `https://aistudio.google.com/app/usage` |
| Zhipu / DeepSeek / Moonshot | vendor-specific console (no deep link) |

Unknown vendors fall back to a generic *"look up the trace-id in your
upstream vendor's console"* hint instead of a URL.

---

## 4. L3 — End-to-end replay

**What it proves:** when given the original `request.json` and your own
upstream API key, replaying the request against the vendor produces the
**same `prompt_tokens` and the same `model`** that PrimeRouter reported.
Mismatch is the canonical signal of silent model downgrade or token
inflation.

**What it does NOT prove:** that the assistant's *text* is unchanged —
LLM completions are non-deterministic, so byte-level diffs are not
possible.

### Quickstart — OpenAI (no vendor key required)

OpenAI uses **offline `tiktoken`** reconciliation, so plain-text chat
needs no API key:

```bash
./pr-audit replay \
  --response response.txt \
  --request  request.json
```

### Quickstart — Anthropic / Gemini (vendor key required)

Both use the vendor's `count_tokens` / `countTokens` endpoint, which
needs your own key. The key is passed via flag or env var:

```bash
# flag form (best for one-off runs)
./pr-audit replay \
  --response response.txt \
  --request  request.json \
  --vendor-key sk-ant-xxx

# env form (best for shells & CI; keeps the secret out of `history`)
export PR_AUDIT_VENDOR_KEY=sk-ant-xxx
./pr-audit replay --response response.txt --request request.json
```

> **Vendor key safety guarantees**
> - The key is sent only to hard-coded vendor domains
>   (`api.openai.com`, `api.anthropic.com`,
>   `generativelanguage.googleapis.com`).
> - The key is **never** sent to PrimeRouter.
> - The key is **never** rendered in human or JSON output. The
>   `model.Result` type does not even have a field for it.

### Expected output (pass)

```text
[L3 · End-to-end replay]
    strategy: tiktoken_offline
[✓] L3 strategy ready
    tiktoken offline (OpenAI plain text)
[✓] prompt_tokens matches reported value
    computed: 8
    reported: 8
[✓] model field matches request
    request: gpt-4o-mini
    response: gpt-4o-mini

Result: NO EVIDENCE OF TAMPERING
  L1 self-consistency held AND L3 replay reconciled deterministic
  fields (prompt_tokens, model) against the upstream vendor.
  This is the strongest verdict pr-audit can produce; it does NOT
  prove the assistant text itself is uncensored or unchanged.

Exit code: 0 (L1 + L3 reconciled)
```

### Expected output (token mismatch)

```text
[✗] prompt_tokens DOES NOT match reported value
    prompt_tokens mismatch: computed 8, reported 999 (diff +991)
    computed: 8
    difference: 991
    reported: 999

Result: L3 FAIL
  Replay against the upstream vendor produced a different
  prompt_tokens or model than PrimeRouter reported. This is a
  tampering / mis-routing signal — stop and investigate.

Exit code: 40 (L3 failed — see details above)
```

### Replay verdict cheat sheet

| Verdict | Exit | Meaning |
|---|---|---|
| `NO EVIDENCE OF TAMPERING` | 0 | L1 + L3 both reconciled. Strongest verdict possible. |
| `L3 SKIPPED` | 0 | Vendor not supported, or key missing for a key-required vendor. L1 verdict still stands. |
| `L3 DEGRADED` | 0 | Only structural fields (e.g. `model`) reconciled; tokens not hard-checked. Triggered by tools/multimodal requests or transient upstream API failures. |
| `L1 FAIL` | 10 | Body hash mismatch — L3 was **not** attempted. |
| `PARSE ERROR` | 20 | Missing/malformed `request.json` or bad flags. |
| `L3 FAIL` | 40 | `prompt_tokens` or `model` differs from the vendor's own answer. |
| `(network error)` | 31/32/33 | DNS / TLS / timeout while reaching the vendor. |

---

## 5. Per-vendor L3 notes

L3 routing is automatic based on `x-upstream-vendor` (or, as a
fallback, the response body's `model` field).

### OpenAI / Azure-OpenAI — `tiktoken_offline`

- No vendor key required for plain-text chat.
- BPE files are embedded in the binary; `pr-audit` never downloads
  them at runtime.
- **Tools / function-calling requests automatically degrade to
  `structural`** because OpenAI applies private overhead to tool
  definitions that `tiktoken` cannot reproduce. You will see a lower-
  bound estimate in the output and `model_match` is still hard-checked.
- Multimodal content (images, audio) contributes 0 tokens at the text
  layer and is therefore also a degraded path.

### Anthropic — `count_tokens_api`

- Calls `POST https://api.anthropic.com/v1/messages/count_tokens`.
- **Critical correctness rule:** when prompt caching is enabled, the
  reconciliation MUST sum
  `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`
  before comparing against `count_tokens`. `pr-audit` does this for
  you; if you ever extend the code, double-check this is preserved.
- Sends `anthropic-version: 2023-06-01`.

### Gemini — `count_tokens_api`

- Calls `POST https://generativelanguage.googleapis.com/v1beta/models/{model}:countTokens?key=<KEY>`.
- Authenticates via the `?key=` URL parameter (Gemini's standard).
- Reconciles `totalTokens` against PrimeRouter's reported `prompt_tokens`
  (or `input_tokens` if the body uses Anthropic-style fields).
- OpenAI-shaped messages are auto-converted to Gemini's `contents`
  format (`system` → `user` with prefix, `assistant` → `model`).

### Zhipu / DeepSeek / Moonshot — `skipped`

- These vendors do not currently expose a `count_tokens` endpoint and
  there is no offline tokenizer in the binary.
- `pr-audit` returns `L3 SKIPPED` with exit 0; the L1 verdict and L2
  dashboard hint still apply.

---

## 6. JSON output for CI

Both subcommands accept `--format json` and emit a stable schema:

```bash
./pr-audit verify --response response.txt --format json
./pr-audit replay --response response.txt --request request.json --format json
```

Top-level keys you can rely on:

| Key | Type | Notes |
|---|---|---|
| `version` | string | Output schema version (currently `0.1.0`) |
| `command` | string | `verify` or `replay` |
| `result` | string | One of `self_consistent`, `l1_unavailable`, `l1_fail`, `parse_error`, `no_evidence_of_tampering`, `l3_fail`, `l3_skipped`, `l3_degraded` |
| `exit_code` | number | Same as the process exit code |
| `trust_level_reached` | string | `none`, `L1_unavailable`, `L1_self_consistent`, `L1_fail`, `L3_no_evidence_of_tampering`, `L3_fail`, `L3_skipped`, `L3_degraded` |
| `vendor`, `model`, `trace_id` | string | Resolved metadata |
| `usage` | object | Parsed token counts from the body |
| `checks` | array | L1 checks |
| `l3_strategy`, `l3_checks` | string / array | **Replay only.** Verify never emits these keys (they are `omitempty`). |
| `next_steps` | array | L2 dashboard hints |

### Example: gate a CI step on L3 result

```bash
out=$(./pr-audit replay --response r.txt --request req.json --format json)
case $(echo "$out" | jq -r .result) in
  no_evidence_of_tampering|self_consistent|l3_skipped|l3_degraded) exit 0 ;;
  l3_fail|l1_fail) echo "$out" >&2; exit 1 ;;
  *) echo "unexpected: $out" >&2; exit 2 ;;
esac
```

---

## 7. Exit code reference

| Code | Where | Meaning |
|---|---|---|
| `0` | both | L1 passed, or L1 + L3 reconciled, or L3 skipped/degraded |
| `10` | both | L1 hash mismatch — `replay` returns this and skips L3 |
| `20` | both | Input parse error (missing files, bad JSON, bad flags) |
| `31` | replay | L3 network: DNS resolution failed |
| `32` | replay | L3 network: TLS handshake / certificate failed |
| `33` | replay | L3 network: upstream timeout or 5xx |
| `40` | replay | L3 fail — prompt_tokens or model mismatch |
| `99` | both | Internal error (file the bug) |

---

## 8. Troubleshooting

**`./pr-audit replay -h` shows the long description but no flags.**
You ran `go run cmd/pr-audit/main.go` instead of `make build` or
`go run ./cmd/pr-audit`. Subcommand registration is in
`verify.go` / `replay.go` and only links when you compile the
*package*, not the single file.

**`Result: L1 UNAVAILABLE` even though the request succeeded.**
PrimeRouter did not emit `x-upstream-sha256` for this route/account.
Confirm with PrimeRouter that response attestation is enabled. This is
exit 0 by design — absence of evidence is not evidence of tampering.

**`prompt_tokens mismatch` on OpenAI by 1 or 2 tokens.**
Check whether your request includes a `name` field on a message, an
unusual system message, or a multimodal part. The code uses OpenAI's
documented per-message overhead constants
(`tokensPerMessage=3`, `tokensPerName=1`, `primingTokens=3`), which
match the cookbook for GPT-4 / GPT-4o / o1 families. GPT-3.5-turbo-0301
uses a different formula and is out of scope.

**Anthropic `prompt_tokens mismatch` only when prompt caching is on.**
Make sure you are reading the version of `pr-audit` that includes the
three-field cache summation (`input + cache_creation + cache_read`).
Earlier prototypes compared against `input_tokens` alone and produced
false positives on every cache hit.

**Gemini call returns `403`.**
Verify your `PR_AUDIT_VENDOR_KEY` is a Google AI Studio key (not a
GCP service-account key) and has the *Generative Language API*
enabled.

**`L3 DEGRADED` even though my request looks plain.**
For OpenAI this means `pr-audit` detected a non-empty `tools` array.
Either remove tools from the saved request, or accept the
degraded verdict — `model_match` is still hard-checked and that alone
catches the most important class of silent downgrade.

**Network errors in CI but works locally.**
Exit codes `31/32/33` distinguish DNS / TLS / timeout. CI runners
sometimes block egress to vendor APIs; run L3 only in environments
that can reach the upstream vendor, and fall back to `verify` (L1
only) for hermetic CI lanes.
