# pr-audit L1+L2+L3 Full Implementation — Task List

> **Related Spec**: [`full-l1-l2-l3.md`](./full-l1-l2-l3.md)
> **Created**: 2026-04-23
> **Status**: Not started
>
> **Prerequisites**: This document assumes you have read `full-l1-l2-l3.md` and `docs/trust-model.md`.
> Each task includes: what to do, why, how to do it (with code examples), acceptance criteria, and references to existing code.

---

## Prerequisites

- Go 1.22 (no generics or newer version features)
- New dependencies: `github.com/spf13/cobra` + `github.com/pkoukk/tiktoken-go`
- Remove CI gocloc line count limit (drop the < 2000 line constraint; L3 features exceed the cap)
- All tests must pass with `go test -race`
- `make lint` (gofmt + go vet) must pass
- Three-platform CI (ubuntu/macos/windows)
- **Never output `VERIFIED`**, L1 pass = `SELF-CONSISTENT`, L1+L3 pass = `NO EVIDENCE OF TAMPERING`

---

## Phase 1: CLI Skeleton Migration (cobra)

### T1.1 Introduce cobra dependency

**What to do**: Add the cobra dependency to go.mod.

**How to do it**:
```bash
cd pr-audit
go get github.com/spf13/cobra
go mod tidy
```

**Acceptance criteria**:
- `go.mod` contains `github.com/spf13/cobra`
- `go.sum` exists and `go mod verify` passes
- `go build ./...` compiles successfully

---

### T1.2 Refactor main.go to cobra rootCmd

**What to do**: Change `cmd/pr-audit/main.go` from hand-written `os.Args` switch to a cobra rootCmd.

**Existing code** (`cmd/pr-audit/main.go`):
```go
var version = "v0.1.0-dev"

func main() {
    if len(os.Args) < 2 {
        fmt.Fprint(os.Stderr, usage)
        os.Exit(2)
    }
    switch os.Args[1] {
    case "--version", "-v", "version":
        fmt.Println("pr-audit", version)
    case "--help", "-h", "help":
        fmt.Print(usage)
    case "verify":
        os.Exit(runVerify(os.Args[2:]))
    default:
        fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
        fmt.Fprint(os.Stderr, usage)
        os.Exit(2)
    }
}
```

**Target code**:
```go
var version = "v0.1.0-dev"

var rootCmd = &cobra.Command{
    Use:   "pr-audit",
    Short: "PrimeRouter response integrity audit",
    Long:  "pr-audit audits PrimeRouter LLM-gateway response integrity via a three-tier trust model (L1 local hash, L2 vendor dashboard, L3 replay).",
    SilenceErrors: true,
    SilenceUsage:  true,
    Version:       version,
}

func init() {
    rootCmd.SetVersionTemplate("pr-audit {{.Version}}\n")
}

func main() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

**Key details**:
- `SilenceErrors: true` + `SilenceUsage: true`: prevents cobra from automatically outputting usage when a subcommand returns an error
- `SetVersionTemplate`: makes `--version` output format consistent with the existing one (`pr-audit v0.1.0-dev`)
- `main()` does not use `os.Exit(rootCmd.Execute())`, because subcommands may internally use `os.Exit(10)` and other non-0/1 exit codes
- **ldflags injection**: `var version` remains unchanged; `go build -ldflags "-X main.version=..."` continues to work

**Acceptance criteria**:
- `./pr-audit --version` outputs `pr-audit v0.1.0-dev`
- `./pr-audit --help` outputs cobra-format help
- `./pr-audit` with no arguments outputs help (no crash)

---

### T1.3 Refactor verify.go to cobra subcommand

**What to do**: Change `cmd/pr-audit/verify.go` from hand-written `flag.NewFlagSet` to a cobra subcommand.

**Existing code** (`cmd/pr-audit/verify.go`):
```go
func runVerify(args []string) int {
    fs := flag.NewFlagSet("verify", flag.ContinueOnError)
    headers := fs.String("headers", "", ...)
    body := fs.String("body", "", ...)
    response := fs.String("response", "", ...)
    format := fs.String("format", "human", ...)
    // ... parameter validation ...
    result := verify.Run(verify.Params{...})
    // ... rendering ...
    return result.ExitCode
}
```

**Target code**:
```go
var verifyCmd = &cobra.Command{
    Use:   "verify",
    Short: "Verify a saved PrimeRouter response (L1 self-consistency + L2 hints)",
    Long:  verifyUsage,  // keep the existing verifyUsage constant
    RunE: func(cmd *cobra.Command, args []string) error {
        // ... parameter validation logic (same as existing) ...
        result := verify.Run(verify.Params{
            HeadersPath:  headersFlag,
            BodyPath:     bodyFlag,
            ResponsePath: responseFlag,
        })
        if formatFlag == "json" {
            output.RenderJSON(os.Stdout, result)
        } else {
            output.RenderHuman(os.Stdout, result)
        }
        os.Exit(result.ExitCode)
        return nil  // unreachable
    },
}

