# pr-audit Capability Limitations

> This document should be read alongside `trust-model.md`. trust-model explains **what can be proven**; this one explains **what cannot be proven**.
> **The two are equally important** — honestly listing the boundaries is pr-audit's first principle of integrity.
> Last updated: 2026-04-22

---

## 0. One-page summary

pr-audit is a **response integrity audit tool**. What it can verify is: across the link from the **upstream vendor's return** to the **user's hands**, the byte-level integrity of the response and the truthfulness of token counts.

It is **not**:

- A data privacy audit tool (cannot prove whether PrimeRouter records your prompts)
- A security audit tool (cannot prove whether API keys have leaked)
- A service-quality tool (cannot prove SLA, latency, or availability commitments)
- An audit tool for the upstream vendor's own behavior (cannot prove whether OpenAI modified your prompt)
- A proof of honesty toward all users (cannot detect differential behavior — that requires a transparency log, a long-term roadmap item)

If the concern you care about is not within pr-audit's coverage, this document will tell you **what to use instead**.

---

## 1. What pr-audit can do (brief recap)

See `trust-model.md` for details. In one sentence:

> Under the premise that "PrimeRouter is untrusted, upstream vendors are trusted, the user's machine is trusted, and SHA256 is trusted", provide three progressive tiers of verification (L1 / L2 / L3), giving **tiered evidence** about response integrity and the truthfulness of token counts.

---

## 2. What pr-audit cannot prove (full list)

### 2.1 About PrimeRouter's behavior

| Cannot prove | Reason | What to do |
|---|---|---|
| PrimeRouter doesn't record user prompt contents | This is a server-side logging policy; invisible in response headers | Read PrimeRouter's privacy policy; use self-hosted deployment for sensitive data |
| PrimeRouter doesn't leak the user's API key | Same as above; involves internal data handling | Use fine-grained API keys + key rotation + rate limiting |
| PrimeRouter's latency and availability commitments | pr-audit only audits response content, not service quality | Read the SLA; monitor your own P95/P99 |
| PrimeRouter doesn't differentiate responses based on user identity | A single user can only see whether their own response is self-consistent | Requires a **transparency log** mechanism (v2.0+ independent project); not available today |
| PrimeRouter's production code matches the open-source code | pr-audit only looks at responses, not server-side code | Requires reproducible build + code notarization — a separate trust mechanism |

### 2.2 About upstream vendors' behavior

pr-audit treats upstream vendors as the baseline. If the vendor itself cheats, pr-audit cannot detect it:

| Cannot prove | Why |
|---|---|
| OpenAI didn't modify the user prompt | OpenAI is the trust anchor; pr-audit uses it as the baseline and does not check it |
| OpenAI didn't post-process the response (content moderation, truncation) | Same as above; what pr-audit sees is the vendor's "final returned" body |
| Whether the upstream vendor itself records or uses user data | That's the vendor's data policy — see each vendor's OpenAI DPA / Anthropic Usage Policy |

### 2.3 Structural limits of L1 (local hash check)

**Emphasized repeatedly**: L1 passing **does not equal** PrimeRouter being honest.

| Attack vector | Can L1 detect? |
|---|---|
| PrimeRouter accidentally changes bytes in transit (CDN / WAF bug) | ✓ |
| Implementation bug in PrimeRouter that rewrites body but forgets to sync the hash | ✓ |
| PrimeRouter **deliberately** tampers with body + recomputes hash + updates header | ✗ **Cannot** — requires L2/L3 |

This limitation is not a bug in pr-audit; it's the theoretical ceiling of any "server-signed only" mechanism. The real defense is L2 (external verification via vendor dashboard) and L3 (end-to-end reconciliation using the user's key against upstream).

### 2.4 Limits of L3 (end-to-end reconciliation)

#### 2.4.1 completion_tokens cannot be hard-reconciled

