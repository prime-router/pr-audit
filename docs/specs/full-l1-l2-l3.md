# pr-audit L1+L2+L3 Full Implementation — Functional Requirements Spec

> **Project**: pr-audit — PrimeRouter response integrity audit CLI
> **Version**: v1.0.0 (merging v0.1.0 L1-only + v0.2.0 L3 into a single release)
> **Created**: 2026-04-23
> **Status**: Draft
> **Related documents**:
> - Conceptual layer: [`../trust-model.md`](../trust-model.md) (what can be proven), [`../limitations.md`](../limitations.md) (what cannot be proven)
> - Execution layer: [`full-l1-l2-l3-tasks.md`](./full-l1-l2-l3-tasks.md) (task list)
> - Server-side integration: [`../primerouter-integration.md`](../primerouter-integration.md)
> - Existing design: [`../specs.md`](../specs.md) (v0.1.0 design doc; L3 portion is outdated and needs replacement)

---

## 1. Background

### 1.1 What is pr-audit

pr-audit is an open-source Go CLI used to audit whether [PrimeRouter](https://www.primerouter.xyz/) (an LLM API gateway) "faithfully forwards upstream vendor responses".

**Problem**: PrimeRouter sits between users and upstream LLM vendors (OpenAI / Anthropic / Zhipu / Gemini, etc.). It has the ability to do four things the user cannot detect:

1. Tamper with response content (alter answers)
2. Inflate token counts (overcharge)
3. Not actually forward requests (return cached or self-generated content)
4. Silently downgrade models (claim GPT-4 was called while actually using GPT-3.5)

**Core claim**: Don't take our word for it — verify it yourself.

### 1.2 Three-tier trust progression

pr-audit provides three progressive tiers of verification. **Users should move from L1 toward L2/L3**, rather than stopping at L1:

```
                    validation strength
                           ▲
                           │
                L3 ────────┤  Direct upstream reconciliation with user's key
             (auto)        │  → hard reconciliation of prompt_tokens
                           │  → model field comparison
                           │
                L2 ────────┤  trace-id discoverable in vendor dashboard
             (manual)      │  → request actually reached the upstream
                           │  → usage matches vendor records
                           │
                L1 ────────┤  Local sha256(body) ≡ x-upstream-sha256
             (auto)        │  → PrimeRouter self-consistent (does not prove honesty)
                           │
                None ──────┤  No evidence headers
                           │  → CLI indicates "auditing not enabled"
                           └──>
                               evidence coverage
```

**Core insight: self-consistent ≠ honest**. If PrimeRouter deliberately tampers with the body and then recomputes the SHA256 and updates the header, local hash verification cannot detect it. Real defense depends on L2 + L3.

The following unpacks this core insight — **integration engineers must understand it, otherwise they will make mistakes in wording, logic, and output**.

### 1.2.1 Trust assumptions

pr-audit's entire design rests on these four assumptions. If any is broken, the verification model fails:

| Assumption | Meaning | If it doesn't hold |
|---|---|---|
| **Upstream vendor is trusted** | Responses from OpenAI / Anthropic / Zhipu, etc. are the "truth" baseline | If OpenAI itself tampers with usage, pr-audit cannot detect it at all |
| **User's machine is trusted** | The machine running pr-audit is not compromised and the binary has not been replaced | An attacker replaces pr-audit to always output pass |
| **Hash function is trusted** | SHA256 collision resistance has not been broken | Collision attacks feasible → hash verification completely fails |
| **PrimeRouter is untrusted** | **Design stance**: assume PrimeRouter may tamper with anything it can control | — |

The fourth is a **zero-trust assumption** and is the reason this project exists.

### 1.2.2 Self-consistent ≠ honest (the most critical concept)

**Attack scenario**: suppose PrimeRouter decides to act maliciously — inflating the token count to overcharge:

1. Receives the upstream raw response body, `prompt_tokens: 42`
2. Tampers with body: `prompt_tokens: 420` (10x inflation)
3. **Recomputes SHA256 over the tampered body**
4. Writes the new hash into the `x-upstream-sha256` header
5. Sends to the user

User runs `pr-audit verify`: local `sha256(body)` == declared value in header → **L1 passes**.

This is an "internally consistent forgery" — L1 cannot detect it.

**Strict distinction**:

| Concept | Definition | L1 can prove? |
|---|---|---|
| **Self-consistent** | The hash PrimeRouter declared matches the body it delivered | ✓ |
| **Honest** | The body PrimeRouter delivered equals the body the upstream actually returned | ✗ |

Self-consistent = "what PrimeRouter says matches what it does". Honest = "what PrimeRouter does matches what actually happened upstream". L1 only checks self-consistency, not honesty.

**L1's real value** (insufficient but meaningful):
- Detects transmission corruption / implementation bugs (hash computed but body rewritten again)
- Serves as the carrier of a "declared stance" (publishing a hash = accepting scrutiny)
- **Precondition for L2/L3** (without L1, the body cannot serve as the baseline for subsequent reconciliation)

So L1 is a **necessary, not sufficient, condition**.

### 1.2.3 What each of the three tiers can prove

| Tier | Can prove | Cannot prove |
|---|---|---|
| **L1 (self-consistent)** | The hash PrimeRouter declared matches the body it delivered | PrimeRouter didn't tamper with the original upstream response |
| **L2 (externally discoverable)** | The request actually reached the upstream vendor; vendor-recorded usage/model matches the response | The response body contents match upstream (dashboards don't display the full body) |
| **L3 (end-to-end)** | prompt_tokens matches the vendor's own computation (hard evidence); model matches (prevents downgrade) | Byte-exact response body (LLM non-determinism); exact match of completion_tokens |

### 1.2.4 Attack thought experiment

**Scenario**: PrimeRouter inflates `prompt_tokens: 42 → 420`

| Tier | Reaction | Result |
|---|---|---|
| L1 | PrimeRouter tampers with body and recomputes hash; user's `sha256sum` matches | ❌ passes (L1 was never meant to defend against this) |
| L2 | User searches trace-id on OpenAI dashboard and sees prompt_tokens=42, which doesn't match 420 | ✓ detected (but manual) |
| L3 | Local tiktoken recomputes to 42 and compares against PrimeRouter's reported 420 | ✓ auto-detected |

**Conclusion**: L1 passing ≠ no cheating. CLI output must guide users from L1 toward L2/L3.

### 1.2.5 L3's "online ≠ goes through PrimeRouter"

The core of L3 is that **the request path does not go through PrimeRouter**, not "completely offline".

Using the user's own key to call Anthropic's `count_tokens` endpoint directly is **fully equivalent in trust-chain terms** to replaying via the chat endpoint — both anchor on the upstream vendor and neither passes through PrimeRouter. The only differences are that count_tokens is cheaper and faster.

**Implementation constraint**: the replay command's network requests **must not** be sent to PrimeRouter or any non-upstream-vendor domain. The hard-coded API URLs contain only `api.anthropic.com`, `generativelanguage.googleapis.com`, etc.

### 1.2.6 Limits of the trust model

Even if all trust assumptions hold, pr-audit cannot detect the following:

| Cannot detect | Reason |
|---|---|
| PrimeRouter returning different responses to different users | A single-user perspective cannot see what others received; requires a transparency log (v2.0+) |
| Exact value of completion_tokens | LLM non-determinism: same params, two calls yield different output |
| OpenAI tool calls / multimodal prompt_tokens | Private overhead token rules are undocumented; tiktoken cannot compute accurately |
| Tokenizer version drift | The local tiktoken may lag behind the OpenAI server-side version, causing false positives |
| Vendor API rate limiting | Anthropic/Gemini count_tokens have independent rate limits; bulk auditing hits the wall |

### 1.2.7 CLI output wording constraints (hard rules)

| Scenario | Output | Forbidden output |
|---|---|---|
| L1 pass | `SELF-CONSISTENT` | ~~`VERIFIED`~~, ~~`PASS`~~, ~~`TRUSTED`~~ |
| L1+L3 pass | `NO EVIDENCE OF TAMPERING` | ~~`VERIFIED`~~, ~~`100% HONEST`~~ |
| L1 fail | `L1 FAIL` | Do not suggest next steps (user should stop and investigate) |
| L1 unavailable | `L1 UNAVAILABLE` | Not a failure |

**The word `VERIFIED` implies "fully trusted", which contradicts the trust model. An L3 pass can only prove "no tampering evidence found in deterministic fields", not that PrimeRouter is absolutely honest. This is the project's core integrity stance and must not be used in any code, docs, or output.**

### 1.3 Current implementation status (v0.1.0)

| Feature | Status |
|---|---|
| `verify` command (L1 local hash + L2 dashboard URL hints) | ✅ implemented |
| HTTP response file parsing (`curl -D` + `-o` split mode, `curl -i` combined mode) | ✅ implemented |
| Presence check + display of the three `x-upstream-*` headers | ✅ implemented |
| L1 hash verification (SHA256 body ≡ x-upstream-sha256) | ✅ implemented |
| Graceful degradation on missing headers (`L1 unavailable`, exit 0) | ✅ implemented |
| `usage` field parsing + display (including Anthropic cache fields) | ✅ implemented |
| trace-id + vendor dashboard URL generation (L2 hint) | ✅ implemented |
| Human-readable + `--format json` dual output | ✅ implemented |
| Vendor detection (`x-upstream-vendor` first, fallback to body `model` heuristic) | ✅ implemented |
| `replay` command + L3 reconciliation | ❌ not implemented |
| cobra CLI skeleton | ❌ not implemented (currently uses `flag` + `os.Args` switch) |
| OpenAI tiktoken offline reconciliation | ❌ not implemented |
| Anthropic/Gemini count_tokens online reconciliation | ❌ not implemented |

### 1.4 Goals of this work

Upgrade pr-audit from "L1 local hash verification + L2 hints only" to a CLI tool that fully implements "L1 + L2 + L3 three-tier trust":

1. **Introduce cobra** to replace the hand-written `flag` + `switch`, paving the way for future subcommands such as `proxy`
2. **Add the `replay` command** implementing full end-to-end L3 reconciliation
3. **OpenAI tiktoken offline reconciliation**: plain-text requests can hard-reconcile prompt_tokens locally
4. **Anthropic count_tokens online reconciliation**: direct vendor-API reconciliation, including prompt-cache summation
5. **Gemini countTokens online reconciliation**: direct vendor-API reconciliation
6. **Degraded / skipped paths**: tools/multimodal/unsupported vendors have explicit output
7. **Remove the gocloc line-count limit**: L3 functionality exceeds the 2000-line cap; drop the CI restriction

### 1.5 Non-goals

- SSE streaming response support (later iteration; design in `specs.md` §2.5)
- `proxy` proxy mode (later iteration)
- Transparency Log (v2.0+ independent project)
- WebAssembly build (long term)
- Batch verification, CI-friendly output templates (long term)
- Zhipu/DeepSeek/Moonshot L3 reconciliation (these vendors have no count_tokens endpoint; SKIPPED for now)

---

## 2. User Stories

### Story 1: L1 self-consistency check (existing, retained)

**Role**: developer who calls an LLM through PrimeRouter

**Scenario**: I just called GPT-4o-mini via PrimeRouter and got a response. I want to quickly confirm that the body PrimeRouter delivered matches its declared SHA256.

**Action**:
```bash
pr-audit verify --headers headers.txt --body body.bin
```

**Expected result**: CLI outputs `SELF-CONSISTENT` (L1 pass) and prompts me with L2/L3 next steps.

### Story 2: L2 vendor dashboard hint (existing, retained)

**Role**: developer who is suspicious about a particular request

**Scenario**: I want to confirm that this request really reached the upstream vendor (PrimeRouter didn't just pretend to forward it).

**Action**: look at the L2 section of the verify output, open the dashboard URL, and search for the trace-id.

**Expected result**: find this request on the OpenAI Platform Logs page.

### Story 3: L3 end-to-end replay reconciliation (new)

**Role**: developer who needs the strongest verification

**Scenario**: I suspect PrimeRouter inflated prompt_tokens. I want to use my own OpenAI key to connect directly upstream and compare PrimeRouter's reported value with the value the upstream actually computes.

**Action**:
```bash
pr-audit replay \
  --headers headers.txt --body body.bin \
  --request request.json \
  --vendor-key $MY_OPENAI_KEY
```

**Expected result**:
- L1 passes first (self-consistency is a precondition for L3)
- L3 automatically selects tiktoken offline reconciliation (OpenAI plain text)
- prompt_tokens matches → output `NO EVIDENCE OF TAMPERING`
- prompt_tokens doesn't match → output `L3 FAIL`, report the diff value

### Story 4: OpenAI offline token reconciliation (new)

**Role**: OpenAI plain-text chat user

**Scenario**: I don't want to make an extra OpenAI API call (to save quota and latency), but I want to verify prompt_tokens.

**Expected result**: after detecting an OpenAI plain-text request, the replay command locally recomputes prompt_tokens using tiktoken; no network request is needed to hard-reconcile.

### Story 5: Anthropic/Gemini online token reconciliation (new)

**Role**: Anthropic/Gemini user

**Scenario**: Anthropic and Gemini don't have offline-usable tokenizers. I need pr-audit to use my key to call the vendor's count_tokens endpoint to reconcile.

**Action**:
```bash
pr-audit replay \
  --headers h.txt --body b.bin \
  --request request.json \
  --vendor-key $MY_ANTHROPIC_KEY
```

**Expected result**:
- Anthropic: call `/v1/messages/count_tokens`; the return value should equal `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` (sum of the three; omitting the sum causes false positives)
- Gemini: call the `countTokens` API and compare totalTokens

---

## 3. Core flows

### 3.1 verify command (L1 + L2, largely unchanged)

```
User input                   pr-audit verify                        Output
─────────                    ─────────────                          ─────
headers.txt ─────┐
                  ├──→ parse input ──→ vendor detect ──→ SHA256 compute
body.bin ─────────┘                                         │
                                                              ├──→ L1 compare
response.txt ────→ parse combined ──→ vendor detect ──→ SHA256 ──┘  │
                                                              │
                                                     ┌────────┘
                                                     │
                                                     ├──→ L1 verdict (SELF-CONSISTENT / L1 FAIL / L1 UNAVAILABLE)
                                                     │
                                                     ├──→ parse usage / model / trace-id
                                                     │
                                                     └──→ L2 hint (dashboard URL)
```

### 3.2 replay command (L1 + L2 + L3, new)

```
User input                       pr-audit replay                              Output
─────────                        ──────────────                               ─────
headers.txt ─────┐
body.bin ────────┤
                 ├──→ [L1] reuse verify logic ──→ L1 verdict
response.txt ────┘         │
                           │  L1 fail? → stop, do not continue to L3
request.json ──────────────┤
vendor-key ────────────────┤
                           │  L1 pass / unavailable
                           │
                           ├──→ parse request.json
                           │
                           ├──→ vendor detect ──→ L3 strategy routing
                           │     │
                           │     ├── openai (plain text) ──→ tiktoken offline reconciliation
                           │     ├── openai (tools/multimodal) ──→ degraded structural check
                           │     ├── anthropic ──→ count_tokens API (incl. cache summation)
                           │     ├── gemini ──→ countTokens API (format conversion)
                           │     ├── zhipu/deepseek/moonshot ──→ L3 SKIPPED
                           │     └── unknown ──→ L3 SKIPPED
                           │
                           ├──→ [L3] compare prompt_tokens / model
                           │
                           ├──→ [L2] dashboard URL (always output)
                           │
                           └──→ final verdict + exit code
```

**Key design decisions**:

1. **L1 is a precondition of L3**: if L1 fails (hash mismatch), the body has been tampered with and subsequent reconciliation loses its baseline, so L3 is not performed
2. **L3 may still be attempted when L1 is unavailable**: missing `x-upstream-sha256` is not a failure; the user may still want to verify via L3
3. **L2 is always output**: whether or not L3 runs, the dashboard URL is always displayed, because L2 and L3 provide different dimensions of evidence
4. **vendor key never leaves the local machine**: pr-audit uses the key only to connect directly to the upstream vendor; it does not go through PrimeRouter and is not uploaded

### 3.3 L1 is a precondition of L3 (why L3 is skipped when L1 fails)

If L1 fails (hash mismatch), it means the body in the user's hands does not match the body PrimeRouter declared. At this point the body is no longer a reliable baseline for reconciliation — even if L3 reconciliation comes out "matching", it proves nothing, because the starting point (body) is already contaminated.

Therefore, when L1 fails, the replay command **does not continue to L3** and returns `L1 FAIL` directly.

Conversely, when L1 is unavailable (headers missing), L3 can still be attempted — because missing evidence ≠ absent evidence; the user may still want to verify prompt_tokens and model via L3.

### 3.4 L3 reconciliation strategy routing (core decision tree)

```
detect vendor (from x-upstream-vendor header or body model heuristic)
  │
  ├── openai
  │   │
  │   ├── request.json has a non-empty "tools" field?
  │   │   └── YES → L3 DEGRADED
  │   │       Reason: OpenAI has private overhead token rules for tool calls,
  │   │       tiktoken cannot compute them; only a lower-bound estimate is possible
  │   │       prompt_tokens check: StatusSkip
  │   │       model check: runs normally (not tokenizer-dependent)
  │   │
  │   └── NO → tiktoken offline computation
  │       1. tiktoken.EncodingForModel(model)
  │       2. Encode each message's content → compute token counts
  │       3. Sum + message overhead
  │       4. Compare with reported.PromptTokens
  │       5. prompt_tokens check: pass / fail
  │       6. model check: pass / fail
  │
  ├── anthropic
  │   │
  │   └── call /v1/messages/count_tokens API
  │       1. POST https://api.anthropic.com/v1/messages/count_tokens
  │       2. Headers: x-api-key, anthropic-version: 2023-06-01
  │       3. Request body: {model, messages, ...} (same format as /v1/messages)
  │       4. Response: {"input_tokens": N}
  │       5. [KEY] cache summation:
  │          expected = reported.InputTokens
  │                   + reported.CacheCreationInputTokens
  │                   + reported.CacheReadInputTokens
  │          compare count_tokens_result == expected
  │       6. Omitting the sum → any cached request will be falsely reported as "PrimeRouter over-reporting"
  │       7. API unavailable → degrade to structural check + warn
  │
  ├── gemini
  │   │
  │   └── call countTokens API
  │       1. POST https://generativelanguage.googleapis.com/v1beta/models/{model}:countTokens?key={vendorKey}
  │       2. Request body: {contents: [...]} (requires OpenAI→Gemini format conversion)
  │       3. Response: {"totalTokens": N}
  │       4. Compare totalTokens with the reported value
  │       5. API unavailable → degrade
  │
  ├── azure-openai
  │   └── L3 DEGRADED (not yet supported; Azure's base URL differs; later iteration)
  │
  ├── zhipu / deepseek / moonshot
  │   └── L3 SKIPPED (no count_tokens endpoint, no local tokenizer)
  │
  └── unknown
      └── L3 SKIPPED (vendor cannot be determined → tokenizer cannot be chosen)
```

---

## 4. API contracts

### 4.1 PrimeRouter response Evidence Headers (existing, not modified)

| Header | Format | Example | Source |
|---|---|---|---|
| `x-upstream-sha256` | `sha256:` + 64-char lowercase hex | `sha256:6adafd0c9e64fa...` | Computed by PrimeRouter over the upstream body |
| `x-upstream-trace-id` | string (raw upstream ID) | `chatcmpl-ABC123...` | Upstream response's `id` or `x-request-id` |
| `x-upstream-vendor` | lowercase enum | `openai` / `anthropic` / `zhipu` / `gemini` / `deepseek` / `moonshot` / `unknown` | Mapped by PrimeRouter from channel type |

### 4.2 Anthropic count_tokens API (new for L3)

**Endpoint**: `POST https://api.anthropic.com/v1/messages/count_tokens`

**Request**:
```json
{
  "model": "claude-sonnet-4-20250514",
  "messages": [
    {"role": "user", "content": "Hello, world"}
  ]
}
```

**Request headers**:
```
x-api-key: sk-ant-api03-xxxxx
anthropic-version: 2023-06-01
content-type: application/json
```

**Response**:
```json
{
  "input_tokens": 42
}
```

**Anthropic prompt cache summation formula (the most critical implementation detail)**:

When Anthropic prompt caching is enabled, the usage field in the body returned by PrimeRouter is split into three fields:
- `input_tokens`: the portion that didn't hit the cache
- `cache_creation_input_tokens`: cache created this time
- `cache_read_input_tokens`: cache hits

The `count_tokens` endpoint returns the "total without cache". **The correct reconciliation formula**:

```
count_tokens_result = input_tokens + cache_creation_input_tokens + cache_read_input_tokens
```

**Consequences of not summing**: any request that hits the cache will be falsely reported as "PrimeRouter inflated input_tokens" — because count_tokens returns 1066 while the response's input_tokens is only 42, but the inequality is not inflation.

**Degenerate case without cache**: `cache_creation = 0, cache_read = 0`; the formula degenerates to `count_tokens = input_tokens`, naturally correct.

### 4.3 Gemini countTokens API (new for L3)

**Endpoint**: `POST https://generativelanguage.googleapis.com/v1beta/models/{model}:countTokens?key={vendorKey}`

**Request**:
```json
{
  "contents": [
    {"role": "user", "parts": [{"text": "Hello, world"}]}
  ]
}
```

**Response**:
```json
{
  "totalTokens": 42
}
```

**OpenAI → Gemini format conversion**:

This is the most complex adapter. The body returned by PrimeRouter is in OpenAI format (`messages: [{role, content}]`), but Gemini's countTokens API requires the Gemini format (`contents: [{role, parts: [{text}]}]`).

Conversion rules:

| OpenAI | Gemini |
|---|---|
| `role: "system"` | `role: "user"` + add `("system" prefix)` or map to the `systemInstruction` field |
| `role: "user"` | `role: "user"` |
| `role: "assistant"` | `role: "model"` |
| `content: "text"` | `parts: [{text: "text"}]` |
| `content: [{type:"text", text:"..."}]` | `parts: [{text: "..."}]` |
| `content: [{type:"image_url", image_url:{...}}]` | `parts: [{inlineData: {mimeType, data}}]` |

**MVP scope**: only plain-text chat conversion is supported. Multimodal (images) is deferred to a later iteration.

### 4.4 OpenAI tiktoken offline reconciliation (new for L3)

**No API call**. Computed locally with tiktoken-go.

**Flow**:
1. Pick the encoding by model name (`tiktoken.EncodingForModel("gpt-4o-mini")` → `cl100k_base` or `o200k_base`)
2. Encode each message's content → compute token counts
3. Add message overhead (each message has a formatting-token overhead)
4. Sum to obtain prompt_tokens
5. Compare with PrimeRouter's reported `usage.prompt_tokens`

**Message overhead computation details**:

tiktoken only counts the tokens of the text itself; it does not include OpenAI API's message-format overhead. Per OpenAI's official cookbook:

```
tokens_per_message = 3  (each message has a fixed 3-token overhead: <|start|>{role}\n{content}<|end|>)
tokens_per_name = 1    (if the message has a name field, +1 token)
total_overhead = 3     (each conversation has a fixed 3-token overhead: <|start|>assistant\n)
```

The `CountMessageTokens` method of the tiktoken-go library **may already handle the overhead**. During implementation, verify this: manually compute the tokens for a known prompt and compare against OpenAI's reported prompt_tokens to confirm whether the overhead matches.

**Degraded scenarios**:

When request.json contains a `tools` field, OpenAI has private overhead token rules (tool-definition token counts are not in the public docs), which tiktoken cannot compute accurately. In this case:
- `prompt_tokens` check: `StatusSkip`, give a lower-bound estimate
- `model` check: runs normally (not tokenizer-dependent)

### 4.5 request.json format

The raw request JSON saved by the user — the request body sent to PrimeRouter:

```json
{
  "model": "gpt-4o-mini",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "What is 1+1?"}
  ],
  "temperature": 0.1,
  "stream": false
}
```

Request containing tools:
```json
{
  "model": "gpt-4o-mini",
  "messages": [
    {"role": "user", "content": "What is the weather in Tokyo?"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
      }
    }
  ]
}
```

**How to obtain**: the user saves the request JSON when calling PrimeRouter. SDK snippet example (Python):

```python
import json, httpx
from openai import OpenAI

request_payload = {
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "hi"}],
    "temperature": 0
}

def audit_hook(response: httpx.Response):
    response.read()
    with open("/tmp/pr-audit-last.bin", "wb") as f:
        meta = {"headers": dict(response.headers), "status_code": response.status_code}
        f.write(json.dumps(meta).encode() + b"\n\n")
        f.write(response.content)

client = OpenAI(
    base_url="https://www.primerouter.xyz/v1",
    http_client=httpx.Client(event_hooks={"response": [audit_hook]}),
)
response = client.chat.completions.create(**request_payload)

# Save the request JSON for replay
with open("/tmp/pr-audit-request.json", "w") as f:
    json.dump(request_payload, f)
```

---

## 5. CLI command design

### 5.1 Existing verify command (cobra refactor, functionality unchanged)

```bash
# Split-file mode (recommended; from curl -D headers.txt -o body.bin)
pr-audit verify --headers headers.txt --body body.bin

# Combined-file mode (from curl -i)
pr-audit verify --response response.txt

# JSON output
pr-audit verify --headers headers.txt --body body.bin --format json
```

### 5.2 New replay command

```bash
# Split-file mode + request file
pr-audit replay \
  --headers headers.txt \
  --body body.bin \
  --request request.json \
  --vendor-key sk-xxxxx

# Combined-file mode + request file
pr-audit replay \
  --response response.txt \
  --request request.json \
  --vendor-key sk-xxxxx

# vendor-key can also be passed via environment variable (flag takes priority)
export PR_AUDIT_VENDOR_KEY=sk-xxxxx
pr-audit replay --headers h.txt --body b.bin --request req.json

# JSON output
pr-audit replay --headers h.txt --body b.bin --request req.json --vendor-key sk-xxx --format json
```

**Flag descriptions**:

| Flag | Required? | Description |
|---|---|---|
| `--headers` | one of two | HTTP headers file (from `curl -D`) |
| `--body` | one of two | HTTP body file (from `curl -o`) |
| `--response` | one of two | combined file (from `curl -i`) |
| `--request` | **required** | original request JSON file |
| `--vendor-key` | recommended | upstream vendor API key (or use the `PR_AUDIT_VENDOR_KEY` environment variable) |
| `--format` | optional | output format: `human` (default) or `json` |

**Scenarios where vendor-key is not provided**:
- OpenAI plain text: tiktoken offline reconciliation, no key needed → L3 runs normally
- Anthropic/Gemini: a key is required to call count_tokens → without a key, L3 is SKIPPED

### 5.3 General commands

```bash
pr-audit --version          # print version
pr-audit --help             # global help
pr-audit verify --help      # verify subcommand help
pr-audit replay --help      # replay subcommand help
```

---

## 6. Output design

### 6.1 verify output (existing, unchanged)

```
pr-audit verify v0.1.0

[L1 · Self-consistency checks]
[✓] Evidence headers present
    x-upstream-sha256: sha256:a1b2c3...
    x-upstream-trace-id: req_01HXYZABC
    x-upstream-vendor: openai
[✓] Body SHA256 matches declared value
    computed: sha256:a1b2c3...
[✓] Usage field parsed from body
    prompt_tokens:     42
    completion_tokens: 128
    total_tokens:      170

Result: SELF-CONSISTENT
  PrimeRouter's declared hash matches the body it delivered.
  This rules out accidental corruption, but does NOT prove the body
  equals what the upstream vendor actually returned.

  vendor=openai  model=gpt-4o-mini  trace-id=req_01HXYZABC

⚠  To obtain stronger evidence:

  [L2 · External attestation]
    Open the vendor dashboard and confirm this request exists:
      https://platform.openai.com/logs?request_id=req_01HXYZABC

  [L3 · End-to-end replay]
    Re-run the same request with your own vendor key:
      pr-audit replay --headers headers.txt --body body.bin --request request.json --vendor-key $YOUR_KEY

Exit code: 0 (L1 passed)
```

### 6.2 replay output — L3 pass

```
pr-audit replay v0.1.0

[L1 · Self-consistency checks]
[✓] Body SHA256 matches declared value
    computed: sha256:a1b2c3...

[L3 · End-to-end replay checks]
[✓] L3 strategy: tiktoken offline (OpenAI plain text)
[✓] prompt_tokens match
    computed: 42  reported: 42
[✓] model field match
    request: gpt-4o-mini  response: gpt-4o-mini

Result: NO EVIDENCE OF TAMPERING
  L1 self-consistency passed + L3 deterministic fields (prompt_tokens, model)
  match between PrimeRouter's response and your own verification.
  This does NOT prove the response body equals what the upstream vendor
  returned — only that the auditable fields are consistent.

  vendor=openai  model=gpt-4o-mini  trace-id=req_01HXYZABC

⚠  For additional confidence:

  [L2 · External attestation]
    Open the vendor dashboard and confirm this request exists:
      https://platform.openai.com/logs?request_id=req_01HXYZABC

Exit code: 0 (L1+L3 passed)
```

### 6.3 replay output — L3 fail (prompt_tokens mismatch)

```
pr-audit replay v0.1.0

[L1 · Self-consistency checks]
[✓] Body SHA256 matches declared value
    computed: sha256:a1b2c3...

[L3 · End-to-end replay checks]
[✓] L3 strategy: tiktoken offline (OpenAI plain text)
[✗] prompt_tokens DOES NOT MATCH
    computed: 42  reported: 420
    difference: +378 (9x inflation)
[✓] model field match
    request: gpt-4o-mini  response: gpt-4o-mini

Result: L3 FAIL
  prompt_tokens mismatch: PrimeRouter reported 420, independent calculation
  returned 42. This is strong evidence of token count inflation.
  Verify with L2 (vendor dashboard) before concluding this is intentional.

  vendor=openai  model=gpt-4o-mini  trace-id=req_01HXYZABC

⚠  Confirm with L2:

  [L2 · External attestation]
    Open the vendor dashboard and confirm this request exists:
      https://platform.openai.com/logs?request_id=req_01HXYZABC

Exit code: 40 (L3 failed)
```

### 6.4 replay output — L3 degraded (OpenAI tools)

```
pr-audit replay v0.1.0

[L1 · Self-consistency checks]
[✓] Body SHA256 matches declared value

[L3 · End-to-end replay checks]
[-] L3 strategy: structural (OpenAI tool calls — prompt_tokens not reliably verifiable offline)
[-] prompt_tokens: SKIPPED (not reliably verifiable offline)
    lower-bound estimate: 42 (actual may be higher due to tool overhead)
[✓] model field match
    request: gpt-4o-mini  response: gpt-4o-mini

Result: L3 DEGRADED
  L3 verification was partially skipped due to OpenAI tool calls.
  prompt_tokens cannot be reliably verified offline for tool-enabled requests.
  Model field was verified successfully.

  vendor=openai  model=gpt-4o-mini  trace-id=req_01HXYZABC

⚠  For additional confidence:

  [L2 · External attestation]
    Open the vendor dashboard and confirm this request exists:
      https://platform.openai.com/logs?request_id=req_01HXYZABC

Exit code: 0 (L3 degraded — not a failure)
```

### 6.5 replay output — L3 skipped

```
pr-audit replay v0.1.0

[L1 · Self-consistency checks]
[✓] Body SHA256 matches declared value

[L3 · End-to-end replay checks]
[-] L3 strategy: SKIPPED
    vendor "zhipu" does not have a count_tokens endpoint or offline tokenizer

Result: L3 SKIPPED
  L3 end-to-end replay is not available for vendor "zhipu" in this version.
  L1 self-consistency passed. Proceed to L2 for manual verification.

  vendor=zhipu  model=glm-4-flash  trace-id=202604221622267d...

⚠  Verify manually:

  [L2 · External attestation]
    Look up the trace-id in your upstream vendor's console:
      https://bigmodel.cn/usercenter/apikeys

Exit code: 0 (L3 skipped — not a failure)
```

### 6.6 replay output — L1 fail (L3 not continued)

```
pr-audit replay v0.1.0

[L1 · Self-consistency checks]
[✗] Body SHA256 DOES NOT match declared value
    declared:  sha256:abc...
    computed:  sha256:xyz...

Result: L1 FAIL
  Local SHA256 does not match PrimeRouter's declared value.
  L3 replay is not attempted because the body cannot serve as a reliable
  baseline for comparison when L1 self-consistency has failed.
  Preserve response and declared header; confirm with L2 before
  concluding this is intentional tampering (could also be a bug).

Exit code: 10 (L1 failed — L3 skipped)
```

### 6.7 JSON output format (extended)

```json
{
  "version": "0.1.0",
  "command": "replay",
  "timestamp": "2026-04-23T10:30:00Z",
  "input": {
    "source": "split:headers.txt+body.bin",
    "size_bytes": 2048
  },
  "trust_level_reached": "L3_no_evidence_of_tampering",
  "vendor": "openai",
  "model": "gpt-4o-mini",
  "trace_id": "req_01HXYZABC",
  "usage": {
    "prompt_tokens": 42,
    "completion_tokens": 128,
    "total_tokens": 170
  },
  "checks": [
    {
      "name": "header_presence",
      "status": "pass",
      "details": {
        "x-upstream-sha256": "sha256:a1b2c3...",
        "x-upstream-trace-id": "req_01HXYZABC",
        "x-upstream-vendor": "openai"
      }
    },
    {
      "name": "sha256_match",
      "status": "pass",
      "details": {"declared": "sha256:a1b2c3...", "computed": "sha256:a1b2c3..."}
    },
    {
      "name": "usage_parsed",
      "status": "pass",
      "details": {"prompt_tokens": 42, "completion_tokens": 128, "total_tokens": 170}
    }
  ],
  "l3_strategy": "tiktoken_offline",
  "l3_checks": [
    {
      "name": "l3_strategy",
      "status": "pass",
      "message": "tiktoken offline (OpenAI plain text)"
    },
    {
      "name": "prompt_tokens_match",
      "status": "pass",
      "details": {"computed": 42, "reported": 42}
    },
    {
      "name": "model_match",
      "status": "pass",
      "details": {"request": "gpt-4o-mini", "response": "gpt-4o-mini"}
    }
  ],
  "next_steps": [
    {
      "level": "L2",
      "action": "verify_trace_id_on_vendor_dashboard",
      "url": "https://platform.openai.com/logs?request_id=req_01HXYZABC"
    }
  ],
  "result": "no_evidence_of_tampering",
  "exit_code": 0
}
```

---

## 7. Exception handling

| Scenario | CLI behavior | Exit Code | Notes |
|---|---|---|---|
| L1 hash mismatch | output L1 FAIL, do not continue to L3 | 10 | body cannot serve as a reconciliation baseline |
| L1 unavailable (missing headers) | may still attempt L3 (if --request and --vendor-key are provided) | 0 | missing evidence ≠ absent evidence |
| vendor = unknown | L3 SKIPPED | 0 | tokenizer cannot be chosen |
| OpenAI + tools/multimodal | L3 DEGRADED | 0 | tiktoken cannot compute accurately |
| Azure OpenAI | L3 DEGRADED | 0 | not yet supported, later iteration |
| vendor-key invalid/expired | output auth error, prompt to check key | 31 | distinguished from L3 failure |
| DNS resolution failure | explicitly identify DNS | 31 | network-layer error |
| TLS handshake/certificate failure | explicitly identify TLS | 32 | network-layer error |
| Upstream 5xx / timeout | explicitly identify upstream error | 33 | network-layer error |
| L3 prompt_tokens mismatch | L3 FAIL, report diff value | 40 | hard failure |
| L3 model mismatch | L3 FAIL, report diff | 40 | hard failure |
| Anthropic count_tokens unavailable | degrade to structural check + warn | 0 | depends on external service |
| tiktoken version drift | on reconciliation failure, prefer tokenizer-mismatch hint; don't directly judge as tampering | 40 | source of uncertainty |
| --request missing | error: replay requires --request | 20 | input error |
| --headers+--body and --response specified together | error: cannot mix | 20 | input error |
| request.json parse failure | error: cannot parse request file | 20 | input error |
| vendor-key not provided + vendor requires online reconciliation | L3 SKIPPED + hint that a key is required | 0 | not a failure, a limitation |

### Structural limits of L3 (integration engineer must-reads)

**completion_tokens cannot be hard-reconciled**: LLM responses are non-deterministic. Even with temperature=0 and the same seed, vendors don't guarantee identical output every time. So replay does not make a pass/fail decision on completion_tokens — it only shows the value PrimeRouter reported and the directly-obtained value, letting the user judge whether the difference is reasonable.

**Why OpenAI tools / multimodal are degraded**: OpenAI has **private overhead token rules** for tool definitions and multimodal inputs, which are not in the public tiktoken library. Locally-computed token counts will **definitely be less than** what the server actually computes (missing overhead), so only a lower-bound estimate is possible, not pass/fail.

**Tokenizer version drift risk**: the encoding version of tiktoken-go may lag behind the latest OpenAI server-side version. On reconciliation failure, the output should prefer "tokenizer version may be inconsistent" rather than immediately declaring tampering. This is the risk explicitly listed in `limitations.md` §2.4.5.

**Vendor API rate limiting**: Anthropic/Gemini's count_tokens endpoints have independent rate limits. Bulk replay may hit the wall. On failure, degrade to a structural check rather than declaring L3 failure.

---

## 8. Technical design

### 8.1 New dependencies

| Dependency | Purpose | Notes |
|---|---|---|
| `github.com/spf13/cobra` | CLI skeleton, replaces hand-written `flag`+`switch` | mature and stable; paves the way for future subcommands such as `proxy` |
| `github.com/pkoukk/tiktoken-go` | local OpenAI prompt_tokens computation | the only tiktoken Go implementation; requires downloading encoding data |

v0.1.0 had zero dependencies. This work adds 2 dependencies (+ cobra's transitive `viper`/`pflag`).

### 8.2 Project structure changes

```
pr-audit/
├── cmd/pr-audit/
│   ├── main.go              # cobra rootCmd (replaces os.Args switch)
│   ├── verify.go            # verify subcommand cobra definition (refactored)
│   └── replay.go            # replay subcommand cobra definition (new)
├── internal/
│   ├── verify/              # L1 logic (largely unchanged)
│   │   ├── verify.go        # Run() main flow (unchanged)
│   │   ├── parse.go         # HTTP parsing (unchanged; reused by replay)
│   │   ├── hash.go          # SHA256 computation (unchanged)
│   │   └── usage.go         # usage/model parsing (unchanged)
│   ├── replay/              # L3 logic (new package)
│   │   ├── replay.go        # Run() main flow + L3 strategy routing
│   │   ├── openai.go        # OpenAI tiktoken offline reconciliation
│   │   ├── anthropic.go     # Anthropic count_tokens API reconciliation
│   │   ├── gemini.go        # Gemini countTokens API reconciliation
│   │   ├── convert.go       # OpenAI→Gemini message format conversion
│   │   └── degraded.go      # degraded/skipped logic
│   ├── vendor/              # vendor detection + dashboard URL + count_tokens info
│   │   ├── detect.go        # unchanged
│   │   ├── dashboard.go     # unchanged
│   │   └── count_tokens.go  # new: count_tokens endpoint info
│   ├── model/               # shared types (extended)
│   │   └── types.go         # add L3-related types and constants
│   └── output/              # renderer (extended to support L3)
│       ├── human.go         # extended L3 section
│       └── json.go          # unchanged (Result JSON serialization automatically includes new fields)
├── testdata/
├── docs/
├── Makefile
├── go.mod
└── .github/workflows/ci.yml
```

### 8.3 Data model changes

New in `internal/model/types.go`:

```go
// TrustLevel additions
TrustL3NoEvidence TrustLevel = "L3_no_evidence_of_tampering"
TrustL3Fail       TrustLevel = "L3_fail"
TrustL3Skipped    TrustLevel = "L3_skipped"
TrustL3Degraded   TrustLevel = "L3_degraded"

// Outcome additions
OutcomeNoEvidenceOfTampering Outcome = "no_evidence_of_tampering"
OutcomeL3Fail                Outcome = "l3_fail"
OutcomeL3Skipped             Outcome = "l3_skipped"
OutcomeL3Degraded            Outcome = "l3_degraded"

// L3 strategy type
type L3Strategy string
const (
    L3TiktokenOffline L3Strategy = "tiktoken_offline"
    L3CountTokensAPI  L3Strategy = "count_tokens_api"
    L3Structural      L3Strategy = "structural"
    L3Skipped         L3Strategy = "skipped"
)

// replay command parameters
type ReplayParams struct {
    HeadersPath  string
    BodyPath     string
    ResponsePath string
    RequestPath  string
    VendorKey    string
}

// Raw request JSON saved by the user
type ReplayRequest struct {
    Model    string          `json:"model"`
    Messages json.RawMessage `json:"messages"`
    Tools    json.RawMessage `json:"tools,omitempty"`
    Stream   bool            `json:"stream,omitempty"`
}

// New fields on the Result struct
type Result struct {
    // ... existing fields unchanged ...

    L3Strategy L3Strategy `json:"l3_strategy,omitempty"`
    L3Checks   []Check    `json:"l3_checks,omitempty"`
}
```

### 8.4 Exit code full definition

| Code | Meaning | verify? | replay? | New? |
|---|---|---|---|---|
| 0 | success / L1 unavailable / L3 pass / L3 skipped / L3 degraded | ✓ | ✓ | |
| 10 | L1 fail (hash mismatch / unsupported algorithm) | ✓ | ✓ | |
| 11 | Reserved (strict mode, unused) | ✓ | ✓ | |
| 20 | input parse error | ✓ | ✓ | |
| 31 | network: DNS failure | | ✓ | ✓ |
| 32 | network: TLS failure | | ✓ | ✓ |
| 33 | network: upstream 5xx / timeout | | ✓ | ✓ |
| 40 | L3 fail (prompt_tokens / model mismatch) | | ✓ | ✓ |
| 99 | internal error | ✓ | ✓ | |

### 8.5 Output wording conventions (complete)

| Scenario | CLI verdict | Meaning | Trust tier |
|---|---|---|---|
| L1 pass | `SELF-CONSISTENT` | self-consistent, does not prove honesty | L1 |
| L1 pass + L3 pass | `NO EVIDENCE OF TAMPERING` | no tampering evidence found in deterministic fields | L3 |
| L1 pass + L3 degraded | `L3 DEGRADED` | some L3 checks skipped | L3 (partial) |
| L1 pass + L3 skipped | `L3 SKIPPED` | L3 could not run | L1 |
| L1 fail | `L1 FAIL` | self-consistency failed | None |
| L1 unavailable | `L1 UNAVAILABLE` | no evidence headers | L1 unavailable |
| L3 fail | `L3 FAIL` | prompt_tokens or model mismatch | L1 (L1 may have passed) |

**Never output `VERIFIED`**. This is a core constraint of the trust model (`trust-model.md` §3.4).

---

## 9. Security

1. **Zero-trust PrimeRouter**: pr-audit never calls PrimeRouter's own API to obtain a verification verdict. The replay command's network requests are only sent to the hard-coded upstream-vendor domains (`api.anthropic.com`, `generativelanguage.googleapis.com`); never to PrimeRouter or any man-in-the-middle
2. **Vendor key never leaves the local machine**: pr-audit uses the key only to connect directly to the upstream-vendor API; it does not go through PrimeRouter and is not uploaded to any server
3. **Key redaction**: the key is displayed as `sk-...***` in CLI output (only the first 4 and last 2 characters shown)
4. **Key is not written to logs / JSON output**: the `--format json` output does not contain the vendor-key value
5. **HTTPS only**: all count_tokens API calls must go over HTTPS

---

## 10. Testing strategy

### 10.1 Unit tests (same-package `_test.go`)

| Module | Test focus | Method |
|---|---|---|
| `replay/replay.go` | correctness of L3 strategy routing | construct different vendors → verify strategy selection |
| `replay/openai.go` | tiktoken computation + tools degradation | known prompt → expected token count; tools → degraded |
| `replay/anthropic.go` | count_tokens API + cache summation | httptest mock: count_tokens=100, reported input=30+cache_creation=40+cache_read=30 → pass |
| `replay/gemini.go` | countTokens API + format conversion | httptest mock: correctness of format conversion + comparison logic |
| `replay/convert.go` | OpenAI→Gemini message format conversion | per-role conversion + multimodal skip |
| `replay/degraded.go` | degraded output | each degradation reason → correct StatusSkip |
| `model/types.go` | correctness of added constants/types | enum value compile check |
| `vendor/count_tokens.go` | HasCountTokens / HasOfflineTokenizer | correctness across vendors |

### 10.2 Integration tests (`t.TempDir()` + fixture)

| Scenario | Setup | Expected |
|---|---|---|
| OpenAI plain text L3 pass | headers + body + request.json, vendor=openai | L3 pass, exit 0 |
| OpenAI plain text L3 fail | same as above but prompt_tokens in body changed to wrong value | L3 fail, exit 40 |
| OpenAI tools L3 degraded | request.json contains tools field | L3 degraded, exit 0 |
| Anthropic cache summation pass | mock count_tokens + cache usage | L3 pass, exit 0 |
| Anthropic cache summation fail | mock count_tokens + mismatched cache usage | L3 fail, exit 40 |
| vendor=unknown → L3 skipped | headers lack x-upstream-vendor, body model not in known list | L3 skipped, exit 0 |
| L1 fail → L3 not executed | header hash does not match body | L1 fail, exit 10, no L3 section |

### 10.3 CLI-level manual tests

Using fixtures under `testdata/mocks/`:

```bash
# verify unchanged
./pr-audit verify --headers testdata/mocks/openai-ok.headers.txt --body testdata/mocks/openai-ok.body.json

# replay (requires constructing request.json + vendor key)
./pr-audit replay --headers testdata/mocks/openai-ok.headers.txt \
  --body testdata/mocks/openai-ok.body.json \
  --request /tmp/test-request.json \
  --vendor-key sk-test
```

---

## 11. Open items

- [ ] Do Zhipu/DeepSeek/Moonshot have count_tokens endpoints? If yes, L3 can be supported
- [ ] Is Azure OpenAI's tiktoken version identical to official OpenAI's? If yes, openai.go can be reused
- [ ] What's the mechanism to detect version drift between tiktoken-go and the OpenAI server side? Do we need periodic checks for the latest encoding?
- [ ] What's the latest `anthropic-version` header for the Anthropic count_tokens endpoint? Use `2023-06-01` or a newer version?
- [ ] Does the Gemini countTokens API require the `x-goog-api-key` header, or is the URL parameter sufficient?
- [ ] Does request.json need to support non-JSON formats (e.g. YAML)? MVP supports JSON only

---

## 12. MVP scope

### Included in this release

- cobra CLI skeleton migration
- `verify` command (L1+L2, existing functionality + cobra refactor; functionality unchanged)
- `replay` command (full L1+L2+L3 flow)
- OpenAI tiktoken offline reconciliation (plain-text chat)
- Anthropic count_tokens online reconciliation (including prompt-cache summation)
- Gemini countTokens online reconciliation (plain-text chat, including OpenAI→Gemini format conversion)
- Degraded path (OpenAI tools/multimodal → L3 DEGRADED)
- Skipped path (zhipu/deepseek/moonshot/unknown → L3 SKIPPED)
- Network-error exit codes (31/32/33)
- L3 failure exit code (40)
- vendor-key flag + environment variable dual passing
- Extended human + JSON output
- Remove CI gocloc line-count limit
- Update AGENTS.md

### Not included in this release

- SSE streaming response support
- proxy proxy mode
- Batch verification
- Azure OpenAI L3 reconciliation
- Zhipu/DeepSeek/Moonshot L3 reconciliation
- Gemini multimodal conversion (images, etc.)
- request.json YAML format support
