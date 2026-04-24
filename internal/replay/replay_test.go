package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primerouter/pr-audit/internal/model"
)

// writeFixture builds a headers + body pair on disk and returns paths.
// The headers file declares the SHA256 of the body so L1 passes.
func writeFixture(t *testing.T, vendor, modelName string, usage map[string]any) (headers, body, request string) {
	t.Helper()
	dir := t.TempDir()

	bodyObj := map[string]any{
		"id":    "trace-test-123",
		"model": modelName,
		"usage": usage,
	}
	bodyBytes, _ := json.Marshal(bodyObj)
	bp := filepath.Join(dir, "body.json")
	if err := os.WriteFile(bp, bodyBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	hp := filepath.Join(dir, "headers.txt")
	headerStr := "HTTP/2 200\r\n" +
		"content-type: application/json\r\n" +
		"x-upstream-vendor: " + vendor + "\r\n" +
		"x-upstream-trace-id: trace-test-123\r\n" +
		"x-upstream-sha256: sha256:" + sha256Hex(bodyBytes) + "\r\n\r\n"
	if err := os.WriteFile(hp, []byte(headerStr), 0o644); err != nil {
		t.Fatal(err)
	}

	reqObj := map[string]any{
		"model": modelName,
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	rb, _ := json.Marshal(reqObj)
	rp := filepath.Join(dir, "request.json")
	if err := os.WriteFile(rp, rb, 0o644); err != nil {
		t.Fatal(err)
	}
	return hp, bp, rp
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestRun_OpenAI_L3Pass(t *testing.T) {
	headers, body, request := writeFixture(t, "openai", "gpt-4o-mini",
		map[string]any{"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
	if res.Outcome != model.OutcomeNoEvidenceOfTampering {
		t.Errorf("outcome = %v, want no_evidence_of_tampering", res.Outcome)
	}
	if res.L3Strategy != model.L3TiktokenOffline {
		t.Errorf("strategy = %v, want tiktoken_offline", res.L3Strategy)
	}
}

func TestRun_OpenAI_L3FailOnInflation(t *testing.T) {
	headers, body, request := writeFixture(t, "openai", "gpt-4o-mini",
		map[string]any{"prompt_tokens": 80}) // 10x inflation

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.ExitCode != 40 {
		t.Errorf("exit = %d, want 40", res.ExitCode)
	}
	if res.Outcome != model.OutcomeL3Fail {
		t.Errorf("outcome = %v, want l3_fail", res.Outcome)
	}
}

func TestRun_L1FailShortCircuitsL3(t *testing.T) {
	headers, body, request := writeFixture(t, "openai", "gpt-4o-mini",
		map[string]any{"prompt_tokens": 8})
	// Tamper with body so L1 hash fails.
	if err := os.WriteFile(body, []byte(`{"hacked":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.ExitCode != 10 {
		t.Errorf("exit = %d, want 10", res.ExitCode)
	}
	if res.Outcome != model.OutcomeL1Fail {
		t.Errorf("outcome = %v, want l1_fail", res.Outcome)
	}
	if len(res.L3Checks) != 0 {
		t.Errorf("L3 should not run on L1 fail; got %d checks", len(res.L3Checks))
	}
}

func TestRun_UnknownVendorSkipsL3(t *testing.T) {
	headers, body, request := writeFixture(t, "unknown", "mystery-model",
		map[string]any{"prompt_tokens": 10})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
	if res.L3Strategy != model.L3Skipped {
		t.Errorf("strategy = %v, want skipped", res.L3Strategy)
	}
	if res.Outcome != model.OutcomeL3Skipped {
		t.Errorf("outcome = %v, want l3_skipped", res.Outcome)
	}
}

func TestRun_ZhipuSkipsL3(t *testing.T) {
	headers, body, request := writeFixture(t, "zhipu", "glm-4-flash",
		map[string]any{"prompt_tokens": 3})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.L3Strategy != model.L3Skipped {
		t.Errorf("strategy = %v, want skipped (zhipu has no count_tokens)", res.L3Strategy)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0 (skipped is not a failure)", res.ExitCode)
	}
}

func TestRun_AnthropicWithoutKeySkips(t *testing.T) {
	// Force PR_AUDIT_VENDOR_KEY unset.
	t.Setenv("PR_AUDIT_VENDOR_KEY", "")

	headers, body, request := writeFixture(t, "anthropic", "claude-3-5-sonnet",
		map[string]any{"input_tokens": 8})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.L3Strategy != model.L3Skipped {
		t.Errorf("strategy = %v, want skipped", res.L3Strategy)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
}

func TestRun_AnthropicWithEnvKeyUsesAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"input_tokens": 8}`))
	}))
	defer srv.Close()
	prev := anthropicEndpointOverride
	anthropicEndpointOverride = srv.URL
	defer func() { anthropicEndpointOverride = prev }()

	t.Setenv("PR_AUDIT_VENDOR_KEY", "sk-from-env")

	headers, body, request := writeFixture(t, "anthropic", "claude-3-5-sonnet",
		map[string]any{"input_tokens": 8})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
	if res.L3Strategy != model.L3CountTokensAPI {
		t.Errorf("strategy = %v, want count_tokens_api (env key should activate)", res.L3Strategy)
	}
}

func TestRun_MissingRequestFile(t *testing.T) {
	headers, body, _ := writeFixture(t, "openai", "gpt-4o-mini",
		map[string]any{"prompt_tokens": 8})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: "/no/such/file.json",
	})
	if res.ExitCode != 20 {
		t.Errorf("exit = %d, want 20", res.ExitCode)
	}
	if res.Outcome != model.OutcomeParseError {
		t.Errorf("outcome = %v, want parse_error", res.Outcome)
	}
}

func TestRun_RequestMissingModelField(t *testing.T) {
	headers, body, request := writeFixture(t, "openai", "gpt-4o-mini",
		map[string]any{"prompt_tokens": 8})
	if err := os.WriteFile(request, []byte(`{"messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
	})
	if res.ExitCode != 20 {
		t.Errorf("exit = %d, want 20", res.ExitCode)
	}
	if !strings.Contains(strings.ToLower(string(res.Outcome)), "parse") {
		t.Errorf("outcome = %v, want parse-related", res.Outcome)
	}
}

func TestRun_VendorKeyNotPersisted(t *testing.T) {
	headers, body, request := writeFixture(t, "openai", "gpt-4o-mini",
		map[string]any{"prompt_tokens": 8})

	res := Run(model.ReplayParams{
		HeadersPath: headers, BodyPath: body, RequestPath: request,
		VendorKey: "sk-secret-leak-canary",
	})
	// Trust-model invariant: the key MUST NOT survive in res anywhere.
	blob, _ := json.Marshal(res)
	if strings.Contains(string(blob), "sk-secret-leak-canary") {
		t.Fatalf("vendor-key leaked into Result JSON: %s", blob)
	}
}