LLM responses are **non-deterministic**. Even with `temperature=0` and the same `seed`, vendors do not guarantee byte-identical output across calls. Therefore:

- **Cannot** use replay to perform a byte-level diff to judge tampering
- **Cannot** give an exact pass/fail conclusion on `completion_tokens`
- For completion_tokens, pr-audit only shows "what PrimeRouter reported" and "what a direct call reported", **letting the user decide** whether the difference is within a reasonable range

#### 2.4.2 OpenAI tool calls / multimodal: hard reconciliation of prompt_tokens fails

OpenAI has **private overhead token rules** for tool calls and multimodal input, which are not in the public tiktoken library. Therefore:

- When `pr-audit replay` encounters requests with tools/multimodal, it **does not hard-reconcile prompt_tokens**
- The output will explicitly mark `Strategy: SKIPPED (not reliably verifiable offline)`
- Only a `lower-bound estimate` is given (the lower bound computed by tiktoken); the user decides

#### 2.4.3 Anthropic / Gemini: depend on the vendor's count_tokens endpoint

Anthropic and Gemini don't have offline-usable tokenizers. pr-audit reconciles by **directly calling the vendor's count_tokens endpoint**. This means:

- The user must provide their own vendor API key
- Depends on the vendor endpoint's availability — if the vendor takes it offline or changes the interface, L3 will break for these vendors
- Consumes one rate-limit quota call against that endpoint
- **This is not "fully offline" verification** — but is equivalent in trust-chain terms to replaying via the chat endpoint

#### 2.4.4 The Anthropic prompt caching pitfall

When Anthropic prompt caching is enabled, `usage` is split into:

