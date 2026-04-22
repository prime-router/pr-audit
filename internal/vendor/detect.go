// Package vendor detects the upstream LLM vendor and knows where its
// dashboards live. Detection runs in a specific order by design:
//  1. x-upstream-vendor header (authoritative when PrimeRouter emits it)
//  2. body `model` field heuristics (fallback for L1-unavailable path)
package vendor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Known vendor enum — must match docs/primerouter-integration.md §4.3.
const (
	OpenAI      = "openai"
	AzureOpenAI = "azure-openai"
	Anthropic   = "anthropic"
	Zhipu       = "zhipu"
	Gemini      = "gemini"
	DeepSeek    = "deepseek"
	Moonshot    = "moonshot"
	Unknown     = "unknown"
)

var allowed = map[string]bool{
	OpenAI: true, AzureOpenAI: true, Anthropic: true, Zhipu: true,
	Gemini: true, DeepSeek: true, Moonshot: true, Unknown: true,
}

// Detect returns the best-effort vendor identifier.
// Preference: x-upstream-vendor header → body `model` heuristic → "unknown".
// body may be nil if we don't have inline bytes yet (split mode); in that
// case only the header path is consulted.
func Detect(h http.Header, body []byte) string {
	if v := strings.ToLower(strings.TrimSpace(h.Get("x-upstream-vendor"))); v != "" {
		if allowed[v] {
			return v
		}
		// Unknown value → treat as unknown rather than trusting a typo.
		return Unknown
	}
	if len(body) > 0 {
		if m := vendorFromModel(extractModelField(body)); m != "" {
			return m
		}
	}
	return Unknown
}

// extractModelField pulls out the `model` string from a JSON body without
// fully unmarshalling (which would fail on streaming or malformed bodies).
// Returns "" on any parse error.
func extractModelField(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return ""
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Model
}

// vendorFromModel applies conservative prefix/substring heuristics to a
// model identifier. Unknown models return "".
func vendorFromModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(m, "gpt-"),
		strings.HasPrefix(m, "o1-"),
		strings.HasPrefix(m, "o3-"),
		strings.HasPrefix(m, "chatgpt-"),
		strings.HasPrefix(m, "text-embedding-"),
		strings.HasPrefix(m, "dall-e"):
		return OpenAI
	case strings.HasPrefix(m, "claude-"):
		return Anthropic
	case strings.HasPrefix(m, "glm-"):
		return Zhipu
	case strings.HasPrefix(m, "gemini-"):
		return Gemini
	case strings.HasPrefix(m, "deepseek-"):
		return DeepSeek
	case strings.HasPrefix(m, "moonshot-"), strings.HasPrefix(m, "kimi-"):
		return Moonshot
	}
	return ""
}
