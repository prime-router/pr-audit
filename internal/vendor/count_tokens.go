package vendor

// count_tokens.go: vendor capability lookup for L3 reconciliation.
//
// Two distinct capabilities matter to the replay command:
//
//   - HasOfflineTokenizer(v): can we recompute prompt_tokens locally
//     (no network) using something like tiktoken? OpenAI is the only
//     vendor with a usable Go-side tokenizer today.
//
//   - HasCountTokens(v): does the vendor expose a `count_tokens`-style
//     HTTP endpoint we can call directly with the user's own key,
//     bypassing PrimeRouter? Anthropic and Gemini do; Zhipu/DeepSeek/
//     Moonshot do not (as of the cutover this file was written for).
//
// Both functions are intentionally simple booleans — strategy routing
// in internal/replay decides what to do with the answer.

// CountTokensEndpoint describes how to reach a vendor's count_tokens API.
// URL may contain `{model}` as a placeholder; callers substitute it.
type CountTokensEndpoint struct {
	URL    string
	Method string
	Header map[string]string
}

var countTokensEndpoints = map[string]CountTokensEndpoint{
	Anthropic: {
		URL:    "https://api.anthropic.com/v1/messages/count_tokens",
		Method: "POST",
		Header: map[string]string{
			// 2023-06-01 is Anthropic's only stable API version as of writing;
			// they have not published a newer Stable revision since launch.
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
		},
	},
	Gemini: {
		// Gemini auth is via URL ?key= per spec §4.3 / open item #5.
		URL:    "https://generativelanguage.googleapis.com/v1beta/models/{model}:countTokens",
		Method: "POST",
		Header: map[string]string{
			"content-type": "application/json",
		},
	},
}

// HasCountTokens reports whether vendor v exposes a count_tokens HTTP API
// that pr-audit knows how to call.
func HasCountTokens(v string) bool {
	_, ok := countTokensEndpoints[v]
	return ok
}

// HasOfflineTokenizer reports whether vendor v can be reconciled fully
// offline (no network call). OpenAI uses tiktoken; Azure OpenAI shares
// tiktoken encodings, so we treat them the same.
func HasOfflineTokenizer(v string) bool {
	return v == OpenAI || v == AzureOpenAI
}

// LookupCountTokens returns the endpoint descriptor and ok=true when the
// vendor has one. Callers must still substitute {model} where applicable.
func LookupCountTokens(v string) (CountTokensEndpoint, bool) {
	ep, ok := countTokensEndpoints[v]
	return ep, ok
}
