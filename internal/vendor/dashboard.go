package vendor

import "net/url"

// DashboardURL returns a URL the user can open to look up a trace-id in
// the vendor's own dashboard (L2 verification). Empty string means "no
// direct deep-link known" — the CLI still prints the trace-id itself.
//
// These URLs are best-effort pointers. Vendors change their UIs; we
// intentionally don't encode a promise that the link always resolves.
func DashboardURL(vendor, traceID string) string {
	if traceID == "" {
		return ""
	}
	q := url.QueryEscape(traceID)
	switch vendor {
	case OpenAI:
		return "https://platform.openai.com/logs?request_id=" + q
	case AzureOpenAI:
		return "https://portal.azure.com/" // no deep-link; user navigates to their resource
	case Anthropic:
		return "https://console.anthropic.com/logs?request_id=" + q
	case Zhipu:
		return "https://bigmodel.cn/usercenter/apikeys" // no deep-link; usage page
	case Gemini:
		return "https://aistudio.google.com/"
	case DeepSeek:
		return "https://platform.deepseek.com/usage"
	case Moonshot:
		return "https://platform.moonshot.cn/console/info"
	}
	return ""
}