- `input_tokens` (portion that didn't hit the cache)
- `cache_creation_input_tokens` (cache created this time)
- `cache_read_input_tokens` (cache hits)

The `count_tokens` endpoint returns "the total without cache". **The correct reconciliation formula is a sum**:

```
count_tokens_result ≡ input_tokens + cache_creation_input_tokens + cache_read_input_tokens
```

pr-audit's implementation handles this summation. But if you implement verification logic yourself, **omitting this step will cause any cached request to be falsely reported as "PrimeRouter inflated input_tokens"**.

#### 2.4.5 Tokenizer version drift

The local tiktoken and the OpenAI server-side version may have a **brief window of inconsistency** (right when a new model launches, or the encoding is updated). This can cause L3 reconciliation to produce **false positives**.

pr-audit's response:

- tokenizer version pinned in `go.sum`
- On reconciliation failure, preferentially indicate `tokenizer version mismatch (pinned v0.5.0, upstream may be newer)`; **do not** directly declare tampering
- Known version differences are documented

#### 2.4.6 Risk of vendor misidentification

pr-audit needs to identify "which upstream this response came from" before it can choose the right tokenizer. Identification mechanism:

1. Prefer reading the `x-upstream-vendor` header (requires server-side support)
2. Fallback: URL-path heuristic (`/v1/chat/completions` → OpenAI-compatible, etc.)
3. Last resort: heuristic on the `model` field inside the body (`gpt-*` → OpenAI, etc.)

**Cases of misidentification**:

- Azure OpenAI is mistaken for official OpenAI (their tokenizers are essentially the same, but some models differ)
- Secondary-relay services like OpenRouter / Together / Fireworks are recognized as OpenAI (while the actual model may be Llama)

When identification is uncertain, pr-audit **skips L3 hard reconciliation** and outputs `vendor: unknown → L3 skipped`. Better to not judge than to misjudge.

### 2.5 Deeper limits on audit completeness

#### 2.5.1 Differential behavior

In theory, PrimeRouter could return a truthful response (with real hash) to **user A** and a tampered response (with a fake hash) to **user B**. Each user's individual verify passes — because what each sees is self-consistent.

**pr-audit currently cannot defend against this attack**, because a single-user viewpoint cannot see what other users received.

**Direction for a fix**: **Transparency Log** (cf. Certificate Transparency) — PrimeRouter periodically publishes the Merkle tree root of hashes of all requests, and any user can spot-check "is my request's hash in yesterday's Merkle tree?" If any inconsistency is found, it means PrimeRouter made different commitments to different users.

This is a **v2.0+ independent initiative** for pr-audit; not covered in v1.0.

#### 2.5.2 Timing and replay attacks

pr-audit does not verify "whether the response timestamp is real". If PrimeRouter caches an old response and forges a timestamp on return, pr-audit cannot detect it — because the response itself is self-consistent and real (just old).

**Mitigation**: L2 dashboard reveals the real initiation time; the user's own request logs have timestamps to compare against.

#### 2.5.3 Statistical attacks

In theory, PrimeRouter could be **honest most of the time** and cheat on only 1% of requests (picking ones unlikely to be spot-checked). pr-audit verifies **per-request**, not across requests statistically.

**Mitigation**: run pr-audit in bulk as a CI patrol; or use proxy mode (v0.2+) to audit everything.

---

## 3. Plausible-sounding claims that don't hold

The following are statements **likely to appear in marketing copy but which pr-audit cannot deliver**. If you see them, stay skeptical:

### ❌ "pr-audit byte-level diffs and replays responses"

**Why it's wrong**: LLM responses are non-deterministic — even with identical parameters, two calls won't be byte-identical. What replay can do is reconcile **deterministic fields** (prompt_tokens, model, trace-id format), not a byte diff.

**Correct wording**:

- "local `sha256(body)` is byte-identical to `x-upstream-sha256`" — that's L1, a byte diff is correct
- "replay reconciles usage / model / trace-id" — that's L3, not a byte diff

### ❌ "pr-audit 100% verifies PrimeRouter's honesty"

**Why it's wrong**: see trust-model.md §3. L1 only proves self-consistency, not honesty; L2 needs a human; L3 has deterministic-field limits. **There is no 100%** — only "how far each tier can go, clearly explained".

### ❌ "pr-audit is independent of the upstream vendor"

**Why it's wrong**: pr-audit's trust anchor **is** the upstream vendor. L2 depends on the vendor dashboard; L3 depends on the user's direct connection to upstream. If the upstream vendor is untrusted, pr-audit is meaningless — because there's no "truth" to reconcile against.

**Correct wording**: pr-audit is independent of PrimeRouter, but depends on upstream vendors as the trust anchor.

### ❌ "pr-audit proves PrimeRouter has no security vulnerabilities"

**Why it's wrong**: pr-audit only looks at response content; it doesn't look at server-side code, doesn't do penetration testing, and doesn't audit operational processes. It is a **single-dimensional integrity tool**, not a comprehensive security audit.

### ❌ "pr-audit is a substitute for SOC2 / ISO27001"

**Why it's wrong**: compliance audits focus on **operational processes** (access control, change management, incident response, etc.). pr-audit focuses on **the integrity of a single response**. They are complementary, not substitutes.

---

## 4. What pr-audit is not

### 4.1 Not a comprehensive security audit tool

Does not do: penetration testing, vulnerability scanning, key management audit, access control review, log compliance checks.

**If you need these**: hire a professional security firm to perform the audit; use tools like OWASP ZAP / Burp Suite / Snyk.

### 4.2 Not a data compliance tool

Cannot prove: data does not leave the country, GDPR/CCPA compliance, fulfillment of user data deletion rights, sensitive-data redaction.

**If you need these**: read the provider's Data Processing Agreement (DPA); self-host PrimeRouter for full control over the data path.

### 4.3 Not an SLA monitoring tool

Cannot measure: response latency, service availability, accuracy of rate limiting, error rate.

**If you need these**: observability tools like Datadog / Grafana / Prometheus; the provider's SLA terms + your own probing.

### 4.4 Not a compliance audit of the upstream vendor

Cannot prove: whether OpenAI itself operates per its ToS, or whether it truly uses the model weights it declares.

**If you need these**: this is between OpenAI and its auditors; users cannot verify independently.

---

## 5. If you care about X, what should you use?

| Your concern | pr-audit? | Tool / method to use |
|---|---|---|
| "Is PrimeRouter tampering with response bytes?" | ✓ L1 + L2 + L3 | **pr-audit** |
| "Is PrimeRouter inflating token counts?" | ✓ L3 (differs by vendor) | **pr-audit** |
| "Is PrimeRouter pretending to forward but not actually hitting upstream?" | ✓ L2 (look up trace-id on dashboard) | **pr-audit** |
| "Is PrimeRouter silently downgrading models?" | ✓ L2 (dashboard) + L3 (model field) | **pr-audit** |
| "Is PrimeRouter recording my prompts?" | ✗ | Read privacy policy + self-host + DPA |
| "Is PrimeRouter leaking my API key?" | ✗ | Fine-grained key + rotation + usage monitoring |
| "PrimeRouter service availability" | ✗ | Self-run probes + commercial SLA terms |
| "PrimeRouter latency" | ✗ | Datadog / Grafana + your own monitoring |
| "Is the upstream vendor itself trusted?" | ✗ (assumed by default) | Vendor SOC2 / DPA / industry reputation |
| "Consistency across users" | ✗ (not in v1.0) | Wait for transparency log (v2.0+) |

---

## 6. FAQ

### Q: If I use pr-audit, do I still need to read PrimeRouter's security whitepaper?

Yes. pr-audit covers the single dimension of "response integrity"; **other dimensions still need** other trust mechanisms. pr-audit is a **supplement** to the whitepaper, not a replacement.

### Q: If L1 can't prove honesty, is it worth running?

L1 is a precondition for L2/L3 — without L1, the body in your hands cannot serve as the baseline for later reconciliation. Also, L1 has near-zero cost (done locally in an instant); there's no reason not to. Just **don't stop at L1**.

### Q: If pr-audit outputs PASS for a request, can I tell customers "this request passed verification"?

It depends on which tier's PASS:

- `SELF-CONSISTENT` (L1): you can say "PrimeRouter's declaration matches delivery"; **do not** say "honesty has been verified"
- `NO EVIDENCE OF TAMPERING` (L3 passed): you can say "no tampering evidence was found in the deterministic fields checked by L3"; **do not** say "100% honest"
- Manual L2: you can say "I confirmed the request exists on the vendor dashboard"

The precision of your wording determines whether your statement can withstand questioning.

### Q: What should I do after pr-audit detects tampering?

1. **Preserve evidence**: response file, output of `pr-audit verify --format json`, timestamps
2. **Reproduce**: run the same request again to see if it's sporadic
3. **L2 confirmation**: look up the trace-id on the vendor dashboard to see what the vendor recorded
4. **L3 verification**: use your own key to connect directly to upstream, ruling out a pr-audit bug itself
5. **Contact PrimeRouter**: provide evidence; if malicious behavior is confirmed, consider public disclosure + switching providers

### Q: Will this document be updated?

Yes. As PrimeRouter's capabilities, vendor APIs, and pr-audit features evolve, the boundaries change — **but "what cannot be done" will always be listed here**. Any capability upgrade will be reflected here in sync.

---

## 7. Acknowledgements and disclaimer

As an open-source tool (MIT License), pr-audit **provides no express or implied warranty**. Users should understand its capability limits and not rely on its output as the sole basis of trust. Any business, legal, or technical decisions made based on pr-audit are the user's own responsibility.

If you find that some limitation listed in this document **can actually be solved**, feel free to open a GitHub issue — we'd be delighted.

---

## Appendix: Version history

| Version | Date | Notes |
|---|---|---|
| v1.0 | 2026-04-22 | Initial version, written alongside trust-model.md v1.0 |
