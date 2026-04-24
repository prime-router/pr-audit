package replay

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	tiktoken_loader "github.com/pkoukk/tiktoken-go-loader"
	"github.com/primerouter/pr-audit/internal/model"
)

// We use the offline BPE loader so pr-audit never has to reach the
// network for tokenizer data. The loader is registered exactly once
// (sync.Once) — tiktoken keeps a package-level loader so re-registering
// is a no-op but harmless; the Once guard avoids races.
var setLoaderOnce sync.Once

func ensureOfflineLoader() {
	setLoaderOnce.Do(func() {
		tiktoken.SetBpeLoader(tiktoken_loader.NewOfflineLoader())
	})
}

// OpenAI per-message overhead constants from the official cookbook
// (https://github.com/openai/openai-cookbook). These have been stable
// across the GPT-4 / GPT-4o / o1 family. GPT-3.5-turbo-0301 used a
// slightly different formula but is far out of warranty here.
const (
	tokensPerMessage = 3
	tokensPerName    = 1
	primingTokens    = 3
)

// replayOpenAI runs the OpenAI L3 path. Returns the L3 check list plus
// the strategy name used. Tools-bearing requests degrade because OpenAI
// applies private overhead to tool definitions that tiktoken cannot
// reproduce — see spec §4.4.
func replayOpenAI(req model.ReplayRequest, reported *model.Usage, responseModel string) ([]model.Check, model.L3Strategy) {
	if hasTools(req.Tools) {
		return openAIDegraded(req, responseModel), model.L3Structural
	}

	checks := []model.Check{{
		Name:    "l3_strategy",
		Status:  model.StatusPass,
		Message: "tiktoken offline (OpenAI plain text)",
	}}

	computed, err := countOpenAITokens(req)
	switch {
	case err != nil:
		// Tokenizer failure (unknown model, malformed messages) is NOT a
		// tampering signal — surface as warn so the caller doesn't flip
		// the verdict to L3 FAIL on what is really our limitation.
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusWarn,
			Message: fmt.Sprintf("tiktoken computation failed: %v", err),
		})
	case reported == nil:
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusWarn,
			Message: "response body has no usage field; cannot compare",
			Details: map[string]any{"computed": computed},
		})
	default:
		checks = append(checks, comparePromptTokens(computed, reported.PromptTokens))
	}

	checks = append(checks, checkModelMatch(req.Model, responseModel))
	return checks, model.L3TiktokenOffline
}

// openAIDegraded is the tools/multimodal path: prompt_tokens cannot be
// hard-reconciled; only the model field is checked.
func openAIDegraded(req model.ReplayRequest, responseModel string) []model.Check {
	checks := []model.Check{
		degradedStrategyCheck("OpenAI tool calls — prompt_tokens not reliably verifiable offline"),
	}

	// Best-effort lower-bound estimate of the text portion. Useful as a
	// sanity floor: if reported prompt_tokens is lower than this, that's
	// already a smoking gun (real value can only be higher, never lower,
	// once tool overhead is added). We surface it but never use it as a
	// fail trigger.
	if lb, err := countOpenAITokens(req); err == nil {
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusSkip,
			Message: fmt.Sprintf("SKIPPED — not reliably verifiable offline (lower-bound estimate: %d)", lb),
			Details: map[string]any{"lower_bound": lb},
		})
	} else {
		checks = append(checks, skippedPromptTokensCheck("not reliably verifiable offline"))
	}

	checks = append(checks, checkModelMatch(req.Model, responseModel))
	return checks
}

// hasTools returns true when the saved request carries a non-empty tools
// array. We tolerate raw JSON that is null / [] / missing.
func hasTools(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	s := string(raw)
	if s == "null" || s == "[]" || s == "{}" {
		return false
	}
	// Basic shape check: parse to []any and confirm non-empty.
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr) > 0
	}
	// Tools could in theory be an object (some experimental APIs); treat
	// any non-array-non-empty value as "tools present".
	return true
}

// openAIMessage matches the subset of OpenAI's message format we tokenise.
// Content is best-effort: a string for plain text, an array for multimodal.
// We accept both shapes and only count the text portion (multimodal is
// already a degraded path; we don't enter it for tool-free requests).
type openAIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Name    string          `json:"name,omitempty"`
}

// countOpenAITokens computes prompt_tokens with OpenAI's per-message
// overhead formula. Verified against fixtures: a single user "hello"
// message yields 8 tokens with gpt-4o-mini, matching OpenAI's own count.
func countOpenAITokens(req model.ReplayRequest) (int, error) {
	ensureOfflineLoader()

	enc, err := tiktoken.EncodingForModel(req.Model)
	if err != nil {
		// Unknown model → fall back to o200k_base (gpt-4o family) which
		// is the most likely encoding for any new OpenAI model. cl100k
		// remains a viable fallback for older GPT-3.5/4 models but they
		// are explicitly recognised by EncodingForModel already.
		enc, err = tiktoken.GetEncoding("o200k_base")
		if err != nil {
			return 0, fmt.Errorf("tiktoken encoding not found: %w", err)
		}
	}

	var messages []openAIMessage
	if len(req.Messages) > 0 {
		if err := json.Unmarshal(req.Messages, &messages); err != nil {
			return 0, fmt.Errorf("parse messages: %w", err)
		}
	}

	total := primingTokens
	for _, msg := range messages {
		total += tokensPerMessage
		total += len(enc.Encode(msg.Role, nil, nil))
		total += len(enc.Encode(messageText(msg.Content), nil, nil))
		if msg.Name != "" {
			total += tokensPerName
		}
	}
	return total, nil
}

// messageText extracts the textual content from a message body. We accept
// either:
//
//	"plain string"
//	[ {"type":"text","text":"..."}, ... ]
//
// Anything else (image_url, audio, file) contributes 0 tokens at the
// text layer — multimodal accuracy needs vendor-private rules and is
// outside L3's hard-reconciliation scope.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try array of parts.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var out string
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				out += p.Text
			}
		}
		return out
	}
	return ""
}

// comparePromptTokens builds a check from a (computed, reported) pair.
// Mismatch is reported as fail with a signed difference for the human
// renderer to format.
func comparePromptTokens(computed, reported int) model.Check {
	details := map[string]any{
		"computed": computed,
		"reported": reported,
	}
	if computed == reported {
		return model.Check{Name: "prompt_tokens_match", Status: model.StatusPass, Details: details}
	}
	diff := reported - computed
	details["difference"] = diff
	return model.Check{
		Name:    "prompt_tokens_match",
		Status:  model.StatusFail,
		Message: fmt.Sprintf("prompt_tokens mismatch: computed %d, reported %d (diff %+d)", computed, reported, diff),
		Details: details,
	}
}
