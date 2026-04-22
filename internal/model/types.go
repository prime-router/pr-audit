// Package model defines shared types across the verify pipeline.
// Keep it small: only data, no behavior.
package model

import "net/http"

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
)

// Outcome is the single-word verdict we echo to users and CI.
// Phrasing is load-bearing (see docs/trust-model.md §3.4) — never "VERIFIED".
type Outcome string

const (
	OutcomeSelfConsistent Outcome = "self_consistent"
	OutcomeL1Unavailable  Outcome = "l1_unavailable"
	OutcomeL1Fail         Outcome = "l1_fail"
	OutcomeParseError     Outcome = "parse_error"
)

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
}
