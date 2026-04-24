package vendor

import "testing"

func TestHasCountTokens(t *testing.T) {
	cases := []struct {
		vendor string
		want   bool
	}{
		{Anthropic, true},
		{Gemini, true},
		{OpenAI, false},
		{AzureOpenAI, false},
		{Zhipu, false},
		{DeepSeek, false},
		{Moonshot, false},
		{Unknown, false},
		{"bogus", false},
	}
	for _, c := range cases {
		if got := HasCountTokens(c.vendor); got != c.want {
			t.Errorf("HasCountTokens(%q) = %v, want %v", c.vendor, got, c.want)
		}
	}
}

func TestHasOfflineTokenizer(t *testing.T) {
	cases := []struct {
		vendor string
		want   bool
	}{
		{OpenAI, true},
		{AzureOpenAI, true},
		{Anthropic, false},
		{Gemini, false},
		{Zhipu, false},
		{Unknown, false},
	}
	for _, c := range cases {
		if got := HasOfflineTokenizer(c.vendor); got != c.want {
			t.Errorf("HasOfflineTokenizer(%q) = %v, want %v", c.vendor, got, c.want)
		}
	}
}

func TestLookupCountTokens(t *testing.T) {
	if ep, ok := LookupCountTokens(Anthropic); !ok {
		t.Fatal("Anthropic should have endpoint")
	} else {
		if ep.URL != "https://api.anthropic.com/v1/messages/count_tokens" {
			t.Errorf("Anthropic URL = %q", ep.URL)
		}
		if ep.Header["anthropic-version"] != "2023-06-01" {
			t.Errorf("Anthropic version header = %q, want 2023-06-01", ep.Header["anthropic-version"])
		}
	}

	if ep, ok := LookupCountTokens(Gemini); !ok {
		t.Fatal("Gemini should have endpoint")
	} else {
		if ep.URL == "" || ep.URL[:5] != "https" {
			t.Errorf("Gemini URL not https: %q", ep.URL)
		}
	}

	if _, ok := LookupCountTokens(OpenAI); ok {
		t.Error("OpenAI should not have count_tokens endpoint (uses offline tokenizer)")
	}
	if _, ok := LookupCountTokens(Zhipu); ok {
		t.Error("Zhipu should not have count_tokens endpoint")
	}
}
