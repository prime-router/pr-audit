# PrimeRouter Integration Document

> **Purpose**: the complete technical contract for the server-side capabilities PrimeRouter must implement for pr-audit.
> **Audience**: PrimeRouter server-side developers (one-api fork maintainers).
> **Related documents**: [`specs.md`](./specs.md) (design basis), [`tasks.md`](./tasks.md) (Track A task breakdown), [`trust-model.md`](./trust-model.md) (trust model).
> **Last updated**: 2026-04-22 (based on grayscale environment measurements)

---

## 1. Integration overview

PrimeRouter needs to attach **three headers** to each response + satisfy **four engineering hard constraints** so that the client-side pr-audit CLI can perform byte-level integrity verification.

| Deliverable | Current state (2026-04-22) | Effort |
|---|---|---|
| `x-upstream-trace-id` header | ❌ missing (but `x-log-id` already carries the corresponding value) | 0.5 day |
| `x-upstream-vendor` header | ❌ missing | 0.5 day |
| `x-upstream-sha256` header | ❌ missing | 3-5 days (requires raw-passthrough refactor) |
| GLM path byte-level passthrough | ✓ **verified in testing** | — |
| Aliyun WAF byte fidelity | ⏳ not tested | 0.5 day |
| SSE hash delivery (v0.1.1) | ⏳ done in v0.1.1 phase | 1-2 days |
| Byte passthrough for other vendors (OpenAI/Anthropic) | ⏳ not tested | 1 day each |

---

## 2. Hash object definition (the server-side implementation's "alignment point")

The bytes covered by `x-upstream-sha256` are **strictly defined as**:

> **The complete byte sequence the client obtains at the SDK layer by calling `response.content` / `await response.read()` / `response.body`.**

Equivalently, on the server side: **the byte sequence after all HTTP framing (chunked size lines, HTTP/2 DATA frame headers) and `Content-Encoding` (gzip/br decompression) have been resolved, and before any business logic takes place**.

**Why this layer**:

| Layer | Obtainable server-side? | Reproducible by the client SDK? |
|---|---|---|
| Raw bytes close to the TLS socket | ✓ | ✗ (not available at the SDK layer) |
| HTTP wire bytes (including framing) | ✓ | ✗ (HTTP/2 ↔ 1.1 conversion changes framing) |
| **Application-layer body (after framing + decompression)** | **✓** | **✓** |
| Parsed-JSON object | ✓ | ✗ (byte order not reproducible) |

Only the **application-layer body bytes** layer can be aligned on both ends.

---

## 3. Four engineering hard constraints

**Violating any one of these will cause pr-audit to produce massive false positives under real traffic**, and project credibility will immediately collapse:

### Constraint 1: Hash compute point

Compute SHA256 over the application-layer body bytes **after** HTTP framing is resolved and `Content-Encoding` is decoded, and **before** any business processing (JSON parsing, field rewriting, log redaction, etc.).

### Constraint 2: Byte fidelity

The bytes delivered to the user must be **byte-identical** to the bytes covered by the hash. Implementation suggestion: **buffer the application-layer body → compute the hash → deliver the same buffered copy**.

**Forbidden**: hash-and-stream-through all the way — any framing change during passthrough will break byte identity.

**Code example (pseudo-code, one-api fork refactor)**:

```go
// On the response return path
upstreamBytes, err := io.ReadAll(upstreamResp.Body)
if err != nil { /* handle */ }

// Compute hash
hash := sha256.Sum256(upstreamBytes)
headerValue := "sha256:" + hex.EncodeToString(hash[:])

// Side-channel parsing, used only for logging / billing / usage recording
// Do not re-serialize the parsing result and deliver that
var parsed ChatCompletionResp
_ = json.Unmarshal(upstreamBytes, &parsed)
go recordMetrics(parsed)   // asynchronous, does not block delivery

// Deliver the raw bytes (not the result of json.Marshal)
w.Header().Set("x-upstream-sha256", headerValue)
w.Header().Set("x-upstream-trace-id", parsed.ID)
w.Header().Set("x-upstream-vendor", "zhipu")  // mapped by channel type
w.Write(upstreamBytes)  // ← the bytes the hash covers must == the bytes delivered here
```

### Constraint 3: Content-Encoding policy

**Decision**: outbound `Content-Encoding: identity` (no compression).

**Reasons**:

