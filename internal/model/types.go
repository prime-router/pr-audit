// Package model defines shared types across the verify pipeline.
// Keep it small: only data, no behavior.
package model

import (
	"encoding/json"
	"net/http"
)

// Response is a saved PrimeRouter HTTP response ready to be verified.
// BodyPath is the on-disk location so we can stream-hash without buffering.
type Response struct {
	StatusCode int
	Headers    http.Header
	BodyPath   string // always set; hashing streams from here
	BodyInline []byte // optional: when the body was parsed from a combined file it was buffered
	Source     string // human-readable input description
}

// Usage mirrors the fields we care about from body-level usage objects.
// Present=false means no usage field at all (e.g., error response).
// We parse both OpenAI-style (prompt/completion/total) and Anthropic-style
// (input/output + cache fields) and only surface what we actually found.
type Usage struct {
	Present                  bool `json:"-"`
	PromptTokens             int  `json:"prompt_tokens,omitempty"`
	CompletionTokens         int  `json:"completion_tokens,omitempty"`
	TotalTokens              int  `json:"total_tokens,omitempty"`
	InputTokens              int  `json:"input_tokens,omitempty"`
	OutputTokens             int  `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int  `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int  `json:"cache_read_input_tokens,omitempty"`
}

// CheckStatus is the outcome of one individual verify step.
type CheckStatus string

const (
	StatusPass CheckStatus = "pass"
	StatusFail CheckStatus = "fail"
	StatusWarn CheckStatus = "warn" // evidence absent but not a tampering signal
	StatusSkip CheckStatus = "skip"
)

// Check is one L1 step (header presence, hash match, usage parse, ...).
type Check struct {
	Name    string         `json:"name"`
	Status  CheckStatus    `json:"status"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// TrustLevel is the highest rung of evidence we reached in this run.
type TrustLevel string

const (
	TrustNone             TrustLevel = "none"
	TrustL1Unavailable    TrustLevel = "L1_unavailable"
	TrustL1SelfConsistent TrustLevel = "L1_self_consistent"
	TrustL1Fail           TrustLevel = "L1_fail"
	TrustL3NoEvidence     TrustLevel = "L3_no_evidence_of_tampering"
	TrustL3Fail           TrustLevel = "L3_fail"
	TrustL3Skipped        TrustLevel = "L3_skipped"
	TrustL3Degraded       TrustLevel = "L3_degraded"
)

// Outcome is the single-word verdict we echo to users and CI.
// Phrasing is load-bearing (see docs/trust-model.md §3.4) — never "VERIFIED".
type Outcome string

const (
	OutcomeSelfConsistent        Outcome = "self_consistent"
	OutcomeL1Unavailable         Outcome = "l1_unavailable"
	OutcomeL1Fail                Outcome = "l1_fail"
	OutcomeParseError            Outcome = "parse_error"
	OutcomeNoEvidenceOfTampering Outcome = "no_evidence_of_tampering"
	OutcomeL3Fail                Outcome = "l3_fail"
	OutcomeL3Skipped             Outcome = "l3_skipped"
	OutcomeL3Degraded            Outcome = "l3_degraded"
)

// L3Strategy names which reconciliation method replay used (or why it didn't).
type L3Strategy string

const (
	L3TiktokenOffline L3Strategy = "tiktoken_offline"
	L3CountTokensAPI  L3Strategy = "count_tokens_api"
	L3Structural      L3Strategy = "structural"
	L3Skipped         L3Strategy = "skipped"
)

// ReplayParams captures replay's CLI inputs after flag parsing.
// VendorKey is never serialised — see internal/output/json.go and §9 of the spec.
type ReplayParams struct {
	HeadersPath  string
	BodyPath     string
	ResponsePath string
	RequestPath  string
	VendorKey    string
}

// ReplayRequest is the original request JSON the user saved before calling
// PrimeRouter. We only unmarshal the fields we need; Messages and Tools
// are kept as raw bytes so vendor-specific reconcilers can re-encode them
// without lossy round-tripping.
type ReplayRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	Stream   bool            `json:"stream,omitempty"`
}

// NextStep points the user at a stronger verification layer (L2/L3).
type NextStep struct {
	Level   string `json:"level"`
	Action  string `json:"action"`
	URL     string `json:"url,omitempty"`
	Command string `json:"command,omitempty"`
}

// Input describes where we loaded the response from.
type Input struct {
	Source    string `json:"source"`
	SizeBytes int64  `json:"size_bytes"`
}

// Result is the full verify report — the thing we serialize to JSON or render.
type Result struct {
	Version           string     `json:"version"`
	Command           string     `json:"command"`
	Timestamp         string     `json:"timestamp"`
	Input             Input      `json:"input"`
	TrustLevelReached TrustLevel `json:"trust_level_reached"`
	Vendor            string     `json:"vendor,omitempty"`
	Model             string     `json:"model,omitempty"`
	TraceID           string     `json:"trace_id,omitempty"`
	Usage             *Usage     `json:"usage,omitempty"`
	Checks            []Check    `json:"checks"`
	NextSteps         []NextStep `json:"next_steps,omitempty"`
	Outcome           Outcome    `json:"result"`
	ExitCode          int        `json:"exit_code"`

	// L3 fields — populated only by the replay command. omitempty keeps
	// verify's existing JSON output byte-identical for L1-only consumers.
	L3Strategy L3Strategy `json:"l3_strategy,omitempty"`
	L3Checks   []Check    `json:"l3_checks,omitempty"`
}
