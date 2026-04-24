package replay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/primerouter/pr-audit/internal/model"
)

// reqJSON is a tiny fixture builder so each test reads as one expression.
func reqJSON(t *testing.T, modelName string, messages, tools any) model.ReplayRequest {
	t.Helper()
	mb, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	r := model.ReplayRequest{Model: modelName, Messages: mb}
	if tools != nil {
		tb, err := json.Marshal(tools)
		if err != nil {
			t.Fatalf("marshal tools: %v", err)
		}
		r.Tools = tb
	}
	return r
}

func TestCountOpenAITokens_HelloMatchesReference(t *testing.T) {
	// Reference: a single user "hello" message with gpt-4o-mini reports
	// prompt_tokens = 8 (per fixture and OpenAI's own count).
	req := reqJSON(t, "gpt-4o-mini",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	got, err := countOpenAITokens(req)
	if err != nil {
		t.Fatalf("countOpenAITokens: %v", err)
	}
	if got != 8 {
		t.Errorf("got %d tokens for single 'hello' message, want 8", got)
	}
}

func TestCountOpenAITokens_UnknownModelFallsBackToO200k(t *testing.T) {
	req := reqJSON(t, "gpt-9000-not-real",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	got, err := countOpenAITokens(req)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if got <= 0 {
		t.Errorf("expected positive token count from fallback, got %d", got)
	}
}

func TestCountOpenAITokens_MultiPartContent(t *testing.T) {
	req := reqJSON(t, "gpt-4o-mini",
		[]any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "..."}},
			},
		}}, nil)
	got, err := countOpenAITokens(req)
	if err != nil {
		t.Fatalf("countOpenAITokens: %v", err)
	}
	// Should match the plain "hello" count (we only tokenise the text part).
	if got != 8 {
		t.Errorf("multi-part text-only count = %d, want 8", got)
	}
}

func TestReplayOpenAI_ToolsTriggerDegrade(t *testing.T) {
	req := reqJSON(t, "gpt-4o-mini",
		[]any{map[string]any{"role": "user", "content": "weather?"}},
		[]any{map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "get_weather"},
		}})
	usage := &model.Usage{PromptTokens: 50}
	checks, strategy := replayOpenAI(req, usage, "gpt-4o-mini")

	if strategy != model.L3Structural {
		t.Errorf("strategy = %v, want L3Structural", strategy)
	}
	hasSkippedPromptTokens := false
	hasModelMatch := false
	for _, ck := range checks {
		if ck.Name == "prompt_tokens_match" && ck.Status == model.StatusSkip {
			hasSkippedPromptTokens = true
		}
		if ck.Name == "model_match" && ck.Status == model.StatusPass {
			hasModelMatch = true
		}
	}
	if !hasSkippedPromptTokens {
		t.Error("tools path must skip prompt_tokens_match")
	}
	if !hasModelMatch {
		t.Error("tools path must still verify model_match")
	}
}

func TestReplayOpenAI_HappyPath(t *testing.T) {
	req := reqJSON(t, "gpt-4o-mini",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{PromptTokens: 8}
	checks, strategy := replayOpenAI(req, usage, "gpt-4o-mini")

	if strategy != model.L3TiktokenOffline {
		t.Errorf("strategy = %v, want L3TiktokenOffline", strategy)
	}
	for _, ck := range checks {
		if ck.Status == model.StatusFail {
			t.Errorf("unexpected fail check on happy path: %+v", ck)
		}
	}
}

func TestReplayOpenAI_PromptTokensMismatch(t *testing.T) {
	req := reqJSON(t, "gpt-4o-mini",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{PromptTokens: 80} // 10x inflation
	checks, _ := replayOpenAI(req, usage, "gpt-4o-mini")

	failed := false
	for _, ck := range checks {
		if ck.Name == "prompt_tokens_match" && ck.Status == model.StatusFail {
			failed = true
			if !strings.Contains(ck.Message, "8") || !strings.Contains(ck.Message, "80") {
				t.Errorf("fail message should mention both values: %q", ck.Message)
			}
			if ck.Details["difference"] != 72 {
				t.Errorf("difference = %v, want 72", ck.Details["difference"])
			}
		}
	}
	if !failed {
		t.Error("expected prompt_tokens_match to fail on inflation")
	}
}

func TestReplayOpenAI_ModelDowngradeDetected(t *testing.T) {
	req := reqJSON(t, "gpt-4o",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	usage := &model.Usage{PromptTokens: 7} // tokens for "hi"
	checks, _ := replayOpenAI(req, usage, "gpt-3.5-turbo")

	for _, ck := range checks {
		if ck.Name == "model_match" {
			if ck.Status != model.StatusFail {
				t.Errorf("model_match status = %v, want fail", ck.Status)
			}
			return
		}
	}
	t.Error("model_match check missing")
}

func TestReplayOpenAI_VersionedVariantIsPass(t *testing.T) {
	// Common case: request asks for "gpt-4o-mini", response has the
	// dated variant "gpt-4o-mini-2024-07-18" — same model, different
	// pin. Should not flag as downgrade.
	req := reqJSON(t, "gpt-4o-mini",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	usage := &model.Usage{PromptTokens: 7}
	checks, _ := replayOpenAI(req, usage, "gpt-4o-mini-2024-07-18")
	for _, ck := range checks {
		if ck.Name == "model_match" && ck.Status != model.StatusPass {
			t.Errorf("versioned variant should be pass, got %v", ck.Status)
		}
	}
}

func TestHasTools(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"empty", ``, false},
		{"null", `null`, false},
		{"empty array", `[]`, false},
		{"empty object", `{}`, false},
		{"one tool", `[{"type":"function","function":{"name":"x"}}]`, true},
		{"two tools", `[{"a":1},{"b":2}]`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hasTools(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("hasTools(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}
