package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/primerouter/pr-audit/internal/model"
	"github.com/primerouter/pr-audit/internal/vendor"
)

// anthropicEndpointOverride is an optional test seam: when set, replay
// hits this URL instead of api.anthropic.com. Production code never
// touches it; the integration test in anthropic_test.go uses it to point
// at an httptest.Server.
var anthropicEndpointOverride string

// replayAnthropic runs the Anthropic L3 path: call /v1/messages/count_tokens
// with the user's own key, then reconcile against the three-field cache
// summation. The cache rule (spec §4.2 / trust-model §4.4) is the single
// most bug-prone spot in this whole project — getting it wrong yields
// false positives every time prompt caching is enabled.
func replayAnthropic(req model.ReplayRequest, reported *model.Usage, responseModel, vendorKey string) ([]model.Check, model.L3Strategy) {
	checks := []model.Check{{
		Name:    "l3_strategy",
		Status:  model.StatusPass,
		Message: "count_tokens API (Anthropic)",
	}}

	result, err := callAnthropicCountTokens(req, vendorKey)
	if err != nil {
		// Network / API failures are NOT reported as L3 fail — we degrade
		// to structural so the user can still trust the model_match check.
		// Network errors with a netError code propagate via the returned
		// error so the CLI can map them to exit 31/32/33; we still emit a
		// Check here so the L3 section renders coherently.
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusWarn,
			Message: fmt.Sprintf("count_tokens API unavailable: %v", err),
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

	// CRITICAL: cache summation. count_tokens returns the "no-cache"
	// total. PrimeRouter's body splits this into three fields when
	// prompt caching is enabled. Comparing against InputTokens alone
	// misreports every cache hit as inflation.
	expected := reported.InputTokens + reported.CacheCreationInputTokens + reported.CacheReadInputTokens
	details := map[string]any{
		"count_tokens_result":         result,
		"reported_input_tokens":       reported.InputTokens,
		"cache_creation_input_tokens": reported.CacheCreationInputTokens,
		"cache_read_input_tokens":     reported.CacheReadInputTokens,
		"expected_sum":                expected,
	}
	if result == expected {
		checks = append(checks, model.Check{
			Name:    "prompt_tokens_match",
			Status:  model.StatusPass,
			Details: details,
		})
	} else {
		details["difference"] = expected - result
		checks = append(checks, model.Check{
			Name:   "prompt_tokens_match",
			Status: model.StatusFail,
			Message: fmt.Sprintf(
				"prompt_tokens mismatch: count_tokens=%d, sum(input+cache_creation+cache_read)=%d (diff %+d)",
				result, expected, expected-result,
			),
			Details: details,
		})
	}

	checks = append(checks, checkModelMatch(req.Model, responseModel))
	return checks, model.L3CountTokensAPI
}

// callAnthropicCountTokens issues the POST and returns input_tokens.
// The request body re-uses messages verbatim — same shape as /v1/messages.
func callAnthropicCountTokens(req model.ReplayRequest, vendorKey string) (int, error) {
	ep, ok := vendor.LookupCountTokens(vendor.Anthropic)
	if !ok {
		return 0, fmt.Errorf("internal: anthropic endpoint missing")
	}
	url := ep.URL
	if anthropicEndpointOverride != "" {
		url = anthropicEndpointOverride
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(ep.Method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	for k, v := range ep.Header {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("x-api-key", vendorKey)

	resp, err := httpDoer().Do(httpReq)
	if err != nil {
		return 0, classifyNetError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, readError(resp, "anthropic")
	}

	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	return out.InputTokens, nil
}
