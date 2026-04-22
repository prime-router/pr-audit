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
		"pr-audit verify v0.1.0",
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
