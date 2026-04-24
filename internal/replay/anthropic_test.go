package replay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/primerouter/pr-audit/internal/model"
)

// withAnthropicMock replaces the anthropic endpoint URL for the duration
// of the test and restores it after.
func withAnthropicMock(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := anthropicEndpointOverride
	anthropicEndpointOverride = srv.URL
	return srv, func() {
		anthropicEndpointOverride = prev
		srv.Close()
	}
}

func TestReplayAnthropic_NoCacheHappyPath(t *testing.T) {
	srv, cleanup := withAnthropicMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("missing x-api-key, got headers %v", r.Header)
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
		}
		_, _ = w.Write([]byte(`{"input_tokens": 42}`))
	})
	defer cleanup()
	_ = srv

	req := reqJSON(t, "claude-sonnet-4-20250514",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{InputTokens: 42}
	checks, strategy := replayAnthropic(req, usage, "claude-sonnet-4-20250514", "sk-test")

	if strategy != model.L3CountTokensAPI {
		t.Errorf("strategy = %v, want L3CountTokensAPI", strategy)
	}
	for _, ck := range checks {
		if ck.Status == model.StatusFail {
			t.Errorf("unexpected fail: %+v", ck)
		}
	}
}

// The single most important Anthropic test: cache hits MUST be summed
// across input + cache_creation + cache_read, otherwise every cached
// request reports a false positive.
func TestReplayAnthropic_CacheSummationPasses(t *testing.T) {
	_, cleanup := withAnthropicMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"input_tokens": 1066}`))
	})
	defer cleanup()

	req := reqJSON(t, "claude-sonnet-4-20250514",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{
		InputTokens:              42,
		CacheCreationInputTokens: 0,
		CacheReadInputTokens:     1024,
	}
	checks, _ := replayAnthropic(req, usage, "claude-sonnet-4-20250514", "sk-test")

	for _, ck := range checks {
		if ck.Status == model.StatusFail {
			t.Fatalf("cache-hit case must pass after summation; got fail: %+v", ck)
		}
	}
}

// Inverse: real over-reporting must still be caught after summation.
func TestReplayAnthropic_RealInflationFails(t *testing.T) {
	_, cleanup := withAnthropicMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"input_tokens": 100}`))
	})
	defer cleanup()

	req := reqJSON(t, "claude-sonnet-4-20250514",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{InputTokens: 1000} // PrimeRouter inflated 10x
	checks, _ := replayAnthropic(req, usage, "claude-sonnet-4-20250514", "sk-test")

	for _, ck := range checks {
		if ck.Name == "prompt_tokens_match" {
			if ck.Status != model.StatusFail {
				t.Errorf("expected fail on real inflation, got %v", ck.Status)
			}
			if !strings.Contains(ck.Message, "100") || !strings.Contains(ck.Message, "1000") {
				t.Errorf("fail message should mention both values: %q", ck.Message)
			}
			return
		}
	}
	t.Error("prompt_tokens_match check missing")
}

func TestReplayAnthropic_AuthError(t *testing.T) {
	_, cleanup := withAnthropicMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	})
	defer cleanup()

	req := reqJSON(t, "claude-sonnet-4-20250514",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{InputTokens: 42}
	checks, strategy := replayAnthropic(req, usage, "claude-sonnet-4-20250514", "bad-key")

	if strategy != model.L3Structural {
		t.Errorf("auth failure should degrade to structural; got %v", strategy)
	}
	gotWarn := false
	for _, ck := range checks {
		if ck.Status == model.StatusWarn && strings.Contains(ck.Message, "authentication") {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Error("expected auth-failure warn check")
	}
}

func TestReplayAnthropic_5xxDegrades(t *testing.T) {
	_, cleanup := withAnthropicMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`upstream broke`))
	})
	defer cleanup()

	req := reqJSON(t, "claude-sonnet-4-20250514",
		[]any{map[string]any{"role": "user", "content": "hello"}}, nil)
	usage := &model.Usage{InputTokens: 42}
	checks, strategy := replayAnthropic(req, usage, "claude-sonnet-4-20250514", "sk-test")

	if strategy != model.L3Structural {
		t.Errorf("strategy = %v, want L3Structural on 5xx", strategy)
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

func TestCallAnthropicCountTokens_RequestShape(t *testing.T) {
	srv, cleanup := withAnthropicMock(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		if body.Model != "claude-3-5-sonnet" {
			t.Errorf("model = %q", body.Model)
		}
		if len(body.Messages) == 0 {
			t.Error("messages missing from request body")
		}
		_, _ = w.Write([]byte(`{"input_tokens": 7}`))
	})
	defer cleanup()
	_ = srv

	req := reqJSON(t, "claude-3-5-sonnet",
		[]any{map[string]any{"role": "user", "content": "hi"}}, nil)
	got, err := callAnthropicCountTokens(req, "sk-test")
	if err != nil {
		t.Fatalf("callAnthropicCountTokens: %v", err)
	}
	if got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

// Sanity: the http client we use respects timeouts so a hung server
// doesn't wedge the CLI forever. We don't actually need 30s here.
func TestHttpDoer_HasTimeout(t *testing.T) {
	c := httpDoer()
	hc, ok := c.(*http.Client)
	if !ok {
		t.Fatalf("default doer is not *http.Client")
	}
	if hc.Timeout < time.Second {
		t.Errorf("client timeout = %v, want >= 1s", hc.Timeout)
	}
}
