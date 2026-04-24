package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/primerouter/pr-audit/internal/model"
)

func sampleResult(outcome model.Outcome, exit int) model.Result {
	return model.Result{
		Version:           "0.1.0",
		Command:           "verify",
		Timestamp:         "2026-04-22T10:00:00Z",
		Input:             model.Input{Source: "split:h.txt+b.bin", SizeBytes: 243},
		TrustLevelReached: model.TrustL1SelfConsistent,
		Vendor:            "openai",
		Model:             "gpt-4o-mini",
		TraceID:           "req_1",
		Outcome:           outcome,
		ExitCode:          exit,
		Checks: []model.Check{
			{Name: "header_presence", Status: model.StatusPass, Details: map[string]any{
				"x-upstream-sha256": "sha256:abc",
			}},
			{Name: "sha256_match", Status: model.StatusPass, Details: map[string]any{
				"declared": "sha256:abc", "computed": "sha256:abc",
			}},
			{Name: "usage_parsed", Status: model.StatusPass, Details: map[string]any{
				"prompt_tokens": 3,
			}},
		},
		NextSteps: []model.NextStep{
			{Level: "L2", Action: "verify_trace_id_on_vendor_dashboard", URL: "https://x"},
			{Level: "L3", Action: "run_replay_with_own_vendor_key", Command: "pr-audit replay ..."},
		},
	}
}

func TestRenderHuman_PassVerdict(t *testing.T) {
	var buf bytes.Buffer
	RenderHuman(&buf, sampleResult(model.OutcomeSelfConsistent, 0))
	s := buf.String()

	mustContain := []string{
		"pr-audit v0.1.0",
		"SELF-CONSISTENT",
		"Body SHA256 matches declared value",
		"Usage field parsed from body",
		"[L2 · External attestation]",
		"[L3 · End-to-end replay",
		"Exit code: 0",
	}
	for _, want := range mustContain {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output:\n%s", want, s)
		}
	}
	// Forbidden phrasing per trust-model §3.4
	for _, forbidden := range []string{"VERIFIED", "HONEST"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("forbidden phrase %q present in human output", forbidden)
		}
	}
}

func TestRenderHuman_FailVerdict(t *testing.T) {
	r := sampleResult(model.OutcomeL1Fail, 10)
	r.TrustLevelReached = model.TrustL1Fail
	r.Checks[1] = model.Check{
		Name: "sha256_match", Status: model.StatusFail,
		Message: "local SHA256 does not match declared value",
		Details: map[string]any{"declared": "sha256:a", "computed": "sha256:b"},
	}
	r.NextSteps = nil

	var buf bytes.Buffer
	RenderHuman(&buf, r)
	s := buf.String()
	if !strings.Contains(s, "L1 FAIL") {
		t.Errorf("missing L1 FAIL verdict:\n%s", s)
	}
	if strings.Contains(s, "To obtain stronger evidence") {
		t.Errorf("next-steps section should not appear on fail")
	}
}

func TestRenderHuman_ParseError(t *testing.T) {
	r := model.Result{
		Version:  "0.1.0",
		Outcome:  model.OutcomeParseError,
		ExitCode: 20,
		Checks: []model.Check{
			{Name: "parse_input", Status: model.StatusFail, Message: "could not read file"},
		},
	}
	var buf bytes.Buffer
	RenderHuman(&buf, r)
	s := buf.String()
	if !strings.Contains(s, "PARSE ERROR") {
		t.Errorf("missing PARSE ERROR:\n%s", s)
	}
	if !strings.Contains(s, "could not read file") {
		t.Errorf("parse error message not surfaced:\n%s", s)
	}
}

// replayResult builds a fully-populated replay Result with L1 + L3 sections.
func replayResult(outcome model.Outcome, exit int, l3 []model.Check, strategy model.L3Strategy) model.Result {
	r := sampleResult(outcome, exit)
	r.Command = "replay"
	r.L3Checks = l3
	r.L3Strategy = strategy
	return r
}

