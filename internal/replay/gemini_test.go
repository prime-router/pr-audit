package replay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/primerouter/pr-audit/internal/model"
)

func withGeminiMock(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := geminiEndpointOverride
	geminiEndpointOverride = srv.URL // no {model} placeholder needed; we substitute below
	return srv, func() {
		geminiEndpointOverride = prev
		srv.Close()
	}
}

func TestConvertOpenAIToGemini_RolesAndContent(t *testing.T) {
	in := []any{
		map[string]any{"role": "system", "content": "you are helpful"},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "hello"},
	}
	mb, _ := json.Marshal(in)

	out, err := convertOpenAIToGemini(mb)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d contents, want 3", len(out))
	}
	if out[0].Role != "user" || !strings.HasPrefix(out[0].Parts[0].Text, "[System]") {
		t.Errorf("system → user with prefix; got %+v", out[0])
	}
	if out[1].Role != "user" || out[1].Parts[0].Text != "hi" {
		t.Errorf("user mapping wrong: %+v", out[1])
	}
	if out[2].Role != "model" || out[2].Parts[0].Text != "hello" {
		t.Errorf("assistant → model wrong: %+v", out[2])
	}
}

func TestConvertOpenAIToGemini_EmptyMessages(t *testing.T) {
	out, err := convertOpenAIToGemini(nil)
	if err != nil {
		t.Fatalf("convert(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("nil input should yield empty contents; got %v", out)
	}
}

func TestConvertOpenAIToGemini_MultiPartText(t *testing.T) {
	in := []any{map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "hello "},
			map[string]any{"type": "text", "text": "world"},
		},
	}}
	mb, _ := json.Marshal(in)
	out, err := convertOpenAIToGemini(mb)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 1 || out[0].Parts[0].Text != "hello world" {
		t.Errorf("multi-part text not concatenated: %+v", out)
	}
}

func TestReplayGemini_HappyPath(t *testing.T) {
	srv, cleanup := withGeminiMock(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify auth via URL parameter
		if r.URL.Query().Get("key") != "AIza-test" {
			t.Errorf("expected key=AIza-test in URL, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"totalTokens": 5}`))
	})
	defer cleanup()
	_ = srv

	req := reqJSON(t, "gemini-1.5-pro",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	usage := &model.Usage{PromptTokens: 5}
	checks, strategy := replayGemini(req, usage, "gemini-1.5-pro", "AIza-test")

	if strategy != model.L3CountTokensAPI {
		t.Errorf("strategy = %v, want L3CountTokensAPI", strategy)
	}
	for _, ck := range checks {
		if ck.Status == model.StatusFail {
			t.Errorf("unexpected fail: %+v", ck)
		}
	}
}

func TestReplayGemini_MismatchFails(t *testing.T) {
	_, cleanup := withGeminiMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"totalTokens": 5}`))
	})
	defer cleanup()

	req := reqJSON(t, "gemini-1.5-pro",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	usage := &model.Usage{PromptTokens: 50}
	checks, _ := replayGemini(req, usage, "gemini-1.5-pro", "AIza-test")

	for _, ck := range checks {
		if ck.Name == "prompt_tokens_match" && ck.Status != model.StatusFail {
			t.Errorf("expected fail; got %+v", ck)
		}
	}
}

func TestReplayGemini_AcceptsInputTokensFallback(t *testing.T) {
	// Some routers expose Gemini's native input_tokens instead of
	// prompt_tokens. We accept whichever is non-zero.
	_, cleanup := withGeminiMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"totalTokens": 5}`))
	})
	defer cleanup()

	req := reqJSON(t, "gemini-1.5-pro",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	usage := &model.Usage{InputTokens: 5} // NOT PromptTokens
	checks, _ := replayGemini(req, usage, "gemini-1.5-pro", "AIza-test")

	for _, ck := range checks {
		if ck.Status == model.StatusFail {
			t.Errorf("input_tokens fallback failed: %+v", ck)
		}
	}
}

func TestReplayGemini_5xxDegrades(t *testing.T) {
	_, cleanup := withGeminiMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	defer cleanup()

	req := reqJSON(t, "gemini-1.5-pro",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	checks, strategy := replayGemini(req, &model.Usage{PromptTokens: 5}, "gemini-1.5-pro", "AIza-test")

	if strategy != model.L3Structural {
		t.Errorf("strategy = %v, want L3Structural", strategy)
	}
	hasWarn := false
	for _, ck := range checks {
		if ck.Status == model.StatusWarn {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Error("expected warn check on 5xx")
	}
}