- Eliminates risk from inconsistent auto-decompression behavior across client SDKs
- The hash object definition directly equals the wire bytes — unambiguous
- Bandwidth cost is acceptable (LLM responses don't compress well to begin with; compression gains are even smaller in SSE streaming scenarios)

**Handling from upstream to PrimeRouter**: when PrimeRouter requests upstream, use `Accept-Encoding: identity` to tell upstream not to compress; if upstream forces compression, PrimeRouter **decompresses first**, then computes the hash and delivers.

### Constraint 4: No re-framing by intermediate layers

There must be no intermediate layer between PrimeRouter and the user that modifies the body. Common components that must be disabled or bypassed:

- CDN response optimization / minification / auto-decompression
- WAF response rewriting (see §5 Aliyun WAF handling)
- L7 load balancer HTTP protocol conversion (HTTP/2 ↔ 1.1)
- Enterprise gateway content injection

---

## 4. Evidence Headers value format contract

The following is the formal contract for **server-side generation / client-side parsing**. Any deviation is an implementation bug.

### 4.1 `x-upstream-trace-id`

**Format**: string; the upstream vendor's raw trace ID directly, without a prefix.

| Upstream | Example value | Source |
|---|---|---|
| Zhipu (GLM) | `202604221650485cc5b21ecf1c41fc` | `id` / `request_id` field of the response body |
| OpenAI | `chatcmpl-ABC123xyz...` or `req_xyz123` | HTTP header `x-request-id` or body `id` |
| Anthropic | `msg_01A2B3C4D5E6F7G8H9I0J1K2` | HTTP header `x-request-id` |
| Gemini | `<digits or UUID>` | HTTP header `x-goog-request-id` or body `responseId` |

**Server-side implementation rule**: prefer taking it from the upstream response header; if the header is absent, take it from the body.

**[HARD REQUIREMENT] The server must not generate this value itself** — once discovered (e.g. by comparing against a trace from a direct upstream connection), it immediately invalidates itself.

### 4.2 `x-upstream-sha256`

**Format**: `sha256:` prefix + 64-character **lowercase** hexadecimal.

**Example**:

```
x-upstream-sha256: sha256:6adafd0c9e64fa76327328cafde9cbc5024b71f2291db3eb5ee97dabd9ee9ec6
```

**Why a prefix**: future support for other hash algorithms (SHA3-256) is possible; the prefix lets the CLI dispatch by algorithm. v0.1.0 supports only SHA256; for any other prefix the client reports `unsupported hash algorithm`.

**Server-side computation**: see the code example under §3 Constraint 2.

**Client-side verification flow**:

1. Read the header value; separate the `sha256:` prefix from the hex part
2. Locally compute `sha256.Sum256(body)` over the body bytes
3. Compare the hex strings (case-insensitive)

### 4.3 `x-upstream-vendor`

**Format**: lowercase enum string.

**Allowed values (v0.1.0)**:

| Value | Meaning |
|---|---|
| `openai` | OpenAI official |
| `azure-openai` | Azure OpenAI service |
| `anthropic` | Anthropic official |
| `zhipu` | Zhipu AI (GLM series) |
| `gemini` | Google Gemini |
| `deepseek` | DeepSeek |
| `moonshot` | Moonshot (Kimi) |
| `unknown` | Unidentifiable or intentionally not disclosed to the client |

**Extensions**: adding a new vendor requires synchronous updates to this list + the client-side enum table.

**Client behavior**: values not in the enum → treated as `unknown`; L3 reconciliation is skipped; L1/L2 still run normally.

---

## 5. Aliyun WAF handling (empirical observation)

**Empirical finding**: both direct Zhipu and PrimeRouter responses carry `set-cookie: acw_tc=...` — indicating both sit behind Aliyun WAF.

**Conjecture**: Aliyun WAF is **transparent by default** for `Content-Type: application/json` API traffic (otherwise it would break all API clients). But **"conjecture" is not "confirmation"**; a one-time measurement is required.

**Test method** (§6 will include this as part of integration acceptance):

```go
// fake-upstream.go (place in the server-side test environment, or any public-facing server)
package main
import "net/http"
func main() {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"hello":"world"}`))  // fixed 17 bytes
    })
    http.ListenAndServe(":9999", nil)
}
```

```bash
# Expected hash
echo -n '{"hello":"world"}' | sha256sum
# 93a23971a914e5eacbf0a8d25154cda309c3c1c72fafdd4a6c4cc952... (first 32 bytes)

# In the PrimeRouter test channel, configure upstream to point to http://localhost:9999
# Send a request from a public-network client via WAF to PrimeRouter
# Compare the response body's sha256sum with the expected value above

