package verify

import "testing"

func TestParseUsage_OpenAI(t *testing.T) {
	u := ParseUsage([]byte(`{"usage":{"prompt_tokens":42,"completion_tokens":128,"total_tokens":170}}`))
	if !u.Present {
		t.Fatal("expected Present")
	}
	if u.PromptTokens != 42 || u.CompletionTokens != 128 || u.TotalTokens != 170 {
		t.Errorf("bad OpenAI usage: %+v", u)
	}
}

func TestParseUsage_AnthropicWithCache(t *testing.T) {
	u := ParseUsage([]byte(`{"usage":{"input_tokens":42,"output_tokens":128,"cache_creation_input_tokens":0,"cache_read_input_tokens":1024}}`))
	if u.InputTokens != 42 || u.OutputTokens != 128 || u.CacheReadInputTokens != 1024 {
		t.Errorf("bad Anthropic usage: %+v", u)
	}
}

func TestParseUsage_Absent(t *testing.T) {
	u := ParseUsage([]byte(`{"id":"x","model":"gpt-4o"}`))
	if u.Present {
		t.Errorf("expected Present=false for body without usage")
	}
}

func TestParseUsage_Malformed(t *testing.T) {
	for _, b := range [][]byte{
		nil,
		[]byte(""),
		[]byte("not json"),
		[]byte("["),
	} {
		if ParseUsage(b).Present {
			t.Errorf("ParseUsage(%q) unexpectedly Present", b)
		}
	}
}

func TestParseUsage_WrongType(t *testing.T) {
	// `usage` is present but a string — we record presence but no fields.
	u := ParseUsage([]byte(`{"usage":"not-an-object"}`))
	if !u.Present {
		t.Errorf("usage field present but not recognised as such")
	}
	if u.PromptTokens != 0 {
		t.Errorf("no numeric fields should have leaked")
	}
}

func TestExtractModel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"model":"gpt-4o-mini"}`, "gpt-4o-mini"},
		{`{"id":"x"}`, ""},
		{`not json`, ""},
		{``, ""},
		{`{"model":42}`, ""}, // non-string → unmarshal fails
	}
	for _, c := range cases {
		if got := ExtractModel([]byte(c.in)); got != c.want {
			t.Errorf("ExtractModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
