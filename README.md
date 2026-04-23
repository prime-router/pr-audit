# pr-audit

> **Trust is earned in bytes, but bytes alone aren't enough.**

`pr-audit` is an independent response-integrity auditing CLI for [PrimeRouter](https://www.primerouter.xyz/). It lets any developer **verify on-site, locally** that PrimeRouter did not tamper with upstream responses, did not misreport token usage, and did not silently downgrade models - while **honestly stating how far this verification can actually go**.

- **Open source** (MIT License), < 2000 lines of Go, readable in about 30 minutes
- **Zero trust in PrimeRouter**: the tool itself never calls PrimeRouter APIs to derive verification conclusions
- **Three progressive verification levels**: L1 local self-consistency / L2 vendor dashboard / L3 end-to-end replay
- **Honest capability boundaries**: explicitly states what it can do and **cannot do**

---

## Why this tool exists

As an LLM API gateway, PrimeRouter sits between users and upstream vendors like OpenAI, Anthropic, Zhipu, and Google. This "man-in-the-middle" position gives it the **ability** to do four things users cannot easily detect:

1. Tamper with response content
2. Misreport token usage
3. Not actually forward requests
4. Silently swap GPT-4 for GPT-3.5

Traditional trust mechanisms (SOC2, security whitepapers, third-party audits) work for **enterprises**, but are not directly verifiable for **developers** in real time - they are "someone says so," not "you can `curl` and verify now."

The core claim of `pr-audit` is:

> **"Don't trust what we say. Verify it yourself."**

---

## How it works

PrimeRouter includes three categories of evidence in each response:

| Header / Field | Meaning |
|---|---|
| `x-upstream-trace-id` | Original upstream trace ID, can be checked in the vendor dashboard |
| `x-upstream-sha256` | SHA256 of the original upstream body |
| `x-upstream-vendor` | Upstream vendor identifier (`openai` / `anthropic` / `zhipu` / `gemini`) |
| `usage` (in body) | Token counts determined by upstream and passed through unchanged by PrimeRouter |

`pr-audit` turns these from unilateral server claims into facts users can independently verify with a single command.

---

## Quickstart (5 minutes)

### Install

```bash
# Download single binary from Releases (Linux / macOS / Windows)
curl -L https://github.com/primerouter/pr-audit/releases/latest/download/pr-audit-$(uname -s)-$(uname -m) -o pr-audit
chmod +x pr-audit && sudo mv pr-audit /usr/local/bin/

# Or build from source
git clone https://github.com/primerouter/pr-audit.git
cd pr-audit && make build
```

### First verification

```bash
# 1) Send a request to PrimeRouter and save the full response
curl -i -X POST 'https://www.primerouter.xyz/v1/chat/completions' \
  -H 'Authorization: Bearer YOUR_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}' \
  -D headers.txt -o body.bin

# 2) Audit this response
pr-audit verify --headers headers.txt --body body.bin
```

Expected output:

```text
pr-audit verify v0.1.0

[L1 · Self-consistency checks]
[✓] x-upstream-sha256 header present
[✓] x-upstream-trace-id header present (vendor: openai)
[✓] Body SHA256 matches declared value
[✓] Usage field parsed: prompt=12 completion=8 total=20

Result: SELF-CONSISTENT
  PrimeRouter's declared hash matches the body it delivered.
  This rules out accidental corruption, but does NOT prove the body
  equals what the upstream vendor actually returned.

⚠  Next steps for stronger evidence:

  [L2] Verify trace-id on vendor dashboard:
       https://platform.openai.com/logs?request_id=req_xxx

  [L3] Re-run with your own vendor key:
       pr-audit replay --body body.bin --vendor-key $OPENAI_KEY
       (replay available in v0.2)

Exit code: 0 (L1 passed)
```

---

## Three-level verification (one diagram)

```text
                    validation strength
                           ▲
                           │
                L3 ────────┤  Direct upstream reconciliation with your own key
             (auto)        │  → hard-check prompt_tokens, compare model field
                           │  → requires your own vendor key
                           │
                L2 ────────┤  trace-id is queryable on vendor dashboard
             (manual)      │  → proves request reached upstream
                           │  → requires manual dashboard login
                           │
                L1 ────────┤  local sha256(body) ≡ x-upstream-sha256
             (auto)        │  → PrimeRouter is self-consistent (not proof of honesty)
                           │  → this is what pr-audit verify runs by default
                           │
                None ──────┤  no evidence headers at all
                           │  → warns "PrimeRouter auditing is not enabled"
                           └──>
                              evidence coverage
```

**Key point**: Passing L1 is **not the same as** "PrimeRouter is honest."
It only proves internal consistency with PrimeRouter's own declarations. If PrimeRouter acts maliciously, it can alter the body and recompute the hash - L1 alone cannot detect that. **Real verification depends on L2 + L3**.

This is not a flaw in `pr-audit`; it is the theoretical limit of mechanisms based on server-provided signatures alone. We choose to **state this honestly**, rather than using misleading wording like `VERIFIED`.

---

## What pr-audit cannot do

This is the project's **first principle of integrity**. The list is not exhaustive, but covers major items:

- ✗ **Cannot** prove PrimeRouter did not log your prompt content
- ✗ **Cannot** prove PrimeRouter did not leak your API key
- ✗ **Cannot** prove PrimeRouter latency or availability guarantees
- ✗ **Cannot** prove upstream vendors themselves did not manipulate outputs
- ✗ **Cannot** perform byte-level replay diffs (LLM responses are non-deterministic)
- ✗ **Cannot** detect per-user differential responses from PrimeRouter (would require a transparency log; planned as a separate v2.0+ effort)

---

## Current status and roadmap

**Current version**: v0.1.0 (planned, pending Track A + Track B progress)

| CLI Version | Planned scope |
|---|---|
| **v0.1.0** | `verify` command (non-streaming) + OpenAI compatibility + SDK snippets |
| v0.1.1 | SSE streaming support |
| v0.2.0 | `replay` command + L3 reconciliation + Anthropic/Gemini adapters + `proxy` mode |
| v1.0.0 | Stable protocol, independent security audit, long-term support commitment |
| v2.0+ | Transparency Log (separate initiative) |

---

## SDK injection snippet (provided in v0.1.0)

In real development, no one manually runs `curl` for every request. `pr-audit` provides plug-and-play snippets so each call through official SDKs like `openai` / `anthropic` can automatically save responses for auditing:

**Python (httpx + OpenAI SDK):**

```python
import httpx
from openai import OpenAI

def audit_hook(response: httpx.Response):
    response.read()
    with open("/tmp/pr-audit-last.bin", "wb") as f:
        f.write(response.content)  # already de-framed and decompressed

client = OpenAI(
    base_url="https://www.primerouter.xyz/v1",
    http_client=httpx.Client(event_hooks={"response": [audit_hook]}),
)
# After each call: pr-audit verify --body /tmp/pr-audit-last.bin
```

For higher-level wrappers such as LangChain, LiteLLM, or Vercel AI SDK, this style of injection may not be feasible; use `pr-audit proxy` mode once v0.2 is available.

---

## Build

```bash
# Requirement: Go 1.22+
make build          # build binary
make test           # run unit tests
make verify-goldens # regression checks with testdata/golden samples
make lint           # staticcheck + gofmt
make release        # build release binaries for 3 platforms
```

Hard code-size constraints (CI enforced):

- Total Go code (`gocloc`) < 2000 lines (excluding tests and vendor)
- Core verify path < 500 lines
- Third-party dependencies <= 2

---

## Contributing

We **warmly welcome** contributions in these areas:

- **Limit discovery**: if you find something `pr-audit` claims to prove but actually cannot, open a PR to clarify it
- **Vendor adapters**: add detection and L3 reconciliation for vendors beyond OpenAI/Anthropic/Zhipu/Gemini
- **SDK snippets**: additional languages and frameworks
- **Reproducible builds**: improve CI workflows for reproducible build pipelines
- **Security research**: responsible vulnerability disclosure

**Not accepted**:

- Stronger-sounding CLI wording (e.g., replacing `SELF-CONSISTENT` with `VERIFIED`) - this breaks the trust model
- Making `pr-audit` depend on PrimeRouter-owned APIs for verification - this breaks the zero-trust assumption
- Letting `pr-audit` automatically call vendor chat endpoints and spend user token quota - must be opt-in by default

---

## License

MIT License. See `LICENSE`.

---

## Acknowledgements

`pr-audit` is inspired by these projects and ideas:

- [Certificate Transparency](https://certificate.transparency.dev/) - long-term reference for transparency-log direction
- [Signal Protocol](https://signal.org/docs/) - engineering model of open protocols + independent audits
- [Tailscale's open-source client](https://tailscale.com/blog/opensource) - proven pattern of commercial service with an auditable client

---

> If you're a potential PrimeRouter user and still unsure whether to trust the platform - you don't need to trust us. Run **`pr-audit verify`** and see for yourself.
