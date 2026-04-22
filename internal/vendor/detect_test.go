package vendor

import (
	"net/http"
	"testing"
)

func TestDetect_HeaderAuthoritative(t *testing.T) {
	h := http.Header{}
	h.Set("X-Upstream-Vendor", "openai")
	// Even a misleading body model shouldn't override a valid header.
	got := Detect(h, []byte(`{"model":"claude-opus-4"}`))
	if got != OpenAI {
		t.Errorf("Detect = %q, want openai (header wins)", got)
	}
}

func TestDetect_HeaderUnknownEnum(t *testing.T) {
	h := http.Header{}
	h.Set("X-Upstream-Vendor", "not-a-real-vendor")
	// Unknown enum must not fall through to body heuristic — we refuse to guess.
	got := Detect(h, []byte(`{"model":"gpt-4o"}`))
	if got != Unknown {
		t.Errorf("Detect = %q, want unknown (bad enum should not fall through)", got)
	}
}

func TestDetect_BodyFallback(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"model":"gpt-4o-mini"}`, OpenAI},
		{`{"model":"o1-preview"}`, OpenAI},
		{`{"model":"claude-sonnet-4"}`, Anthropic},
		{`{"model":"glm-4-flash"}`, Zhipu},
		{`{"model":"gemini-1.5-pro"}`, Gemini},
		{`{"model":"deepseek-chat"}`, DeepSeek},
		{`{"model":"kimi-k2"}`, Moonshot},
		{`{"model":"moonshot-v1"}`, Moonshot},
		{`{"model":"llama-70b"}`, Unknown},        // unknown family
		{`{"id":"abc"}`, Unknown},                 // no model field
		{`not json at all`, Unknown},              // malformed
		{`   {"model":"GPT-4o-mini"}   `, OpenAI}, // whitespace + caps
	}
	for _, c := range cases {
		got := Detect(http.Header{}, []byte(c.body))
		if got != c.want {
			t.Errorf("Detect(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestDetect_AllMissing(t *testing.T) {
	if got := Detect(http.Header{}, nil); got != Unknown {
		t.Errorf("Detect with nothing = %q, want unknown", got)
	}
}

func TestDashboardURL(t *testing.T) {
	cases := []struct {
		vendor, trace string
		want          string
	}{
		{OpenAI, "req_123", "https://platform.openai.com/logs?request_id=req_123"},
		{Anthropic, "msg_01", "https://console.anthropic.com/logs?request_id=msg_01"},
		{OpenAI, "", ""},          // no trace → no URL
		{Unknown, "anything", ""}, // unknown vendor → no URL
		{"made-up", "x", ""},
	}
	for _, c := range cases {
		if got := DashboardURL(c.vendor, c.trace); got != c.want {
			t.Errorf("DashboardURL(%q, %q) = %q, want %q", c.vendor, c.trace, got, c.want)
		}
	}
}
