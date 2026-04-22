package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/primerouter/pr-audit/internal/model"
)

func writeFixture(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// full integration: build a body + headers in a tempdir and run Run().
func TestRun_L1Pass(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"id":"x","model":"gpt-4o-mini","usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	bodyPath := writeFixture(t, dir, "body.bin", body)
	headers := "HTTP/2 200\r\ncontent-type: application/json\r\n" +
		"x-upstream-vendor: openai\r\n" +
		"x-upstream-trace-id: req_123\r\n" +
		"x-upstream-sha256: sha256:" + hashOf(body) + "\r\n\r\n"
	headersPath := writeFixture(t, dir, "h.txt", []byte(headers))

	r := Run(Params{HeadersPath: headersPath, BodyPath: bodyPath})
	if r.Outcome != model.OutcomeSelfConsistent {
		t.Fatalf("outcome = %s, want self_consistent", r.Outcome)
	}
	if r.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", r.ExitCode)
	}
	if r.TrustLevelReached != model.TrustL1SelfConsistent {
		t.Errorf("trust = %s, want L1_self_consistent", r.TrustLevelReached)
	}
	if r.Vendor != "openai" {
		t.Errorf("vendor = %s, want openai", r.Vendor)
	}
	if r.TraceID != "req_123" {
		t.Errorf("trace = %s, want req_123", r.TraceID)
	}
	if r.Usage == nil || r.Usage.PromptTokens != 3 {
		t.Errorf("usage missing or wrong: %+v", r.Usage)
	}
	// Both L2 and L3 next steps should be present on a pass.
	if len(r.NextSteps) != 2 {
		t.Errorf("expected 2 next steps, got %d", len(r.NextSteps))
	}
}

func TestRun_L1Fail_TamperedBody(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"hello":"world"}`)
	wrong := []byte(`{"hello":"WORLD"}`)
	bodyPath := writeFixture(t, dir, "body.bin", wrong)
	// header declares hash of the ORIGINAL body, but body.bin is the tampered one
	headers := "HTTP/2 200\r\nx-upstream-vendor: openai\r\nx-upstream-sha256: sha256:" + hashOf(body) + "\r\n\r\n"
	headersPath := writeFixture(t, dir, "h.txt", []byte(headers))

	r := Run(Params{HeadersPath: headersPath, BodyPath: bodyPath})
	if r.Outcome != model.OutcomeL1Fail {
		t.Errorf("outcome = %s, want l1_fail", r.Outcome)
	}
	if r.ExitCode != 10 {
		t.Errorf("exit = %d, want 10", r.ExitCode)
	}
	if len(r.NextSteps) != 0 {
		t.Errorf("should not suggest next steps on L1 fail; got %d", len(r.NextSteps))
	}
}

func TestRun_L1Unavailable_MissingHashHeader(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"model":"gpt-4o-mini"}`)
	bodyPath := writeFixture(t, dir, "body.bin", body)
	headers := "HTTP/2 200\r\nx-upstream-vendor: openai\r\nx-upstream-trace-id: req_xyz\r\n\r\n"
	headersPath := writeFixture(t, dir, "h.txt", []byte(headers))

	r := Run(Params{HeadersPath: headersPath, BodyPath: bodyPath})
	if r.Outcome != model.OutcomeL1Unavailable {
		t.Errorf("outcome = %s, want l1_unavailable", r.Outcome)
	}
	if r.ExitCode != 0 {
		t.Errorf("exit = %d, want 0 (absence of evidence is not failure)", r.ExitCode)
	}
	if r.TrustLevelReached != model.TrustL1Unavailable {
		t.Errorf("trust = %s, want L1_unavailable", r.TrustLevelReached)
	}
}

func TestRun_UnsupportedAlgorithm(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"x":1}`)
	bodyPath := writeFixture(t, dir, "body.bin", body)
	headers := "HTTP/2 200\r\nx-upstream-sha256: sha3-256:abc\r\n\r\n"
	headersPath := writeFixture(t, dir, "h.txt", []byte(headers))

	r := Run(Params{HeadersPath: headersPath, BodyPath: bodyPath})
	if r.Outcome != model.OutcomeL1Fail {
		t.Errorf("outcome = %s, want l1_fail on unsupported algorithm", r.Outcome)
	}
	if r.ExitCode != 10 {
		t.Errorf("exit = %d, want 10", r.ExitCode)
	}
}

func TestRun_ParseError(t *testing.T) {
	r := Run(Params{HeadersPath: "/nope/nonexistent.txt", BodyPath: "/nope/nonexistent.bin"})
	if r.Outcome != model.OutcomeParseError {
		t.Errorf("outcome = %s, want parse_error", r.Outcome)
	}
	if r.ExitCode != 20 {
		t.Errorf("exit = %d, want 20", r.ExitCode)
	}
}

func TestRun_CombinedModeSkipsInformational(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"id":"xxx","model":"glm-4-flash","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	combined := "HTTP/1.1 100 Continue\r\n\r\n" +
		"HTTP/2 200\r\ncontent-type: application/json\r\n" +
		"x-upstream-vendor: zhipu\r\n" +
		"x-upstream-sha256: sha256:" + hashOf(body) + "\r\n\r\n" +
		string(body)
	p := writeFixture(t, dir, "resp.txt", []byte(combined))

	r := Run(Params{ResponsePath: p})
	if r.Outcome != model.OutcomeSelfConsistent {
		t.Errorf("outcome = %s, want self_consistent", r.Outcome)
	}
	if r.Vendor != "zhipu" {
		t.Errorf("vendor = %s, want zhipu", r.Vendor)
	}
}