func TestRenderHuman_L3NoEvidence(t *testing.T) {
	l3 := []model.Check{
		{Name: "l3_strategy", Status: model.StatusPass, Message: "tiktoken offline (OpenAI plain text)"},
		{Name: "prompt_tokens_match", Status: model.StatusPass, Details: map[string]any{
			"computed": 8, "reported": 8,
		}},
		{Name: "model_match", Status: model.StatusPass, Details: map[string]any{
			"request": "gpt-4o-mini", "response": "gpt-4o-mini",
		}},
	}
	r := replayResult(model.OutcomeNoEvidenceOfTampering, 0, l3, model.L3TiktokenOffline)
	r.TrustLevelReached = model.TrustL3NoEvidence

	var buf bytes.Buffer
	RenderHuman(&buf, r)
	s := buf.String()

	mustContain := []string{
		"pr-audit v0.1.0",
		"[L1 · Self-consistency checks]",
		"[L3 · End-to-end replay]",
		"strategy: tiktoken_offline",
		"prompt_tokens matches reported value",
		"model field matches request",
		"NO EVIDENCE OF TAMPERING",
		"L1 + L3 reconciled",
		"Exit code: 0",
	}
	for _, want := range mustContain {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output:\n%s", want, s)
		}
	}
	for _, forbidden := range []string{"VERIFIED", "HONEST"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("forbidden phrase %q present in human output", forbidden)
		}
	}
}

func TestRenderHuman_L3Fail(t *testing.T) {
	l3 := []model.Check{
		{Name: "l3_strategy", Status: model.StatusPass, Message: "count_tokens API (Anthropic)"},
		{
			Name: "prompt_tokens_match", Status: model.StatusFail,
			Message: "prompt_tokens mismatch: count_tokens=120, sum(input+cache_creation+cache_read)=99 (diff -21)",
			Details: map[string]any{"count_tokens_result": 120, "expected_sum": 99},
		},
		{Name: "model_match", Status: model.StatusPass, Details: map[string]any{
			"request": "claude-3-5-sonnet-20240620", "response": "claude-3-5-sonnet-20240620",
		}},
	}
	r := replayResult(model.OutcomeL3Fail, 40, l3, model.L3CountTokensAPI)
	r.TrustLevelReached = model.TrustL3Fail
	r.NextSteps = nil

	var buf bytes.Buffer
	RenderHuman(&buf, r)
	s := buf.String()

	for _, want := range []string{
		"L3 FAIL",
		"prompt_tokens DOES NOT match reported value",
		"prompt_tokens mismatch: count_tokens=120",
		"L3 failed",
		"Exit code: 40",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output:\n%s", want, s)
		}
	}
}

func TestRenderHuman_L3Skipped(t *testing.T) {
	l3 := []model.Check{
		{Name: "l3_strategy", Status: model.StatusSkip, Message: "SKIPPED — vendor \"deepseek\" has no count_tokens endpoint or offline tokenizer in this version"},
	}
	r := replayResult(model.OutcomeL3Skipped, 0, l3, model.L3Skipped)
	r.TrustLevelReached = model.TrustL3Skipped

	var buf bytes.Buffer
	RenderHuman(&buf, r)
	s := buf.String()

	for _, want := range []string{
		"[L3 · End-to-end replay]",
		"L3 SKIPPED",
		"could not run",
		"Exit code: 0 (L3 skipped",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output:\n%s", want, s)
		}
	}
}

func TestRenderHuman_L3Degraded(t *testing.T) {
	l3 := []model.Check{
		{Name: "l3_strategy", Status: model.StatusSkip, Message: "structural — OpenAI tool calls — prompt_tokens not reliably verifiable offline"},
		{Name: "prompt_tokens_match", Status: model.StatusSkip, Message: "SKIPPED — not reliably verifiable offline (lower-bound estimate: 42)"},
		{Name: "model_match", Status: model.StatusPass, Details: map[string]any{
			"request": "gpt-4o-mini", "response": "gpt-4o-mini",
		}},
	}
	r := replayResult(model.OutcomeL3Degraded, 0, l3, model.L3Structural)
	r.TrustLevelReached = model.TrustL3Degraded

	var buf bytes.Buffer
	RenderHuman(&buf, r)
	s := buf.String()

	for _, want := range []string{
		"L3 DEGRADED",
		"prompt_tokens skipped",
		"model field matches request",
		"L3 partial coverage",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output:\n%s", want, s)
		}
	}
}

