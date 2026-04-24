package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/primerouter/pr-audit/internal/model"
	"github.com/primerouter/pr-audit/internal/vendor"
)

// geminiEndpointOverride is the test seam mirroring anthropicEndpointOverride.
// Production uses the URL from vendor.LookupCountTokens.
var geminiEndpointOverride string

// replayGemini calls Gemini's countTokens API via the user's own key
// and reconciles against PrimeRouter's reported prompt_tokens.
//
// Important: Gemini's countTokens returns the INPUT token count only —
// it does not run the model. Reconciliation therefore compares against
// reported.PromptTokens (or InputTokens if the body uses Anthropic-style
// fields, but Gemini-vended bodies use OpenAI-style under PrimeRouter's
// adapter). The spec's task pseudocode comparing input+output is wrong;
// we follow §4.3 of the spec instead.
func replayGemini(req model.ReplayRequest, reported *model.Usage, responseModel, vendorKey string) ([]model.Check, model.L3Strategy) {
	checks := []model.Check{{
		Name:    "l3_strategy",
		Status:  model.StatusPass,
		Message: "countTokens API (Gemini)",
	}}

	contents, err := convertOpenAIToGemini(req.Messages)
	if err != nil {
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusWarn,
			Message: fmt.Sprintf("message format conversion failed: %v", err),
		})
		checks = append(checks, checkModelMatch(req.Model, responseModel))
		return checks, model.L3Structural
	}

	result, err := callGeminiCountTokens(req.Model, contents, vendorKey)
	if err != nil {
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusWarn,
			Message: fmt.Sprintf("countTokens API unavailable: %v", err),
		})
		checks = append(checks, checkModelMatch(req.Model, responseModel))
		return checks, model.L3Structural
	}

	if reported == nil {
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusWarn,
			Message: "response body has no usage field; cannot compare",
			Details: map[string]any{"count_tokens_result": result},
		})
		checks = append(checks, checkModelMatch(req.Model, responseModel))
		return checks, model.L3CountTokensAPI
	}

	// Gemini-via-PrimeRouter bodies typically expose prompt_tokens
	// (OpenAI-shape adapter). Some routers may instead expose the
	// Gemini-native input_tokens — accept whichever is non-zero.
	expected := reported.PromptTokens
	if expected == 0 {
		expected = reported.InputTokens
	}

	details := map[string]any{
		"count_tokens_result":    result,
		"reported_prompt_tokens": expected,
	}
	checks = append(checks, comparePromptTokensWithLabel(result, expected, details, "totalTokens", "reported prompt_tokens"))
	checks = append(checks, checkModelMatch(req.Model, responseModel))
	return checks, model.L3CountTokensAPI
}

// comparePromptTokensWithLabel is a Gemini-flavoured variant of
// comparePromptTokens that names the two operands explicitly so the
// human renderer can produce a sensible message.
func comparePromptTokensWithLabel(computed, reported int, details map[string]any, computedLabel, reportedLabel string) model.Check {
	if computed == reported {
		return model.Check{Name: "prompt_tokens_match", Status: model.StatusPass, Details: details}
	}
	details["difference"] = reported - computed
	return model.Check{
		Name:    "prompt_tokens_match",
		Status:  model.StatusFail,
		Message: fmt.Sprintf("prompt_tokens mismatch: %s=%d, %s=%d", computedLabel, computed, reportedLabel, reported),
		Details: details,
	}
}

// callGeminiCountTokens posts to the URL-templated countTokens endpoint.
// Auth is via the ?key= URL parameter (per spec §4.3 — the user's choice).
func callGeminiCountTokens(modelName string, contents []geminiContent, vendorKey string) (int, error) {
	ep, ok := vendor.LookupCountTokens(vendor.Gemini)
	if !ok {
		return 0, fmt.Errorf("internal: gemini endpoint missing")
	}

	base := ep.URL
	if geminiEndpointOverride != "" {
		base = geminiEndpointOverride
	}
	endpoint := strings.ReplaceAll(base, "{model}", url.PathEscape(modelName))
	if vendorKey != "" {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		endpoint = endpoint + sep + "key=" + url.QueryEscape(vendorKey)
	}

	body, err := json.Marshal(map[string]any{"contents": contents})
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(ep.Method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	for k, v := range ep.Header {
		httpReq.Header.Set(k, v)
	}

	resp, err := httpDoer().Do(httpReq)
	if err != nil {
		return 0, classifyNetError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, readError(resp, "gemini")
	}
	var out struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	return out.TotalTokens, nil
}
