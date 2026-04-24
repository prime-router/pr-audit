// Package replay implements the L3 end-to-end reconciliation pipeline.
//
// The package is organised by reconciliation strategy:
//
//	replay.go    — top-level Run(): wires L1 → strategy routing → L2 hints
//	openai.go    — tiktoken offline path (OpenAI plain-text chat)
//	anthropic.go — count_tokens API + prompt-cache summation
//	gemini.go    — countTokens API
//	convert.go   — OpenAI → Gemini message format adapter
//	degraded.go  — common skip / degrade check builders
//
// Trust model invariants (see docs/trust-model.md):
//   - Replay never sends data to PrimeRouter.
//   - The user's vendor key is used only against hard-coded upstream domains.
//   - L1 fail short-circuits L3: a contaminated body cannot serve as baseline.
package replay

import (
	"fmt"

	"github.com/primerouter/pr-audit/internal/model"
)

// replaySkipped builds the L3 check list when reconciliation cannot run at
// all (unknown vendor, missing key, vendor without count_tokens, etc.).
// L3Skipped is NOT a failure — exit code stays 0; we just degrade the
// trust verdict back to L1's level.
func replaySkipped(reason string) ([]model.Check, model.L3Strategy) {
	return []model.Check{
		{
			Name:    "l3_strategy",
			Status:  model.StatusSkip,
			Message: fmt.Sprintf("SKIPPED — %s", reason),
		},
	}, model.L3Skipped
}

// degradedStrategyCheck announces a partial-coverage L3 strategy (e.g.
// OpenAI tools where prompt_tokens cannot be hard-reconciled but model
// can). Callers append per-field checks (skipped prompt_tokens + a real
// model_match) after this header check.
func degradedStrategyCheck(reason string) model.Check {
	return model.Check{
		Name:    "l3_strategy",
		Status:  model.StatusSkip,
		Message: fmt.Sprintf("structural — %s", reason),
	}
}

// skippedPromptTokensCheck records that prompt_tokens was not verified
// because the vendor / request shape doesn't permit hard reconciliation.
func skippedPromptTokensCheck(reason string) model.Check {
	return model.Check{
		Name:    "prompt_tokens_match",
		Status:  model.StatusSkip,
		Message: fmt.Sprintf("SKIPPED — %s", reason),
	}
}

// checkModelMatch compares the model declared in the saved request against
// the model PrimeRouter reported in the response body. A mismatch is the
// canonical signal of silent model downgrade (trust-model §1.1).
func checkModelMatch(requestModel, responseModel string) model.Check {
	details := map[string]any{
		"request":  requestModel,
		"response": responseModel,
	}
	if responseModel == "" {
		return model.Check{
			Name:    "model_match",
			Status:  model.StatusWarn,
			Message: "response body did not include a model field",
			Details: details,
		}
	}
	if requestModel == responseModel {
		return model.Check{Name: "model_match", Status: model.StatusPass, Details: details}
	}
	// Many vendors return a versioned response model (e.g. request asks
	// "gpt-4o-mini" → response says "gpt-4o-mini-2024-07-18"). Treat that
	// as a pass with a note rather than a hard fail; outright downgrade
	// (different family entirely) still trips fail.
	if isVersionedVariant(requestModel, responseModel) {
		details["note"] = "response is a dated variant of request model"
		return model.Check{Name: "model_match", Status: model.StatusPass, Details: details}
	}
	return model.Check{
		Name:    "model_match",
		Status:  model.StatusFail,
		Message: fmt.Sprintf("model mismatch: request=%s, response=%s", requestModel, responseModel),
		Details: details,
	}
}

// isVersionedVariant returns true when response looks like "<request>-YYYYMMDD"
// or "<request>-YYYY-MM-DD" — i.e. the same family with a date suffix. We
// keep it simple to avoid false negatives on legitimate vendor variants.
func isVersionedVariant(request, response string) bool {
	if len(response) <= len(request)+1 {
		return false
	}
	if response[:len(request)] != request {
		return false
	}
	if response[len(request)] != '-' {
		return false
	}
	suffix := response[len(request)+1:]
	// Loose check: at least 4 chars, all digits or hyphens.
	if len(suffix) < 4 {
		return false
	}
	for _, r := range suffix {
		if !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}