func TestRenderHuman_NetworkErrorExitCodes(t *testing.T) {
	cases := []struct {
		exit int
		want string
	}{
		{31, "network: DNS resolution failed"},
		{32, "network: TLS handshake / certificate failed"},
		{33, "network: upstream timeout or 5xx"},
	}
	for _, tc := range cases {
		r := replayResult(model.OutcomeL3Degraded, tc.exit, []model.Check{
			{Name: "l3_strategy", Status: model.StatusPass, Message: "count_tokens API (Anthropic)"},
			{Name: "prompt_tokens_match", Status: model.StatusWarn, Message: "count_tokens API unavailable: dns_failure: lookup foo: no such host"},
			{Name: "model_match", Status: model.StatusPass, Details: map[string]any{
				"request": "claude-3-5-sonnet", "response": "claude-3-5-sonnet",
			}},
		}, model.L3Structural)

		var buf bytes.Buffer
		RenderHuman(&buf, r)
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("exit %d: missing %q in output:\n%s", tc.exit, tc.want, buf.String())
		}
	}
}

// TestRenderJSON_VerifyBackwardCompat ensures the verify-only Result
// (no L3 fields) does NOT serialise empty L3 keys — keeps existing
// JSON consumers byte-stable.
func TestRenderJSON_VerifyBackwardCompat(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, sampleResult(model.OutcomeSelfConsistent, 0)); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, forbidden := range []string{"l3_strategy", "l3_checks"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("verify Result should not include %q (omitempty failed):\n%s", forbidden, s)
		}
	}
}

func TestRenderJSON_ReplayIncludesL3(t *testing.T) {
	r := replayResult(model.OutcomeNoEvidenceOfTampering, 0, []model.Check{
		{Name: "l3_strategy", Status: model.StatusPass, Message: "tiktoken offline (OpenAI plain text)"},
		{Name: "prompt_tokens_match", Status: model.StatusPass, Details: map[string]any{
			"computed": 8, "reported": 8,
		}},
		{Name: "model_match", Status: model.StatusPass, Details: map[string]any{
			"request": "gpt-4o-mini", "response": "gpt-4o-mini",
		}},
	}, model.L3TiktokenOffline)
	r.TrustLevelReached = model.TrustL3NoEvidence

	var buf bytes.Buffer
	if err := RenderJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("JSON invalid: %v\n%s", err, buf.String())
	}
	for _, k := range []string{"l3_strategy", "l3_checks"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in replay JSON output", k)
		}
	}
	if m["result"] != "no_evidence_of_tampering" {
		t.Errorf("result = %v, want no_evidence_of_tampering", m["result"])
	}
	if m["trust_level_reached"] != "L3_no_evidence_of_tampering" {
		t.Errorf("trust_level_reached = %v", m["trust_level_reached"])
	}
	checks, ok := m["l3_checks"].([]any)
	if !ok || len(checks) != 3 {
		t.Errorf("l3_checks should be a 3-element array, got %T len=%d", m["l3_checks"], len(checks))
	}
}

// TestRenderJSON_NeverLeaksVendorKey asserts that the Result type's JSON
// contract never exposes a vendor-key shaped field. We look for quoted
// JSON keys (with a leading quote) so the legitimate "vendor" field is
// not a false positive.
func TestRenderJSON_NeverLeaksVendorKey(t *testing.T) {
	r := sampleResult(model.OutcomeSelfConsistent, 0)
	var buf bytes.Buffer
	if err := RenderJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"vendor_key"`, `"vendorKey"`, `"VendorKey"`,
		`"api_key"`, `"x-api-key"`, `"authorization"`,
	} {
		if strings.Contains(buf.String(), forbidden) {
			t.Errorf("forbidden key %s in JSON output:\n%s", forbidden, buf.String())
		}
	}
}

func TestRenderJSON_SchemaKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, sampleResult(model.OutcomeSelfConsistent, 0)); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("JSON invalid: %v\n%s", err, buf.String())
	}
	for _, k := range []string{"version", "result", "exit_code", "trust_level_reached", "checks", "next_steps"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in JSON output", k)
		}
	}
	if m["result"] != "self_consistent" {
		t.Errorf("result = %v, want self_consistent", m["result"])
	}
	if m["exit_code"].(float64) != 0 {
		t.Errorf("exit_code = %v, want 0", m["exit_code"])
	}
}
