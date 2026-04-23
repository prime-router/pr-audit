# pr-audit Trust Model

> This document is the **core conceptual document** of the pr-audit project.
> Any dispute about verification strength, CLI wording, or capability boundaries is resolved by reference to this document.
> Last updated: 2026-04-22

---

## 0. One-page summary

pr-audit is an open-source CLI used to audit whether **PrimeRouter** — an LLM API gateway — is "faithfully forwarding upstream vendor responses". It is designed from a **zero-trust** stance: trust nothing PrimeRouter claims about itself; only trust:

1. The upstream vendors (OpenAI / Anthropic / Zhipu / Google, etc.) return the real baseline
2. The user's machine running pr-audit is not compromised
3. The SHA256 hash function has not been broken

Under these premises, pr-audit provides **three progressive tiers of verification** — L1 local self-consistency, L2 vendor-side verifiable, L3 end-to-end replay — ranging from weak to strong. Users **should not treat L1 passing as "PrimeRouter's honesty has been verified"**: L1 can only prove that PrimeRouter is internally self-consistent about its own declarations; it **cannot** prove that what PrimeRouter delivers equals what upstream actually returned. Real verification requires moving to L2 or L3.

The core task of this document is to clearly distinguish "what can be proven" from "what cannot be proven" — this honesty itself is the trust pr-audit seeks to establish.

---

## 1. Why we need a trust model

### 1.1 PrimeRouter's "man-in-the-middle" position

PrimeRouter sits between the user and the upstream LLM vendors:

```
[ user ]  ──>  [ PrimeRouter ]  ──>  [ OpenAI / Anthropic / Zhipu ]
```

This man-in-the-middle position gives it the ability to do four things the user cannot detect:

1. **Tamper with response content** (alter answers)
2. **Inflate token counts** (overcharge)
3. **Not actually forward the request** (return cached or self-generated content)
4. **Silently downgrade models** (claim GPT-4 was called while actually using GPT-3.5)

Traditional trust mechanisms (SOC2 audits, security whitepapers, third-party audit reports) are effective for **enterprise procurement** but are weak constraints for **technical developers** — because they all amount to "some institution says so", not "you can verify it right now".

### 1.2 pr-audit's claim

> **"Don't take our word for it — verify it yourself."**

PrimeRouter attaches three pieces of evidence to each response:

| Evidence | Header / Field | Meaning |
|---|---|---|
| Upstream trace ID | `x-upstream-trace-id` | Original request identifier assigned by the vendor |
| Upstream response-body hash | `x-upstream-sha256` | SHA256 of the upstream raw body |
| usage field passed through verbatim | `usage` in body | Token counts are determined upstream; PrimeRouter does not recompute them |

pr-audit turns these three pieces of evidence from "unilateral server-side claims" into "facts the user can re-check with a single command". But **different evidence proves different things**, which is elaborated below.

---

## 2. Trust assumptions (explicitly listed)

pr-audit is built on these four assumptions. **If any is broken, the entire model fails** — this is not a pr-audit bug; it is the boundary of trust itself.

