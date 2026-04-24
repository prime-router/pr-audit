package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/primerouter/pr-audit/internal/model"
	"github.com/primerouter/pr-audit/internal/vendor"
	"github.com/primerouter/pr-audit/internal/verify"
)

// Run is the L3 replay pipeline. Mirrors verify.Run's contract: returns
// a fully populated model.Result so the caller can render either format
// uniformly. Exit-code policy lives entirely here.
//
// Flow (spec §3.2):
//  1. Reuse verify.Run for L1 + body parsing + vendor detection.
//  2. If L1 failed, stop — body is no longer a reliable baseline.
//  3. Parse the saved request.json.
//  4. Resolve the vendor key (flag → env var).
//  5. Route to the vendor-specific reconciler.
//  6. Compute the final outcome + exit code.
//  7. Always emit L2 hints.
func Run(p model.ReplayParams) model.Result {
	res := model.Result{
		Version:   verify.SchemaVersion,
		Command:   "replay",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// ── 1. L1 (delegates to verify) ──────────────────────────────────
	l1 := verify.Run(verify.Params{
		HeadersPath:  p.HeadersPath,
		BodyPath:     p.BodyPath,
		ResponsePath: p.ResponsePath,
	})

	// Carry over everything L1 already established.
	res.Input = l1.Input
	res.Vendor = l1.Vendor
	res.Model = l1.Model
	res.TraceID = l1.TraceID
	res.Usage = l1.Usage
	res.Checks = l1.Checks

	// ── 2. L1 hard-fails are terminal ────────────────────────────────
	if l1.Outcome == model.OutcomeL1Fail {
		res.Outcome = l1.Outcome
		res.TrustLevelReached = l1.TrustLevelReached
		res.ExitCode = l1.ExitCode
		// Don't render L2 next steps on L1 fail — see verify.Run §80.
		return res
	}
	if l1.Outcome == model.OutcomeParseError {
		res.Outcome = l1.Outcome
		res.TrustLevelReached = l1.TrustLevelReached
		res.ExitCode = l1.ExitCode
		return res
	}

	// ── 3. Parse the saved request.json ──────────────────────────────
	req, err := parseRequest(p.RequestPath)
	if err != nil {
		res.Checks = append(res.Checks, model.Check{
			Name:    "parse_request",
			Status:  model.StatusFail,
			Message: err.Error(),
		})
		res.Outcome = model.OutcomeParseError
		res.TrustLevelReached = model.TrustNone
		res.ExitCode = 20
		return res
	}

	// ── 4. Vendor key: flag wins, fallback to env. The key never enters
	//      res — see internal/output/json.go for the redaction guarantee.
	vendorKey := p.VendorKey
	if vendorKey == "" {
		vendorKey = os.Getenv("PR_AUDIT_VENDOR_KEY")
	}

	// ── 5. Strategy routing ──────────────────────────────────────────
	v := l1.Vendor
	var l3Checks []model.Check
	var l3Strategy model.L3Strategy
	switch v {
	case vendor.OpenAI, vendor.AzureOpenAI:
		l3Checks, l3Strategy = replayOpenAI(req, l1.Usage, l1.Model)
	case vendor.Anthropic:
		if vendorKey == "" {
			l3Checks, l3Strategy = replaySkipped("vendor-key required for Anthropic count_tokens API (use --vendor-key or PR_AUDIT_VENDOR_KEY)")
		} else {
			l3Checks, l3Strategy = replayAnthropic(req, l1.Usage, l1.Model, vendorKey)
		}
	case vendor.Gemini:
		if vendorKey == "" {
			l3Checks, l3Strategy = replaySkipped("vendor-key required for Gemini countTokens API (use --vendor-key or PR_AUDIT_VENDOR_KEY)")
		} else {
			l3Checks, l3Strategy = replayGemini(req, l1.Usage, l1.Model, vendorKey)
		}
	case vendor.Zhipu, vendor.DeepSeek, vendor.Moonshot:
		l3Checks, l3Strategy = replaySkipped(fmt.Sprintf("vendor %q has no count_tokens endpoint or offline tokenizer in this version", v))
	default:
		l3Checks, l3Strategy = replaySkipped(fmt.Sprintf("vendor %q is not yet supported for L3 replay", v))
	}

	res.L3Strategy = l3Strategy
	res.L3Checks = l3Checks

	// ── 6. Final outcome / exit code ─────────────────────────────────
	outcome, trust, exit := resolveL3(l1, l3Checks, l3Strategy)
	res.Outcome = outcome
	res.TrustLevelReached = trust
	res.ExitCode = exit

	// Network errors (DNS/TLS/timeout) take precedence over the strategy
	// outcome: surface the precise error code so users can distinguish a
	// network problem from an actual L3 fail.
	if code := networkErrorCodeFromChecks(l3Checks); code != 0 {
		res.ExitCode = code
	}

	// ── 7. L2 hints — always rendered when we got past L1 ────────────
	res.NextSteps = buildNextSteps(l1.Vendor, l1.TraceID, p)
	return res
}

// parseRequest loads request.json. We only require model + messages
// because that's what every reconciler needs; tools / stream are picked
// up via json.RawMessage as needed.
func parseRequest(path string) (model.ReplayRequest, error) {
	if path == "" {
		return model.ReplayRequest{}, errors.New("--request file path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return model.ReplayRequest{}, fmt.Errorf("read request file: %w", err)
	}
	var req model.ReplayRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return model.ReplayRequest{}, fmt.Errorf("parse request JSON: %w", err)
	}
	if req.Model == "" {
		return model.ReplayRequest{}, errors.New("request JSON missing 'model' field")
	}
	if len(req.Messages) == 0 {
		return model.ReplayRequest{}, errors.New("request JSON missing 'messages' field")
	}
	return req, nil
}

// resolveL3 maps the L3 check list + strategy + L1 outcome into the
// final (outcome, trust, exit code) triple per spec §6 / §8.4.
func resolveL3(l1 model.Result, checks []model.Check, strategy model.L3Strategy) (model.Outcome, model.TrustLevel, int) {
	// Strategy not run at all → keep L1's verdict.
	if strategy == model.L3Skipped {
		return outcomeWithSkippedL3(l1)
	}

	hasFail := false
	for _, ck := range checks {
		if ck.Status == model.StatusFail {
			hasFail = true
			break
		}
	}
	if hasFail {
		return model.OutcomeL3Fail, model.TrustL3Fail, 40
	}

	if strategy == model.L3Structural {
		return model.OutcomeL3Degraded, model.TrustL3Degraded, 0
	}

	// All checks passed (or warned) under a hard-reconciliation strategy.
	return model.OutcomeNoEvidenceOfTampering, model.TrustL3NoEvidence, 0
}

// outcomeWithSkippedL3 picks a verdict label that still tells the user
// L3 was skipped, while preserving L1's actual exit code (always 0 here
// because we already returned early on L1 fail).
func outcomeWithSkippedL3(l1 model.Result) (model.Outcome, model.TrustLevel, int) {
	if l1.Outcome == model.OutcomeL1Unavailable {
		return model.OutcomeL3Skipped, model.TrustL3Skipped, 0
	}
	return model.OutcomeL3Skipped, model.TrustL3Skipped, 0
}

// networkErrorCodeFromChecks scans for a netError tucked into a check
// message. We use the message string (errors don't survive JSON), so
// the encoding is "<prefix>: <wrapped>" — matched by category prefix.
//
// This is a best-effort lift; if the message doesn't recognise as a
// classified network error we return 0 and the caller keeps its code.
func networkErrorCodeFromChecks(checks []model.Check) int {
	for _, ck := range checks {
		if ck.Status != model.StatusWarn {
			continue
		}
		switch {
		case containsAny(ck.Message, "DNS resolution failed"):
			return 31
		case containsAny(ck.Message, "TLS certificate", "TLS handshake"):
			return 32
		case containsAny(ck.Message, "upstream timeout", "deadline exceeded", "upstream error HTTP 5"):
			return 33
		}
	}
	return 0
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && stringContains(haystack, n) {
			return true
		}
	}
	return false
}

// stringContains avoids importing strings just for one call site in a
// hot-path function.
func stringContains(haystack, needle string) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return len(needle) == 0
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// buildNextSteps emits the L2 dashboard hint. L3 is not re-suggested:
// the user is already running replay, so the dashboard URL is the only
// stronger evidence path remaining.
func buildNextSteps(vnd, trace string, _ model.ReplayParams) []model.NextStep {
	steps := []model.NextStep{}
	if u := vendor.DashboardURL(vnd, trace); u != "" {
		// Defensive: ensure the URL is well-formed before suggesting it.
		if _, err := url.Parse(u); err == nil {
			steps = append(steps, model.NextStep{
				Level:  "L2",
				Action: "verify_trace_id_on_vendor_dashboard",
				URL:    u,
			})
		}
	} else if trace != "" {
		steps = append(steps, model.NextStep{
			Level:  "L2",
			Action: "look_up_trace_id_in_vendor_console",
		})
	}
	return steps
}
