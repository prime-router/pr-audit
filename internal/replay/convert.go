package replay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// geminiContent / geminiPart mirror the shape Gemini's REST API expects
// at /v1beta/models/{model}:countTokens. We only emit text parts; image
// / inlineData / fileData are out of scope for the MVP (spec §4.3).
type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

// convertOpenAIToGemini adapts OpenAI-style messages to Gemini contents.
// Mapping (spec §4.3):
//
//	role:"system"     → role:"user", text prefixed with "[System] "
//	role:"user"       → role:"user"
//	role:"assistant"  → role:"model"
//	content: string   → parts:[{text:string}]
//	content: [parts]  → parts:[{text:joined}]  (text-typed parts only)
//
// We deliberately fold "system" into the user role with a marker prefix
// rather than using systemInstruction — countTokens accepts both, and
// the prefix preserves the system token count (which is what we want
// for prompt_tokens reconciliation; otherwise Gemini would silently
// exclude system tokens from totalTokens).
func convertOpenAIToGemini(messagesJSON json.RawMessage) ([]geminiContent, error) {
	if len(messagesJSON) == 0 {
		return nil, nil
	}
	var raw []openAIMessage
	if err := json.Unmarshal(messagesJSON, &raw); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}

	contents := make([]geminiContent, 0, len(raw))
	for _, msg := range raw {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		text := messageText(msg.Content)
		switch role {
		case "system":
			role = "user"
			if text != "" {
				text = "[System] " + text
			}
		case "assistant":
			role = "model"
		case "":
			role = "user"
		case "user", "model":
			// already correct
		default:
			// Tool / function / unknown roles: pass through as user with
			// a marker so the token count still reflects the content.
			text = "[" + msg.Role + "] " + text
			role = "user"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: text}},
		})
	}
	return contents, nil
}