| Assumption | Meaning | If it doesn't hold |
|---|---|---|
| **Upstream vendor is trusted** | Responses from OpenAI / Anthropic / Zhipu, etc. are the "real" baseline; they do not collude with PrimeRouter to deceive the user | If OpenAI itself tampers with prompts or usage, pr-audit cannot detect it at all |
| **User's machine is trusted** | The machine running pr-audit is not compromised; the pr-audit binary has not been replaced | An attacker replaces pr-audit on your machine to always output "pass"; any verification becomes fake |
| **Hash function is trusted** | SHA256 collision resistance has not been broken | If SHA256 is broken (hasn't happened), hash verification fails outright |
| **PrimeRouter is untrusted** | This is **not** a technical assumption but a **design stance**: pr-audit assumes PrimeRouter may tamper with anything under its control | — |

The fourth is a **zero-trust assumption** and is the reason this entire project exists.

---

## 3. Core insight: self-consistent ≠ honest

This is the most important section of this document, emphasized repeatedly:

### 3.1 Problem statement

Suppose PrimeRouter decides to act maliciously. It can:

1. Receive the upstream raw response body
2. Tamper with body (e.g. change `usage.prompt_tokens` from 42 to 420)
3. **Recompute SHA256 over the tampered body**
4. Put this new hash into the `x-upstream-sha256` header
5. Send it to the user

After the user receives the response, the result of local `sha256sum body` is **fully identical to the value declared in the header** — because both are "hash of the tampered body".

In other words, **local hash verification alone cannot identify this kind of "internally consistent forgery"**.

### 3.2 Strict conceptual distinction

| Concept | Definition | Provable by L1? |
|---|---|---|
| **Self-consistent** | The hash value PrimeRouter declares externally is byte-identical to the body it delivers to the user | ✓ yes |
| **Honest** | The body PrimeRouter delivers to the user equals the body the upstream vendor actually returned | ✗ **no** |

The difference between these two concepts must be understood deeply:

- Self-consistency is **about whether "what PrimeRouter says matches what it does"**
- Honesty is **about whether "what PrimeRouter does matches what actually happened upstream"**

L1 local hash verification checks "does the hash PrimeRouter declared really match the body it gave me?" — this is only self-consistency. Whether the body it gave me is the upstream raw body, **L1 cannot judge at all**.

### 3.3 The real value of L1

Given that L1 cannot defend against a deliberately malicious PrimeRouter, what is it still good for?

- **Detecting transmission corruption**: accidental byte changes from CDN/proxy/WAF (lower value; modern HTTPS is already very reliable)
- **Detecting implementation bugs**: a bug in PrimeRouter's own code that unintentionally rewrites the body without syncing the hash
- **As a carrier of a "declared stance"**: proactively disclosing a hash is itself a verifiable commitment; not disclosing it means "I don't accept this kind of scrutiny"
- **Precondition for L2/L3**: without a hash, the user cannot confirm that "the body I have is the body the server declared" — later reconciliation then loses its baseline

So L1 is not useless; it is **insufficient**. It is a **necessary, not sufficient, condition**.

### 3.4 Important conventions for CLI output

To avoid giving users a false sense of security, pr-audit's CLI output strictly distinguishes wording:

| Scenario | CLI verdict | Meaning |
|---|---|---|
| L1 pass, no further verification | **`SELF-CONSISTENT`** | PrimeRouter's declaration matches its delivery (self-consistent), but honesty is not proven |
| L1 pass + manual L2 dashboard confirmation | (not a CLI state; belongs to manual verification) | Trust strength significantly increased, but still bound by the upstream-vendor-is-trusted assumption |
| L1 pass + L3 reconciliation succeeds | **`NO EVIDENCE OF TAMPERING`** | No tampering evidence found (but not equal to absolute honesty — see §5.3) |
| L1 fail | **`L1 FAIL`** (exit 10) | Self-consistency failed; tampering suspected or an implementation bug |
| Missing required headers | **`L1 unavailable`** (exit 0) | Server did not enable this audit capability; not a failure, just absence of evidence |

**Never output `VERIFIED`** — this word implies "fully trusted", which contradicts the trust model.

---

## 4. The three verification tiers in detail

pr-audit's verification capability is **tiered and progressive**. Users should move from L1 toward L2/L3, rather than stop at L1.

### 4.1 L1: self-consistency (local hash check)

**How to do it**:

```bash
# The user has a PrimeRouter response in hand
sha256sum response.body
# Compare against x-upstream-sha256 in the response header
```

**Can prove**:

- The **hash PrimeRouter declared externally** matches the **bytes of the body it delivered**
- The response has no accidental byte changes on the PrimeRouter → user path
- PrimeRouter's own implementation has no "hash was computed but the body was accidentally rewritten afterwards" bug

**Cannot prove**:

- PrimeRouter's body ≡ the body the upstream vendor returned
- PrimeRouter didn't recompute the hash to cover up tampering (the attack in §3.1)
- That the token counts are real and the model is correct

**Suitable for**: entry-level "sanity checks"; used together with L2/L3.

### 4.2 L2: externally verifiable (trace-id → vendor dashboard)

**How to do it**:

```
1. Take x-upstream-trace-id from the response headers
2. Log in to the upstream vendor dashboard (OpenAI Platform / Anthropic Console / Zhipu console)
3. Search this trace-id and see if the corresponding request record is found
4. Compare usage, model, and time shown on the dashboard with the values in PrimeRouter's response
```

**Can prove**:

- The request **actually reached** the upstream vendor (PrimeRouter didn't fake forwarding or return cache)
- The usage **recorded by** the upstream vendor is consistent with the usage in PrimeRouter's response (if they match)
- The model **recorded by** the upstream vendor is consistent with the model in PrimeRouter's response (prevents downgrade)

**Cannot prove**:

- The **content** of the response body matches what upstream returned (dashboards usually don't display full body)
- That PrimeRouter didn't play tricks like "upstream prompt got modified" (dashboards show the prompt the vendor saw, not the user's original prompt)

**Suitable for**: deep investigation when the user is suspicious about a particular request; periodic spot-checking.

**Limitation**: this is **manual verification**, not automated. The pr-audit CLI prints the dashboard URL to guide the user, but the final check is done by the user themselves.

### 4.3 L3: end-to-end replay (direct upstream reconciliation)

**How to do it**:

```bash
pr-audit replay \
  --request request.json \
  --response primerouter-response.txt \
  --vendor-key $YOUR_OWN_VENDOR_KEY \
  --upstream openai
```

pr-audit will:

1. Use the user's own vendor key to **connect directly to the upstream vendor** (bypassing PrimeRouter)
2. Send the same request
3. Compare several **deterministic fields** between the PrimeRouter response and the direct response

**Reconciliation methods differ by vendor** (because tokenizer ecosystems differ):

| Vendor | prompt_tokens reconciliation method | Can give a hard judgment? |
|---|---|---|
| OpenAI plain-text chat | Local tiktoken recomputation | ✓ |
| OpenAI + tool / multimodal | Degrade to structural check + value-range hint | ✗ (private overhead not public) |
| Anthropic | Direct `/v1/messages/count_tokens` + **cache-aware summation** | ✓ |
| Gemini | Direct `countTokens` method | ✓ |

**Important trust-model clarification**: L3's "locally verifiable" **does not equal "fully offline"** — its core is that **the request path does not go through PrimeRouter**. Using the user's own key to call Anthropic's count_tokens endpoint directly is **fully equivalent in trust-chain terms** to replaying via the chat endpoint — both anchor on the upstream vendor. The only differences are that count_tokens is cheaper and faster.

**Can prove**:

- The `prompt_tokens` PrimeRouter declared matches what the vendor itself computes (deterministic field; hard evidence)
- The model field matches (prevents downgrade)
- The trace-id format conforms to the vendor's specification
- The response structure is valid

**Cannot prove**:

- **Byte-identical** response body — LLMs are non-deterministic; even with temperature=0 and the same seed, vendors don't guarantee byte-level reproducibility
- Exact match of completion_tokens — output length naturally varies across calls

**Suitable for**: deep verification of high-value requests; regression checks for a newly deployed server version.

### 4.4 Special handling of Anthropic prompt caching

When Anthropic prompt caching is enabled, the `usage` in the response is split into **three fields**:

- `input_tokens`: the number of tokens in this request that **did not hit the cache**
- `cache_creation_input_tokens`: the number of tokens for newly-created cache this time
- `cache_read_input_tokens`: the number of tokens that hit the cache

The `count_tokens` endpoint returns **"how many tokens there would be if cache were not used"**.

**The correct reconciliation formula is not a simple equation**:

```
count_tokens_result  ≡  input_tokens + cache_creation_input_tokens + cache_read_input_tokens
```

**Consequence of doing it wrong**: any request that hits the cache would be falsely reported as "PrimeRouter inflated input_tokens" — because count_tokens returns 1066 while the response's input_tokens is only 42; inequality here is not inflation.

pr-audit's replay implementation must perform the three-field summation before comparing.

---

## 5. A thought experiment on a malicious attack

To concretely understand the value of the three tiers, walk through: PrimeRouter wants to inflate token counts to overcharge, and how each tier reacts.

### Scenario

- User sends a request to PrimeRouter
- Upstream (OpenAI) actually returns `prompt_tokens: 42, completion_tokens: 128`
- Malicious PrimeRouter changes body to `prompt_tokens: 420, completion_tokens: 1280` (10x inflation)

### Reaction at each tier

**L1 (local hash)**:

- A clever malicious PrimeRouter recomputes sha256 after modifying body and updates `x-upstream-sha256`
- User's `sha256sum body` → matches the value declared in the header
- **L1 passes** ❌ (a failure case — but L1 was never meant to defend against this)

**L2 (manual trace-id check)**:

- User uses `x-upstream-trace-id` to search for this request on the OpenAI dashboard
- Sees that OpenAI recorded usage as `prompt_tokens: 42, completion_tokens: 128`
- **Does not match** the 420/1280 in PrimeRouter's response
- **L2 detects it** ✓ (attack uncovered, but the user has to proactively check)

**L3 (replay + tokenizer reconciliation)**:

- pr-audit uses the user's OpenAI key; locally recomputes the prompt with tiktoken → gets 42
- Compares with the 420 in PrimeRouter's response → **unequal**
- **L3 auto-detects** ✓ (no need for the user to manually check the dashboard)

### Conclusion

**L1 passing doesn't equal no cheating**. What really leaves PrimeRouter-style attacks nowhere to hide is L2 + L3. That's why CLI output must repeatedly guide the user from L1 toward L2/L3.

---

## 6. Boundaries of the trust model

pr-audit's trust model **is not omnipotent**. Here is what it cannot do:

### 6.1 When the four trust assumptions are broken

- **Upstream vendor untrusted**: if OpenAI tampers with prompts or usage itself, pr-audit is completely ineffective — because pr-audit uses OpenAI as the baseline
- **User machine untrusted**: an attacker replaces the pr-audit binary on the user's machine, causing it to always output pass — any verification is fake
- **Hash broken**: once SHA256 collision attacks become feasible, a malicious PrimeRouter can construct a body with a matching hash but different content

### 6.2 What the model structurally cannot defend against

Even when all trust assumptions hold, pr-audit cannot detect the following:

- **PrimeRouter returning different responses to different users** (differential behavior) — each user's view is "self-consistent" but views differ from each other. Requires a **transparency log** to defend against (v2.0+ independent project)
- **PrimeRouter recording user prompt contents for analysis** — this is a data-handling policy issue, not a response-integrity issue
- **PrimeRouter leaking user API keys** — same as above
- **PrimeRouter's latency/availability commitments** — pr-audit doesn't measure these

### 6.3 Limits of L3 itself

- **OpenAI tool calls / multimodal**: private overhead token rules are not public; cannot hard-reconcile; can only degrade and hint
- **Exact value of completion_tokens**: LLM non-determinism; cannot hard-reconcile
- **Tokenizer version drift**: the local tiktoken may lag the OpenAI server version, producing short-term false positives
- **Vendor API rate limits**: Anthropic/Gemini's count_tokens endpoints have independent rate limits; bulk auditing hits the wall

All of these are **documented in `limitations.md`**. Honestly listing "what cannot be done" is this project's first principle of integrity.

---

## 7. FAQ

### Q1: If PrimeRouter can forge hashes, why publish hashes at all?

Because **publishing the hash is itself a stance**. A dishonest PrimeRouter could choose:

- (a) not publish hash headers at all → obviously refusing scrutiny
- (b) publish but forge hashes internally → get past L1, but L2/L3 will expose it
- (c) publish and compute honestly → pass all verifications

(a) would immediately be spotted by technical users — "you don't even dare give us a hash". (b) increases the cost of cheating (requires maintaining two sets of state) and L2/L3 catch it. (c) is our goal. The hash mechanism gives PrimeRouter a commitment to "honesty" and a cost for "dishonesty".

### Q2: If L1 is insufficient to prove honesty, why is it a P0 feature?

Because L1 is the **precondition** for L2/L3 — the user must first confirm that "the body I have is the body the server declared" before using it as a baseline for L2/L3 reconciliation. Without L1, subsequent discussions lose their coordinate system.

Also, L1 is **the lightest-weight, most frequently used** tier — the vast majority of users won't check the dashboard or do replay every time, but they'll verify the hash every time. L1 plays the role of a "smoke test" in daily use.

### Q3: Why not just use OpenAI's official signed responses?

Currently **no mainstream LLM vendor** offers response-signing capability (e.g. HMAC + private-key signed responses). If this arrives in the future, pr-audit will prefer such vendor-level signatures — because they don't depend on any middleman (including PrimeRouter itself). Until then, pr-audit's SHA256 + trace-id mechanism is the strongest we can do under current constraints.

### Q4: Can I trust pr-audit itself?

This is a good question and it's covered by the second assumption in §2: "the user's machine is trusted and the pr-audit binary is not replaced".

Defenses adopted by pr-audit:

- **Open source** + **MIT License**: source code fully public; anyone can audit
- **< 2000 lines of gocloc**: a proficient developer can read all the code in 30 minutes
- **Zero third-party dependencies (or ≤ 2)**: reduces supply-chain risk
- **Reproducible build**: users can build from source and compare against the official release binary (expected in future versions)

If you still don't feel safe, fork and build it yourself — that's the whole point of the MIT License.

### Q5: If I only trust L2 (manual dashboard check), can I skip L1 and look at the trace-id directly?

You can, but it's **not recommended**. Because:

- L1 passing is the foundation of L2 — if L1 fails, the body in your hands may not be the body PrimeRouter declared, and subsequent comparison loses meaning
- L1 is essentially free (local SHA256 done instantly)
- The pr-audit CLI does L1 and shows the L2 entry at the same time; no extra steps needed

### Q6: The CLI output `SELF-CONSISTENT` looks like "pass"; will it give users a false sense of security?

That's exactly what pr-audit's design worried about most. Therefore:

- The output **explicitly states** "rules out accidental corruption, but does NOT prove the body equals what the upstream vendor actually returned"
- **Never uses** a word like `VERIFIED` implying "already verified"
- **Proactively guides** users to L2/L3 (the `Next steps` section is an explicit mandatory next step, not an optional suggestion)

If you have a better wording suggestion, please open a GitHub issue.

### Q7: Why not add an "overall verification score 0-100"?

Because **scoring obscures trust structure**. What does 83 points mean? Different users have different confidences in different tiers; combining into a single score is misleading. pr-audit's principle is: **present the raw facts and let the user judge**.

---

## 8. Comparison with other trust mechanisms

| Mechanism | Who vouches | Verifiable on the spot? | Fine-grained? |
|---|---|---|---|
| SOC2 audit | Third-party auditor | ✗ | ✗ (only covers operational process) |
| Security whitepaper | Vendor itself | ✗ | Limited |
| Open source | Community | ✗ (open code and deployed code may differ) | ✓ |
| **pr-audit** | **The user themselves** | **✓** | **Per-request** |

pr-audit's differentiator is that it **pushes trust verification down to each individual request**. SOC2 tells you "the company is broadly reliable"; pr-audit tells you "this particular request of yours is reliable". They are complementary.

---

## 9. Conclusion

pr-audit's philosophy can be distilled into one sentence:

> **"Trust is earned in bytes, but bytes alone aren't enough."**

Reduce trust from "verbal promises" to "byte comparison", while **honestly acknowledging the limits of byte comparison**. pr-audit won't make PrimeRouter more trustworthy, but it will make "trustworthiness" itself **verifiable**, and will explain **clearly** how far each tier of verification can go.

For users willing to stay skeptical, that's enough.

---

## Appendix A: One diagram

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

---

## Appendix B: Version history

| Version | Date | Notes |
|---|---|---|
| v1.0 | 2026-04-22 | Initial version, consolidated from PRD v0.3 (revised by real measurement) + IMPLEMENTATION.md |