func init() {
    verifyCmd.Flags().StringVar(&headersFlag, "headers", "", "HTTP headers file")
    verifyCmd.Flags().StringVar(&bodyFlag, "body", "", "HTTP body file")
    verifyCmd.Flags().StringVar(&responseFlag, "response", "", "combined headers+body file (curl -i)")
    verifyCmd.Flags().StringVar(&formatFlag, "format", "human", "output format: human|json")
    rootCmd.AddCommand(verifyCmd)
}
```

**Key details**:
- Inside `RunE`, use `os.Exit(result.ExitCode)` to exit directly; do not rely on cobra's error return
  - cobra `Execute()` can only return exit 0 or 1, but we need 0/10/11/20/40/99
  - This is a hard requirement because pr-audit's exit codes are load-bearing (CI depends on them)
- The `verifyUsage` constant can be kept or replaced by cobra's `Long` field
- All flag names and semantics remain unchanged for backward compatibility

**Acceptance criteria**:
- `./pr-audit verify --help` outputs cobra-format help
- `./pr-audit verify --headers ... --body ...` works identically to before the migration
- exit codes 0/10/20 are correct in each scenario
- All existing `go test -race ./internal/verify/` tests pass (these test `verify.Run()`, not the CLI layer)

---

### T1.4 Regression verification

**What to do**: Confirm that the cobra migration has not broken any existing functionality.

**Verification checklist**:
```bash
make build                          # compiles successfully
make test                           # all tests pass
make lint                           # gofmt + go vet pass
./pr-audit --version                # outputs correct version
./pr-audit verify --help            # outputs help
./pr-audit verify                   # error with no args
./pr-audit verify --headers ... --body ...   # L1 works normally
./pr-audit verify --headers ... --body ... --format json  # JSON output works normally
```

**Regression testing focus**:
- exit codes 0 (L1 pass), 10 (L1 fail), 20 (input error) are correct in each path
- `--format json` output format is consistent with pre-migration
- `--response` combined mode still works normally

---

## Phase 2: Data Model Extension

### T2.1 Extend model/types.go

**What to do**: Add L3-related types and constants to `internal/model/types.go`.

**Existing code** (`internal/model/types.go:51-69`):
```go
type TrustLevel string
const (
    TrustNone             TrustLevel = "none"
    TrustL1Unavailable    TrustLevel = "L1_unavailable"
    TrustL1SelfConsistent TrustLevel = "L1_self_consistent"
    TrustL1Fail           TrustLevel = "L1_fail"
)

type Outcome string
const (
    OutcomeSelfConsistent Outcome = "self_consistent"
    OutcomeL1Unavailable  Outcome = "l1_unavailable"
    OutcomeL1Fail         Outcome = "l1_fail"
    OutcomeParseError     Outcome = "parse_error"
)
```

**Additions**:
```go
const (
    TrustL3NoEvidence TrustLevel = "L3_no_evidence_of_tampering"
    TrustL3Fail       TrustLevel = "L3_fail"
    TrustL3Skipped    TrustLevel = "L3_skipped"
    TrustL3Degraded   TrustLevel = "L3_degraded"
)

const (
    OutcomeNoEvidenceOfTampering Outcome = "no_evidence_of_tampering"
    OutcomeL3Fail                Outcome = "l3_fail"
    OutcomeL3Skipped             Outcome = "l3_skipped"
    OutcomeL3Degraded            Outcome = "l3_degraded"
)

type L3Strategy string
const (
    L3TiktokenOffline L3Strategy = "tiktoken_offline"
    L3CountTokensAPI  L3Strategy = "count_tokens_api"
    L3Structural      L3Strategy = "structural"
    L3Skipped         L3Strategy = "skipped"
)

type ReplayParams struct {
    HeadersPath  string
    BodyPath     string
    ResponsePath string
    RequestPath  string
    VendorKey    string
}

type ReplayRequest struct {
    Model    string          `json:"model"`
    Messages json.RawMessage `json:"messages"`
    Tools    json.RawMessage `json:"tools,omitempty"`
    Stream   bool            `json:"stream,omitempty"`
}
```

**New fields on Result struct**:
```go
type Result struct {
    // existing fields unchanged
    Version           string     `json:"version"`
    Command           string     `json:"command"`
    // ... all retained ...

    // new
    L3Strategy L3Strategy `json:"l3_strategy,omitempty"`
    L3Checks   []Check    `json:"l3_checks,omitempty"`
}
```

**Why `json.RawMessage`**: `ReplayRequest.Messages` and `Tools` preserve the original JSON bytes because different vendor APIs require different formats. During replay, the original JSON is passed through directly to the count_tokens API without re-serialization.

**Acceptance criteria**:
- Compiles successfully
- Existing tests are unaffected (new fields all have zero values; JSON `omitempty` does not affect existing output)
- `ReplayRequest` JSON deserialization works correctly

---

### T2.2 Add vendor/count_tokens.go

**What to do**: Define each vendor's count_tokens endpoint info and capability query functions.

**Code**:
```go
package vendor

type CountTokensEndpoint struct {
    URL    string
    Method string
    Header map[string]string
}

var countTokensEndpoints = map[string]CountTokensEndpoint{
    Anthropic: {
        URL:    "https://api.anthropic.com/v1/messages/count_tokens",
        Method: "POST",
        Header: map[string]string{
            "anthropic-version": "2023-06-01",
            "content-type":     "application/json",
        },
    },
    Gemini: {
        URL:    "https://generativelanguage.googleapis.com/v1beta/models/{model}:countTokens",
        Method: "POST",
        Header: map[string]string{
            "content-type": "application/json",
        },
    },
}

func HasCountTokens(v string) bool {
    _, ok := countTokensEndpoints[v]
    return ok
}

func HasOfflineTokenizer(v string) bool {
    return v == OpenAI || v == AzureOpenAI
}