sha256sum response.bin
```

**Result branches**:

- ✓ matches → Aliyun WAF is transparent to JSON body; continue
- ✗ doesn't match → diff to see what WAF changed (added newlines, altered content-type?); configure WAF policy specifically to pass the API path through; or move the API domain out of WAF

---

## 6. SSE hash delivery (implemented in v0.1.1)

**Empirical finding**: the current SSE is true streaming (`x-accel-buffering: no`, HTTP/2, LF line endings), with the last chunk carrying `usage`. Good foundation.

**Rejected alternatives**:

- ❌ **HTTP Trailer**: httpx has no trailer API; browser `fetch()` doesn't expose trailers; many SDKs just drop them. Client compatibility is poor.
- ❌ **Buffered one-shot delivery**: destroys streaming UX; unacceptable in LLM scenarios.

**Chosen approach ("pr_audit event")**: in the SSE stream, after the last content chunk and before `data: [DONE]`, insert a pr-audit-specific event.

**Example**:

```
data: {"id":"xxx","choices":[{"delta":{"content":"final segment"}}]}

data: {"id":"xxx","choices":[{"finish_reason":"stop","delta":{"content":""}}],"usage":{...}}

data: {"pr_audit":{"upstream_sha256":"sha256:abc...","upstream_trace_id":"...","upstream_vendor":"zhipu"}}

data: [DONE]

