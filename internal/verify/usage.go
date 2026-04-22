package verify

import (
	"bytes"
	"encoding/json"

	"github.com/primerouter/pr-audit/internal/model"
)

// ParseUsage pulls the `usage` object out of a JSON body. It tolerates
// missing/malformed bodies (returns Usage{Present:false}) and recognises
// both OpenAI (prompt/completion/total) and Anthropic (input/output +
// cache) field names. We never attempt reconciliation here — that's an
// L3 concern (see specs §3.3, trust-model §4.4).
func ParseUsage(body []byte) model.Usage {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return model.Usage{}
	}
	var wrapper struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil || len(wrapper.Usage) == 0 {
		return model.Usage{}
	}
	var raw struct {
		PromptTokens             *int `json:"prompt_tokens"`
		CompletionTokens         *int `json:"completion_tokens"`
		TotalTokens              *int `json:"total_tokens"`
		InputTokens              *int `json:"input_tokens"`
		OutputTokens             *int `json:"output_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	}
	if err := json.Unmarshal(wrapper.Usage, &raw); err != nil {
		// usage was present but non-object; still mark as present so the
		// caller can report "usage present, unparseable" rather than absent.
		return model.Usage{Present: true}
	}
	u := model.Usage{Present: true}
	if raw.PromptTokens != nil {
		u.PromptTokens = *raw.PromptTokens
	}
	if raw.CompletionTokens != nil {
		u.CompletionTokens = *raw.CompletionTokens
	}
	if raw.TotalTokens != nil {
		u.TotalTokens = *raw.TotalTokens
	}
	if raw.InputTokens != nil {
		u.InputTokens = *raw.InputTokens
	}
	if raw.OutputTokens != nil {
		u.OutputTokens = *raw.OutputTokens
	}
	if raw.CacheCreationInputTokens != nil {
		u.CacheCreationInputTokens = *raw.CacheCreationInputTokens
	}
	if raw.CacheReadInputTokens != nil {
		u.CacheReadInputTokens = *raw.CacheReadInputTokens
	}
	return u
}

// ExtractModel returns the top-level `model` string from a JSON body.
// Empty string if the field is missing or the body isn't JSON.
func ExtractModel(body []byte) string {
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