func CountTokensEndpoint(v string) (CountTokensEndpoint, bool) {
    ep, ok := countTokensEndpoints[v]
    return ep, ok
}
```

**Acceptance criteria**:
- `HasCountTokens("anthropic") == true`
- `HasCountTokens("openai") == false` (OpenAI uses tiktoken, not the count_tokens API)
- `HasOfflineTokenizer("openai") == true`
- `HasOfflineTokenizer("anthropic") == false`
- Unit tests cover all vendors

---

## Phase 3: L3 Replay Core Logic

### T3.1 Create internal/replay/replay.go — main flow

**What to do**: Implement `replay.Run(p ReplayParams) model.Result`, the entry function for L3.

**Flow pseudocode**:
```go
func Run(p ReplayParams) model.Result {
    res := model.Result{
        Version:   verify.SchemaVersion,
        Command:   "replay",
        Timestamp: time.Now().UTC().Format(time.RFC3339),
    }

    // 1. Run L1 (reuse verify logic)
    l1Result := verify.Run(verify.Params{
        HeadersPath:  p.HeadersPath,
        BodyPath:     p.BodyPath,
        ResponsePath: p.ResponsePath,
    })

    // Copy L1 conclusions to res
    res.Input = l1Result.Input
    res.Vendor = l1Result.Vendor
    res.Model = l1Result.Model
    res.TraceID = l1Result.TraceID
    res.Checks = l1Result.Checks
    res.Usage = l1Result.Usage

    // 2. L1 failed → do not continue to L3
    if l1Result.Outcome == model.OutcomeL1Fail {
        res.Outcome = l1Result.Outcome
        res.TrustLevelReached = l1Result.TrustLevelReached
        res.ExitCode = 10
        return res
    }

    // 3. Parse request.json
    req, err := parseRequest(p.RequestPath)
    if err != nil {
        res.Checks = append(res.Checks, model.Check{
            Name: "parse_request", Status: model.StatusFail, Message: err.Error(),
        })
        res.Outcome = model.OutcomeParseError
        res.ExitCode = 20
        return res
    }

    // 4. Determine vendor key source (flag takes priority, fallback to environment variable)
    vendorKey := p.VendorKey
    if vendorKey == "" {
        vendorKey = os.Getenv("PR_AUDIT_VENDOR_KEY")
    }

    // 5. L3 strategy routing
    vendor := l1Result.Vendor
    var l3Checks []model.Check
    var l3Strategy model.L3Strategy

    switch {
    case vendor == vendor.OpenAI || vendor == vendor.AzureOpenAI:
        l3Checks, l3Strategy = replayOpenAI(req, l1Result.Usage, l1Result.Model)
    case vendor == vendor.Anthropic && vendorKey != "":
        l3Checks, l3Strategy = replayAnthropic(req, l1Result.Usage, l1Result.Model, vendorKey)
    case vendor == vendor.Anthropic && vendorKey == "":
        l3Checks, l3Strategy = replaySkipped(vendor, "vendor-key required for Anthropic count_tokens API")
    case vendor == vendor.Gemini && vendorKey != "":
        l3Checks, l3Strategy = replayGemini(req, l1Result.Usage, l1Result.Model, vendorKey)
    case vendor == vendor.Gemini && vendorKey == "":
        l3Checks, l3Strategy = replaySkipped(vendor, "vendor-key required for Gemini countTokens API")
    default:
        l3Checks, l3Strategy = replaySkipped(vendor, "no count_tokens endpoint or offline tokenizer available")
    }

    res.L3Strategy = l3Strategy
    res.L3Checks = l3Checks

    // 6. Compute L3 outcome + exit code
    res.Outcome, res.TrustLevelReached, res.ExitCode = resolveL3Outcome(l1Result, l3Checks, l3Strategy)

    // 7. Always output L2 hints
    res.NextSteps = buildNextSteps(res.Vendor, res.TraceID, p)

    return res
}
```

**Key design decisions**:
- **Reuse `verify.Run`** rather than reimplementing L1 logic. verify.Run returns a complete Result; replay only needs to copy the L1 conclusions
- **Stop on L1 failure**: if the body is not a reliable baseline, L3 is meaningless
- **Vendor key is not strictly required**: OpenAI plain text does not need a key; other vendors are SKIPPED (not an error) when the key is missing

**`parseRequest` function**:
```go
func parseRequest(path string) (model.ReplayRequest, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return model.ReplayRequest{}, fmt.Errorf("read request file: %w", err)
    }
    var req model.ReplayRequest
    if err := json.Unmarshal(data, &req); err != nil {
        return model.ReplayRequest{}, fmt.Errorf("parse request JSON: %w", err)
    }
    if req.Model == "" {
        return model.ReplayRequest{}, fmt.Errorf("request JSON missing 'model' field")
    }
    return req, nil
}
```

**`resolveL3Outcome` function**:
```go
func resolveL3Outcome(l1 model.Result, l3Checks []model.Check, strategy model.L3Strategy) (model.Outcome, model.TrustLevel, int) {
    if strategy == model.L3Skipped {
        return l1.Outcome, l1.TrustLevelReached, 0
    }

    hasFail := false
    for _, ck := range l3Checks {
        if ck.Status == model.StatusFail {
            hasFail = true
            break
        }
    }

    if hasFail {
        return model.OutcomeL3Fail, model.TrustL3Fail, 40
    }

    isDegraded := strategy == model.L3Structural
    if isDegraded {
        return model.OutcomeL3Degraded, model.TrustL3Degraded, 0
    }

    return model.OutcomeNoEvidenceOfTampering, model.TrustL3NoEvidence, 0
}
```

**Acceptance criteria**:
- Compiles successfully
- With stub implementations (all vendors take the skipped path), returns a structurally complete Result
- L1 fail → does not continue to L3
- L1 pass + unknown vendor → L3 skipped

---

### T3.2 Create internal/replay/openai.go — tiktoken offline reconciliation

**What to do**: Implement offline prompt_tokens reconciliation for OpenAI plain text chat requests.

**Core logic**:
```go
func replayOpenAI(req model.ReplayRequest, reported model.Usage, responseModel string) ([]model.Check, model.L3Strategy) {
    var checks []model.Check

    // Detect tools field
    hasTools := len(req.Tools) > 0 && string(req.Tools) != "null" && string(req.Tools) != "[]"

    if hasTools {
        // Degraded path
        checks = append(checks, model.Check{
            Name:    "l3_strategy",
            Status:  model.StatusSkip,
            Message: "structural (OpenAI tool calls — prompt_tokens not reliably verifiable offline)",
        })
        // lower-bound estimate (optional)
        lowerBound := estimatePromptTokensLowerBound(req)
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusSkip,
            Message: fmt.Sprintf("SKIPPED — not reliably verifiable offline (lower-bound estimate: %d)", lowerBound),
        })
        // model check can still be performed
        checks = append(checks, checkModelMatch(req.Model, responseModel))
        return checks, model.L3Structural
    }

    // Plain text path: tiktoken offline computation
    checks = append(checks, model.Check{
        Name:   "l3_strategy",
        Status: model.StatusPass,
        Message: "tiktoken offline (OpenAI plain text)",
    })

    computed, err := countTokensTiktoken(req)
    if err != nil {
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusWarn,
            Message: fmt.Sprintf("tiktoken computation failed: %v", err),
        })
    } else {
        checks = append(checks, comparePromptTokens(computed, reported.PromptTokens))
    }

    checks = append(checks, checkModelMatch(req.Model, responseModel))
    return checks, model.L3TiktokenOffline
}
```

**`countTokensTiktoken` implementation**:
```go
func countTokensTiktoken(req model.ReplayRequest) (int, error) {
    enc, err := tiktoken.EncodingForModel(req.Model)
    if err != nil {
        // If model is not in tiktoken's known list, fallback to cl100k_base
        enc, err = tiktoken.GetEncoding("cl100k_base")
        if err != nil {
            return 0, fmt.Errorf("tiktoken encoding not found: %w", err)
        }
    }

    // Parse messages
    var messages []struct {
        Role    string `json:"role"`
        Content string `json:"content"`
        Name    string `json:"name,omitempty"`
    }
    if err := json.Unmarshal(req.Messages, &messages); err != nil {
        return 0, fmt.Errorf("parse messages: %w", err)
    }

    total := 0
    for _, msg := range messages {
        // tiktoken-go's CountMessageTokens already includes message overhead
        count := enc.CountTokens(msg.Content)
        total += count
    }

    // Add message overhead (3-4 token format overhead per message)
    // Note: tiktoken-go's CountMessageTokens method may already handle this;
    // must verify with a known prompt. If not, add manually:
    total += len(messages) * 3  // tokens_per_message
    total += 3                  // priming tokens

    return total, nil
}
```

**`comparePromptTokens` implementation**:
```go
func comparePromptTokens(computed, reported int) model.Check {
    details := map[string]any{
        "computed": computed,
        "reported": reported,
    }
    if computed == reported {
        return model.Check{
            Name: "prompt_tokens_match", Status: model.StatusPass, Details: details,
        }
    }
    diff := reported - computed
    details["difference"] = fmt.Sprintf("%+d", diff)
    return model.Check{
        Name:    "prompt_tokens_match",
        Status:  model.StatusFail,
        Message: fmt.Sprintf("prompt_tokens mismatch: computed %d, reported %d", computed, reported),
        Details: details,
    }
}
```

**`checkModelMatch` implementation**:
```go
func checkModelMatch(requestModel, responseModel string) model.Check {
    details := map[string]any{
        "request":  requestModel,
        "response": responseModel,
    }
    if requestModel == responseModel {
        return model.Check{
            Name: "model_match", Status: model.StatusPass, Details: details,
        }
    }
    return model.Check{
        Name:    "model_match",
        Status:  model.StatusFail,
        Message: fmt.Sprintf("model mismatch: request=%s, response=%s", requestModel, responseModel),
        Details: details,
    }
}
```

**Key details**:
1. **tiktoken message overhead**: tiktoken-go's `CountTokens` only counts the text itself, excluding message format overhead. Must be added manually. OpenAI's official overhead rules: `tokens_per_message=3`, `tokens_per_name=1`, `priming=3`. **When implementing, you must verify the overhead calculation is correct using a known prompt.**
2. **Model fallback**: If `EncodingForModel` fails (new model), fallback to `cl100k_base` (common encoding for GPT-4 series). However, this may cause inaccurate token counts; add a warning in the output.
3. **Tools degradation**: OpenAI has private overhead token rules for tool calls that tiktoken cannot compute. Only a lower-bound (text portion token count) can be given, marked as `SKIPPED`.

**Acceptance criteria**:
- Unit test: `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}` → tiktoken returns a reasonable token count
- Unit test: tools request → L3 DEGRADED
- Unit test: model mismatch → fail check
- Unit test: prompt_tokens mismatch → fail check + difference value

---

### T3.3 Create internal/replay/anthropic.go — count_tokens API reconciliation

**What to do**: Implement online prompt_tokens reconciliation for the Anthropic vendor, including **prompt cache summation** (this is the most critical implementation detail).

**Core logic**:
```go
func replayAnthropic(req model.ReplayRequest, reported model.Usage, responseModel string, vendorKey string) ([]model.Check, model.L3Strategy) {
    var checks []model.Check

    checks = append(checks, model.Check{
        Name: "l3_strategy", Status: model.StatusPass,
        Message: "count_tokens API (Anthropic)",
    })

    // Call Anthropic count_tokens API
    result, err := callAnthropicCountTokens(req, vendorKey)
    if err != nil {
        // API unavailable → degrade
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusWarn,
            Message: fmt.Sprintf("count_tokens API unavailable: %v", err),
        })
        checks = append(checks, checkModelMatch(req.Model, responseModel))
        return checks, model.L3Structural  // degrade
    }

    // [CRITICAL] Cache summation
    // count_tokens returns "total without cache"
    // PrimeRouter reports usage split into three parts: input + cache_creation + cache_read
    // Reconciliation formula: count_tokens_result == input + cache_creation + cache_read
    expected := reported.InputTokens + reported.CacheCreationInputTokens + reported.CacheReadInputTokens

    details := map[string]any{
        "count_tokens_result":        result,
        "input_tokens":               reported.InputTokens,
        "cache_creation_input_tokens": reported.CacheCreationInputTokens,
        "cache_read_input_tokens":     reported.CacheReadInputTokens,
        "expected_sum":               expected,
    }

    if result == expected {
        checks = append(checks, model.Check{
            Name: "prompt_tokens_match", Status: model.StatusPass, Details: details,
        })
    } else {
        details["difference"] = result - expected
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusFail,
            Message: fmt.Sprintf("prompt_tokens mismatch: count_tokens=%d, sum(input+cache_creation+cache_read)=%d", result, expected),
            Details: details,
        })
    }

    checks = append(checks, checkModelMatch(req.Model, responseModel))
    return checks, model.L3CountTokensAPI
}
```

**`callAnthropicCountTokens` implementation**:
```go
func callAnthropicCountTokens(req model.ReplayRequest, vendorKey string) (int, error) {
    // Build request body (same format as /v1/messages, but stream not supported)
    body := map[string]any{
        "model":    req.Model,
        "messages": json.RawMessage(req.Messages),
    }
    bodyBytes, err := json.Marshal(body)
    if err != nil {
        return 0, fmt.Errorf("marshal request: %w", err)
    }

    httpReq, err := http.NewRequest("POST",
        "https://api.anthropic.com/v1/messages/count_tokens",
        bytes.NewReader(bodyBytes))
    if err != nil {
        return 0, fmt.Errorf("create request: %w", err)
    }
    httpReq.Header.Set("x-api-key", vendorKey)
    httpReq.Header.Set("anthropic-version", "2023-06-01")
    httpReq.Header.Set("content-type", "application/json")

    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(httpReq)
    if err != nil {
        return 0, classifyNetError(err)  // returns DNS/TLS/timeout error
    }
    defer resp.Body.Close()

    if resp.StatusCode == 401 {
        return 0, fmt.Errorf("authentication failed (check vendor-key)")
    }
    if resp.StatusCode >= 500 {
        return 0, fmt.Errorf("upstream error: HTTP %d", resp.StatusCode)
    }
    if resp.StatusCode != 200 {
        return 0, fmt.Errorf("unexpected status: HTTP %d", resp.StatusCode)
    }

    var result struct {
        InputTokens int `json:"input_tokens"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return 0, fmt.Errorf("decode response: %w", err)
    }
    return result.InputTokens, nil
}
```

**`classifyNetError` implementation** (network error classification, affects exit code):
```go
func classifyNetError(err error) error {
    var dnsErr *net.DNSError
    if errors.As(err, &dnsErr) {
        return &netError{Code: 31, Message: "DNS resolution failed: " + dnsErr.Error()}
    }
    var certErr tls.CertificateVerificationError
    if errors.As(err, &certErr) {
        return &netError{Code: 32, Message: "TLS certificate error: " + certErr.Error()}
    }
    if strings.Contains(err.Error(), "timeout") {
        return &netError{Code: 33, Message: "upstream timeout"}
    }
    return fmt.Errorf("network error: %w", err)
}
```

**Anthropic prompt cache summation — this is the most bug-prone area**:

| Scenario | count_tokens returns | PrimeRouter reports input_tokens | cache_creation | cache_read | Sum result | Reconciliation conclusion |
|---|---|---|---|---|---|---|
| No cache | 42 | 42 | 0 | 0 | 42 | ✓ pass |
| Cache hit | 1066 | 42 | 0 | 1024 | 1066 | ✓ pass |
| Cache creation | 1066 | 0 | 1066 | 0 | 1066 | ✓ pass |
| PrimeRouter over-reports | 42 | 420 | 0 | 0 | 420 | ✗ fail (42 ≠ 420)|
| No summation (wrong!) | 1066 | 42 | — | — | 42 | ✗ false positive (1066 ≠ 42)|

**Consequence of not summing**: Any Anthropic request that hits the cache will be falsely reported as "PrimeRouter over-reported input_tokens". This is the pitfall explicitly called out in `docs/trust-model.md` §4.4.

**Acceptance criteria**:
- Unit test (httptest mock): count_tokens=100, input=30+cache_creation=40+cache_read=30 → pass
- Unit test (httptest mock): count_tokens=100, input=50 → fail (100 ≠ 50, summation is needed to be correct)
- Unit test: API 401 → "authentication failed"
- Unit test: API 5xx → warn + degrade
- Unit test: DNS error → exit 31
- Unit test: TLS error → exit 32
- Unit test: Timeout → exit 33
- Unit test: model mismatch → fail

---

### T3.4 Create internal/replay/gemini.go — countTokens API reconciliation

**What to do**: Implement online token reconciliation for the Gemini vendor.

**Core challenge**: PrimeRouter returns an OpenAI-format body, but the Gemini countTokens API requires Gemini format. Message format conversion is needed.

**Core logic**:
```go
func replayGemini(req model.ReplayRequest, reported model.Usage, responseModel string, vendorKey string) ([]model.Check, model.L3Strategy) {
    var checks []model.Check

    checks = append(checks, model.Check{
        Name: "l3_strategy", Status: model.StatusPass,
        Message: "countTokens API (Gemini)",
    })

    // OpenAI → Gemini format conversion
    geminiContents, err := convertOpenAIToGemini(req.Messages)
    if err != nil {
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusWarn,
            Message: fmt.Sprintf("message format conversion failed: %v", err),
        })
        checks = append(checks, checkModelMatch(req.Model, responseModel))
        return checks, model.L3Structural
    }

    // Call Gemini countTokens API
    result, err := callGeminiCountTokens(req.Model, geminiContents, vendorKey)
    if err != nil {
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusWarn,
            Message: fmt.Sprintf("countTokens API unavailable: %v", err),
        })
        checks = append(checks, checkModelMatch(req.Model, responseModel))
        return checks, model.L3Structural
    }

    // Reconciliation
    reportedTotal := reported.InputTokens + reported.OutputTokens
    details := map[string]any{
        "count_tokens_result": result,
        "reported_input":       reported.InputTokens,
        "reported_output":      reported.OutputTokens,
        "reported_sum":         reportedTotal,
    }
    if result == reportedTotal {
        checks = append(checks, model.Check{
            Name: "prompt_tokens_match", Status: model.StatusPass, Details: details,
        })
    } else {
        checks = append(checks, model.Check{
            Name:    "prompt_tokens_match",
            Status:  model.StatusFail,
            Message: fmt.Sprintf("totalTokens mismatch: countTokens=%d, reported input+output=%d", result, reportedTotal),
            Details: details,
        })
    }

    checks = append(checks, checkModelMatch(req.Model, responseModel))
    return checks, model.L3CountTokensAPI
}
```

### T3.4a Create internal/replay/convert.go — OpenAI→Gemini format conversion

**What to do**: Convert OpenAI-format messages to Gemini-format contents.

**Conversion rules**:

| OpenAI format | Gemini format |
|---|---|
| `{"role":"system","content":"..."}` | `{"role":"user","parts":[{"text":"[System] ..."}]}` or put into `systemInstruction` |
| `{"role":"user","content":"..."}` | `{"role":"user","parts":[{"text":"..."}]}` |
| `{"role":"assistant","content":"..."}` | `{"role":"model","parts":[{"text":"..."}]}` |
| `content: "string"` | `parts: [{text: "string"}]` |
| `content: [{type:"text", text:"..."}]` | `parts: [{text: "..."}]` |
| `content: [{type:"image_url", ...}]` | **Skip** (multimodal not supported in MVP)|

**Implementation**:
```go
type geminiContent struct {
    Role  string        `json:"role"`
    Parts []geminiPart  `json:"parts"`
}

type geminiPart struct {
    Text string `json:"text,omitempty"`
}

func convertOpenAIToGemini(messagesJSON json.RawMessage) ([]geminiContent, error) {
    var messages []struct {
        Role    string `json:"role"`
        Content string `json:"content"`
    }
    if err := json.Unmarshal(messagesJSON, &messages); err != nil {
        return nil, fmt.Errorf("parse messages: %w", err)
    }

    var contents []geminiContent
    for _, msg := range messages {
        role := msg.Role
        switch role {
        case "system":
            role = "user"
        case "assistant":
            role = "model"
        }
        contents = append(contents, geminiContent{
            Role:  role,
            Parts: []geminiPart{{Text: msg.Content}},
        })
    }
    return contents, nil
}
```

**MVP limitations**: Only handles plain text `content: "string"` format. Array format `content: [{type:"text",...}]` and multimodal `content: [{type:"image_url",...}]` are deferred to a later iteration.

**Acceptance criteria**:
- Unit test: `system` → `user`, `assistant` → `model`, `user` → `user`
- Unit test: multimodal content → degrade or skip
- Unit test: empty messages → empty contents

---

### T3.5 Create internal/replay/degraded.go — degraded/skipped logic

**What to do**: Handle all paths that do not support full L3 reconciliation.

**Code**:
```go
func replaySkipped(vendor, reason string) ([]model.Check, model.L3Strategy) {
    return []model.Check{
        {
            Name:    "l3_strategy",
            Status:  model.StatusSkip,
            Message: fmt.Sprintf("SKIPPED — %s", reason),
        },
    }, model.L3Skipped
}

func replayDegraded(vendor, reason string, lowerBound int) ([]model.Check, model.L3Strategy) {
    checks := []model.Check{
        {
            Name:    "l3_strategy",
            Status:  model.StatusSkip,
            Message: fmt.Sprintf("structural — %s", reason),
        },
        {
            Name:    "prompt_tokens_match",
            Status:  model.StatusSkip,
            Message: fmt.Sprintf("SKIPPED — not reliably verifiable (lower-bound estimate: %d)", lowerBound),
        },
    }
    return checks, model.L3Structural
}
```

**Acceptance criteria**:
- Unit test: `replaySkipped("zhipu", "no count_tokens endpoint")` → `L3Strategy=skipped`, check status=skip
- Unit test: `replaySkipped("unknown", "vendor unknown")` → same as above
- Unit test: `replayDegraded("openai", "tool calls", 42)` → `L3Strategy=structural`, prompt_tokens=skip+lower-bound

---

## Phase 4: CLI Command Integration

### T4.1 Create cmd/pr-audit/replay.go

**What to do**: Create the `replay` cobra subcommand.

**Code**:
```go
var replayCmd = &cobra.Command{
    Use:   "replay",
    Short: "End-to-end replay verification (L1 + L2 + L3)",
    Long: `pr-audit replay — verify PrimeRouter response integrity end-to-end

Replays the original request directly to the upstream vendor using your own
API key and compares deterministic fields (prompt_tokens, model) against
PrimeRouter's reported values.

Usage:
  pr-audit replay --headers <file> --body <file> --request <file> --vendor-key <key>
  pr-audit replay --response <file> --request <file> --vendor-key <key>

Vendor key can also be set via PR_AUDIT_VENDOR_KEY environment variable.`,
    RunE: func(cmd *cobra.Command, args []string) error {
        // Parameter validation
        hasSplit := headersFlag != "" || bodyFlag != ""
        hasCombined := responseFlag != ""
        if hasSplit && hasCombined {
            fmt.Fprintln(os.Stderr, "cannot mix --response with --headers/--body")
            os.Exit(20)
        }
        if !hasSplit && !hasCombined {
            fmt.Fprintln(os.Stderr, "provide either --headers + --body or --response")
            os.Exit(20)
        }
        if hasSplit && (headersFlag == "" || bodyFlag == "") {
            fmt.Fprintln(os.Stderr, "both --headers and --body are required in split mode")
            os.Exit(20)
        }
        if requestFlag == "" {
            fmt.Fprintln(os.Stderr, "--request is required for replay")
            os.Exit(20)
        }

        // vendor-key: flag takes priority, fallback to environment variable
        vk := vendorKeyFlag
        if vk == "" {
            vk = os.Getenv("PR_AUDIT_VENDOR_KEY")
        }

        result := replay.Run(replay.ReplayParams{
            HeadersPath:  headersFlag,
            BodyPath:     bodyFlag,
            ResponsePath: responseFlag,
            RequestPath:  requestFlag,
            VendorKey:    vk,
        })

        if formatFlag == "json" {
            output.RenderJSON(os.Stdout, result)
        } else {
            output.RenderHuman(os.Stdout, result)
        }
        os.Exit(result.ExitCode)
        return nil
    },
}

func init() {
    replayCmd.Flags().StringVar(&headersFlag, "headers", "", "HTTP headers file")
    replayCmd.Flags().StringVar(&bodyFlag, "body", "", "HTTP body file")
    replayCmd.Flags().StringVar(&responseFlag, "response", "", "combined headers+body file (curl -i)")
    replayCmd.Flags().StringVar(&requestFlag, "request", "", "original request JSON file (required)")
    replayCmd.Flags().StringVar(&vendorKeyFlag, "vendor-key", "", "upstream vendor API key (or set PR_AUDIT_VENDOR_KEY)")
    replayCmd.Flags().StringVar(&formatFlag, "format", "human", "output format: human|json")
    rootCmd.AddCommand(replayCmd)
}
```

**Flag variables**: `headersFlag`, `bodyFlag`, `responseFlag`, `formatFlag` are shared with verify (defined as package-level variables in main.go), or defined independently (to avoid conflicts).

**Acceptance criteria**:
- `./pr-audit replay --help` outputs correctly
- `./pr-audit replay` (no args) → error exit 20
- `./pr-audit replay --request req.json` (missing response) → error exit 20
- `./pr-audit replay --request req.json --headers h.txt` (missing body) → error exit 20
- vendor-key environment variable fallback works normally

---

### T4.2 Verify command final confirmation

**What to do**: Confirm that the verify command works fully after the cobra migration.

**Acceptance criteria**: Same as T1.4.

---

## Phase 5: Output Rendering Extension

### T5.1 Extend output/human.go to support L3

**What to do**: Add an `[L3 · End-to-end replay checks]` section to the human-readable output.

**Existing rendering structure** (`output/human.go:18-45`):
```
pr-audit verify v0.1.0

[L1 · Self-consistency checks]
[✓] ...
[✓] ...

Result: SELF-CONSISTENT
  ...

⚠  To obtain stronger evidence:
  [L2 · ...]
  [L3 · ...]

Exit code: 0 (L1 passed)
```

**Target rendering structure** (replay command):
```
pr-audit replay v0.1.0

[L1 · Self-consistency checks]
[✓] ...

[L3 · End-to-end replay checks]     ← new section
[✓] L3 strategy: tiktoken offline (OpenAI plain text)
[✓] prompt_tokens match
    computed: 42  reported: 42
[✓] model field match
    request: gpt-4o-mini  response: gpt-4o-mini

Result: NO EVIDENCE OF TAMPERING
  ...

  vendor=openai  model=gpt-4o-mini  trace-id=req_01HXYZABC

⚠  For additional confidence:
  [L2 · External attestation]
    ...

Exit code: 0 (L1+L3 passed)
```

**Key changes**:
- `RenderHuman` detects `result.Command == "replay"` and renders the L3 section
- L3 section format is consistent with L1 section (reuse `renderCheck` function)
- `renderVerdict` adds L3 branch
- L2 hints are retained
- Exit code wording updated (`L1+L3 passed` / `L3 failed` / `L3 skipped`, etc.)

**Acceptance criteria**:
- replay L3 pass → `NO EVIDENCE OF TAMPERING` (green)
- replay L3 fail → `L3 FAIL` (red) + difference details
- replay L3 degraded → `L3 DEGRADED` (yellow) + degradation reason
- replay L3 skipped → `L3 SKIPPED` (gray)
- replay L1 fail → L3 section not rendered
- verify command output unchanged

---

### T5.2 Extend JSON output

**What to do**: The new `L3Strategy` and `L3Checks` fields on the `Result` struct will be automatically serialized by `json.Encoder`; no changes needed to `output/json.go`.

**But verify that**:
- `json:"l3_strategy,omitempty"` and `json:"l3_checks,omitempty"` do not appear in verify command output (zero value + omitempty)
- They are correctly included in replay command output

**Acceptance criteria**:
- `pr-audit verify --format json` output is consistent with current (no l3_ fields)
- `pr-audit replay --format json` output includes `l3_strategy` + `l3_checks`

---

## Phase 6: CI / Build Changes

### T6.1 Remove CI gocloc line count limit

**What to do**: Delete the `gocloc` job from `.github/workflows/ci.yml`.

**Existing CI** (`.github/workflows/ci.yml:36-49`):
```yaml
  gocloc:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: go install github.com/hhatto/gocloc/cmd/gocloc@latest
      - name: Enforce < 2000 Go code lines
        run: |
          lines=$(gocloc --not-match-d='testdata|vendor' --include-lang=Go . | awk '/^Go/ {print $5}')
          echo "Go code lines: $lines"
          if [ -z "$lines" ]; then echo "gocloc produced no Go line count"; exit 1; fi
          if [ "$lines" -gt 2000 ]; then echo "::error::exceeds 2000 lines ($lines)"; exit 1; fi
```

**Target**: Delete the entire `gocloc` job. Keep the `build-test` and `lint` jobs.

**Makefile**: The `gocloc` target is retained (for local manual use); CI does not depend on it.

**Acceptance criteria**: CI no longer runs the gocloc job; `make gocloc` can still be run locally.

---

### T6.2 Update AGENTS.md

**What to do**: Update AGENTS.md to reflect all changes.

**Change points**:
- Remove < 2000 line constraint
- New dependencies: cobra + tiktoken-go
- Add `replay` command description
- Add exit codes 31/32/33/40
- Add testing notes (L3-related)
- Architecture section adds `internal/replay/`

---

## Phase 7: End-to-End Verification

### T7.1 OpenAI tiktoken path

**Steps**:
```bash
# Prepare fixtures
dir=$(mktemp -d)
body='{"id":"x","model":"gpt-4o-mini","usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}'
echo -n "$body" > "$dir/body.bin"
hash=$(echo -n "$body" | sha256sum | awk '{print $1}')
printf "HTTP/2 200\r\ncontent-type: application/json\r\nx-upstream-vendor: openai\r\nx-upstream-sha256: sha256:%s\r\n\r\n" "$hash" > "$dir/headers.txt"
echo '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}' > "$dir/request.json"

# Run replay
./pr-audit replay --headers "$dir/headers.txt" --body "$dir/body.bin" --request "$dir/request.json"

# Verify: tiktoken-computed prompt_tokens matches the reported value in body → L3 pass
# Modify prompt_tokens in body to an incorrect value → L3 fail (exit 40)
```

### T7.2 Degraded path

**Steps**:
```bash
# request.json contains tools
echo '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"weather?"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}' > "$dir/request-tools.json"
./pr-audit replay --headers "$dir/headers.txt" --body "$dir/body.bin" --request "$dir/request-tools.json"
# Expected: L3 DEGRADED, exit 0
```

### T7.3 Skipped path

**Steps**:
```bash
# Construct a fixture with vendor=unknown
# (body model not in known prefix list)
# Expected: L3 SKIPPED, exit 0
```

### T7.4 vendor-key environment variable

**Steps**:
```bash
PR_AUDIT_VENDOR_KEY=sk-test ./pr-audit replay --headers ... --body ... --request ...
# Expected: key read from environment variable
```

### T7.5 Full CI pass

```bash
make build      # passes
make test       # passes
make lint       # passes
# Three-platform CI green
```

---

## Execution Order and Dependencies

```
Phase 1 (cobra migration) ─── ~0.5 days
  T1.1 → T1.2 → T1.3 → T1.4

Phase 2 (data model) ─── ~0.5 days
  T2.1 → T2.2

Phase 3 (L3 core logic) ─── ~2-3 days
  T3.1 → T3.2 (openai)
       → T3.3 (anthropic)  ← can be parallel
       → T3.4+T3.4a (gemini) ← can be parallel
       → T3.5 (degraded)   ← can be parallel

Phase 4 (CLI integration) ─── ~0.5 days
  T4.1 → T4.2

Phase 5 (output rendering) ─── ~0.5 days
  T5.1 → T5.2

Phase 6 (CI) ─── ~0.5 days
  T6.1 + T6.2

Phase 7 (end-to-end) ─── ~0.5 days
  T7.1 → T7.2 → T7.3 → T7.4 → T7.5
```

**Total estimate**: 4.5-6 days

**Critical path**: T1 → T2 → T3.1 → T3.2 → T4 → T5 → T7

---

## Key Risks

| Risk | Impact | Mitigation |
|---|---|---|
| tiktoken-go message overhead calculation inconsistent with OpenAI server-side | OpenAI prompt_tokens false positives | Manually verify with known prompts; note tokenizer version differences in output |
| tiktoken-go needs to download encoding data | CI first build may fail | CI cache or pre-download |
| Anthropic count_tokens API format change | Reconciliation failure | httptest mock testing + anthropic-version header pinning |
| Gemini message format conversion complexity | Multimodal/array-format content cannot be handled | MVP only supports plain text; multimodal deferred to later iteration |
| cobra migration causes exit code regression | CI false positives | Full regression verification at end of Phase 1; cobra `SilenceErrors` + `os.Exit` inside subcommands |
| Network error classification incomplete | Exit codes 31/32/33 inaccurate | Cover common net error types; unrecognized errors default to 33 |
| Vendor key leak concerns | Users reluctant to use replay | Key not written to logs/JSON output; CLI output redacted |
| Anthropic count_tokens rate limiting | Batch auditing hits the wall | Document rate limit risk; degrade on failure |