```

**Hash object (SSE scenario)**:

- The bytes the hash covers = the SSE body bytes, **excluding** the `data: {"pr_audit":...}` line and the blank line that follows it
- When the client pr-audit verifies SSE:
  1. Read the entire SSE body into a buffer
  2. Find the line starting with `data: {"pr_audit":`; remove that whole line (including the trailing blank line)
  3. Compute SHA256 over the remaining bytes
  4. Compare with the `upstream_sha256` in the pr_audit event

**Advantages**: any SSE-capable client can read it; preserves streaming UX; hash object definition is clear (non-circular); client parsing logic is simple.

---

## 7. Architecture self-check checklist

Before implementing the §3 constraints, confirm your own deployment. If the answer to any item is "exists and cannot be refactored", that link cannot carry pr-audit:

- [ ] **HTTP protocol conversion**: does an HTTP/2 ↔ HTTP/1.1 conversion exist between PrimeRouter and the user? If yes, on which hop?
- [ ] **CDN / edge**: does the response path go through a CDN like Cloudflare / Fastly / AWS CloudFront? Are response optimization, minification, or auto-decompression enabled?
- [ ] **WAF / enterprise gateway**: is there a WAF / API gateway / service mesh sidecar that rewrites response body or headers? (Measurement: Aliyun WAF should be transparent by default for `application/json`, but requires a one-time check per §5)
- [ ] **TLS termination location**: does TLS terminate at the PrimeRouter process itself? Or is there an L7 load balancer (AWS ALB, Nginx, Envoy, etc.) in front? A front L7 LB introduces a layer of potential re-framing.
- [ ] **Upstream Content-Encoding negotiation**: what `Accept-Encoding` does PrimeRouter declare when calling upstream? When upstream returns a compressed body, does PrimeRouter currently "pass the compressed bytes through" or "decompress then re-compress"? The new scheme requires "after decompression".
- [ ] **SSE boundary handling**: is the current SSE stream forwarding implementation "transparently forwarding socket bytes" or "parse events → re-serialize"? The latter introduces framing differences. (Measurement: the current grayscale version is already true streaming — good foundation)

---

## 8. Minimum test for integration acceptance

**Goal**: prove with 3 real samples that the server-side hash and the client SDK's computed hash are bit-identical.

**Steps**:

1. Deploy hash computation logic in the test environment per §3 constraints (after Track A A1/A2/A3 are done)
2. Use `curl -D headers.txt -o body.bin` to send 3 requests to PrimeRouter:
   - [ ] **Sample A**: glm-4-flash or gpt-4o-mini **non-streaming** (plain text)
   - [ ] **Sample B**: gpt-4o-mini **non-streaming + tool call** (at least 1 function)
   - [ ] **Sample C**: glm-4-flash or gpt-4o-mini **streaming** (done in v0.1.1 phase)
3. For each sample, locally compute `sha256sum body.bin`
4. Compare with the response header's `x-upstream-sha256`; **pass when all 3 samples match**

**Acceptance script** (bash):

```bash
for sample in A B C; do
    declared=$(grep -i "^x-upstream-sha256:" headers-$sample.txt | awk '{print $2}' | tr -d '\r')
    actual="sha256:$(sha256sum body-$sample.bin | awk '{print $1}')"
    if [ "$declared" == "$actual" ]; then
        echo "[PASS] sample $sample"
    else
        echo "[FAIL] sample $sample: declared=$declared actual=$actual"
    fi
done
```

**Artifacts**: commit the 3 passing samples to `pr-audit/testdata/golden/` as a permanent regression baseline. Any subsequent server-side change that breaks these 3 golden samples must trigger a CI alert.

---

## 9. External commitments (brand layer)

After completing the §3 constraints, the server side must also do:

- [ ] **Publicly commit to the four hard constraints in this document**: add them to the audit copy on the PrimeRouter website as part of the trust model
- [ ] **Deploy "self-audit CI"**: periodically scrape the production environment's own responses and run pr-audit verify; alert on failure — PrimeRouter uses pr-audit on itself first
- [ ] **Adjust website copy**: drop the "byte-level diff replay" phrasing (LLM non-determinism makes that promise untrue), and replace it with a precise two-part commitment:

```diff
- 04 Open source pr-audit CLI. Replay any request to upstream with your own vendor key — byte-level diff.
+ 04 Open source pr-audit CLI.
+    · local sha256(body) == x-upstream-sha256: byte-level verification of the PrimeRouter → you link integrity
+    · Use your own vendor key to connect directly to upstream: verify usage / trace-id / model fields
+      (the response content itself cannot be byte-diffed due to LLM non-determinism; the tool is explicitly honest about this)
```

---

## 10. Track A task overview

(In sync with [`tasks.md`](./tasks.md) Track A)

| # | Task | Effort | Section in this doc |
|---|---|---|---|
| A1 | Add `x-upstream-trace-id` header | 0.5 day | §4.1 |
| A2 | Add `x-upstream-vendor` header | 0.5 day | §4.3 |
| A3 | Raw passthrough + `x-upstream-sha256` | 3-5 days | §3 constraints + §4.2 |
| A4 | Aliyun WAF byte fidelity test | 0.5 day | §5 |
| A5 | SSE hash delivery (v0.1.1) | 1-2 days | §6 |
| A6 | Byte passthrough measurement for other vendors | 1 day each | §8 |
| A7 | Website copy adjustment | 0.5 day | §9 |

---

## 11. Empirical reference values from development

These are observations captured on 2026-04-22 from the `https://www.primerouter.xyz/v1/` grayscale environment, provided for reference during server-side refactoring:

### Current non-streaming response sample (glm-4-flash)

```
HTTP/2 200
date: Wed, 22 Apr 2026 08:22:34 GMT
content-type: application/json; charset=UTF-8
content-length: 1683
set-cookie: acw_tc=2760776217768461466017633eb47345a055c38e2139f6a444520ec2ac6695;path=/;HttpOnly;Max-Age=1800
strict-transport-security: max-age=31536000; includeSubDomains
vary: Accept-Encoding
x-log-id: 202604221622267ddeb1984c594c42      ← the future x-upstream-trace-id value
x-oneapi-request-id: 202604220822265660988958268d9d6OUgD6saL

{"choices":[...],"id":"202604221622267ddeb1984c594c42","model":"glm-4-flash",...}
              ↑ the id inside the body matches x-log-id — confirms "upstream trace has been passed through"
```

**Conclusions**:

- The `x-log-id` value is already what we want — adding `x-upstream-trace-id: ${x-log-id}` is almost zero cost
- Byte-level body passthrough is already achieved (Zhipu direct vs PrimeRouter routed diff is only in ids/timestamps)

### Current SSE response sample (glm-4-flash stream)

```
HTTP/2 200
content-type: text/event-stream
cache-control: no-cache
x-accel-buffering: no                          ← true streaming

data: {"id":"xxx","choices":[{"delta":{"content":"If"}}]}

data: {"id":"xxx","choices":[{"delta":{"content":" you"}}]}
...
data: {"id":"xxx","choices":[{"finish_reason":"length",...}],"usage":{...}}

data: [DONE]
```

**Conclusion**: good foundation; adding a `pr_audit` event conforms to the existing SSE format and does not disrupt streaming characteristics.

---

## Appendix: Document version history

| Version | Date | Notes |
|---|---|---|
| v1.0 | 2026-04-22 | Initial version, merged from `server-alignment-checklist.md` and `IMPLEMENTATION.md §11`, revised based on grayscale environment measurements |
