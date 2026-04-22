package verify

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/primerouter/pr-audit/internal/model"
	"github.com/primerouter/pr-audit/internal/vendor"
)

// SchemaVersion is the pr-audit JSON output schema version. Bumping this
// is a breaking change to downstream CI consumers.
const SchemaVersion = "0.1.0"

// Params captures verify's CLI inputs after flag parsing.
type Params struct {
	HeadersPath  string
	BodyPath     string
	ResponsePath string
}

// Run is the L1 verify pipeline. It always returns a Result — even for parse
// errors — so the caller can uniformly render JSON or human output.
// Exit-code policy lives here; callers just propagate result.ExitCode.
func Run(p Params) model.Result {
	res := model.Result{
		Version:   SchemaVersion,
		Command:   "verify",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	resp, err := loadResponse(p)
	if err != nil {
		res.Checks = []model.Check{{
			Name: "parse_input", Status: model.StatusFail, Message: err.Error(),
		}}
		res.Outcome = model.OutcomeParseError
		res.ExitCode = 20
		res.TrustLevelReached = model.TrustNone
		return res
	}

	bodyBytes, computed, size, err := loadBodyWithHash(resp)
	if err != nil {
		res.Checks = []model.Check{{
			Name: "read_body", Status: model.StatusFail, Message: err.Error(),
		}}
		res.Outcome = model.OutcomeParseError
		res.ExitCode = 20
		res.TrustLevelReached = model.TrustNone
		return res
	}

	res.Input = model.Input{Source: resp.Source, SizeBytes: size}
	res.Vendor = vendor.Detect(resp.Headers, bodyBytes)
	res.Model = ExtractModel(bodyBytes)
	res.TraceID = resp.Headers.Get("x-upstream-trace-id")

	u := ParseUsage(bodyBytes)
	if u.Present {
		res.Usage = &u
	}

	res.Checks = append(res.Checks, checkHeaderPresence(resp.Headers, res.Vendor))

	hashCheck, trust, outcome, exit := checkHash(resp.Headers.Get("x-upstream-sha256"), computed)
	res.Checks = append(res.Checks, hashCheck)
	res.TrustLevelReached = trust
	res.Outcome = outcome
	res.ExitCode = exit

	res.Checks = append(res.Checks, checkUsage(u))

	// Only suggest next steps when L1 didn't fail. A tampering signal shouldn't
	// be buried under "next steps" — the user should see it and stop.
	if outcome == model.OutcomeSelfConsistent || outcome == model.OutcomeL1Unavailable {
		res.NextSteps = nextSteps(res.Vendor, res.TraceID, p)
	}
	return res
}

func loadResponse(p Params) (*model.Response, error) {
	if p.ResponsePath != "" {
		return ParseCombined(p.ResponsePath)
	}
	return ParseSplit(p.HeadersPath, p.BodyPath)
}

func loadBodyWithHash(resp *model.Response) ([]byte, string, int64, error) {
	if resp.BodyInline != nil {
		return resp.BodyInline, HashBufferedBody(resp.BodyInline), int64(len(resp.BodyInline)), nil
	}
	return HashBody(resp.BodyPath)
}

// checkHeaderPresence reports which evidence headers were supplied. Missing
// headers are a "warn" — absence of evidence is not evidence of tampering
// (see trust-model §3.4 and tasks.md B4 降级).
func checkHeaderPresence(h http.Header, detectedVendor string) model.Check {
	details := map[string]any{}
	var missing []string
	for _, name := range []string{"x-upstream-sha256", "x-upstream-trace-id", "x-upstream-vendor"} {
		if v := h.Get(name); v != "" {
			details[name] = v
		} else {
			missing = append(missing, name)
		}
	}
	if detectedVendor != "" && detectedVendor != vendor.Unknown {
		details["resolved_vendor"] = detectedVendor
	}
	if len(missing) == 0 {
		return model.Check{Name: "header_presence", Status: model.StatusPass, Details: details}
	}
	return model.Check{
		Name:    "header_presence",
		Status:  model.StatusWarn,
		Message: "missing: " + strings.Join(missing, ", "),
		Details: details,
	}
}

// checkHash is the L1 core: declared vs computed. Returns the check plus
// trust level, outcome, and exit code so Run doesn't re-derive them.
func checkHash(declared, computed string) (model.Check, model.TrustLevel, model.Outcome, int) {
	details := map[string]any{"computed": "sha256:" + computed}

	if declared == "" {
		return model.Check{
				Name:    "sha256_match",
				Status:  model.StatusWarn,
				Message: "x-upstream-sha256 absent — PrimeRouter has not enabled integrity attestation",
				Details: details,
			},
			model.TrustL1Unavailable, model.OutcomeL1Unavailable, 0
	}
	algo, digest, ok := ParseHashHeader(declared)
	details["declared"] = declared
	if !ok {
		return model.Check{
				Name: "sha256_match", Status: model.StatusFail,
				Message: fmt.Sprintf("malformed x-upstream-sha256 header: %q", declared),
				Details: details,
			},
			model.TrustL1Fail, model.OutcomeL1Fail, 10
	}
	if algo != "sha256" {
		return model.Check{
				Name: "sha256_match", Status: model.StatusFail,
				Message: fmt.Sprintf("unsupported hash algorithm %q (v0.1.0 supports sha256 only)", algo),
				Details: details,
			},
			model.TrustL1Fail, model.OutcomeL1Fail, 10
	}
	if digest != computed {
		return model.Check{
				Name: "sha256_match", Status: model.StatusFail,
				Message: "local SHA256 does not match declared value",
				Details: details,
			},
			model.TrustL1Fail, model.OutcomeL1Fail, 10
	}
	return model.Check{
			Name: "sha256_match", Status: model.StatusPass,
			Details: details,
		},
		model.TrustL1SelfConsistent, model.OutcomeSelfConsistent, 0
}

func checkUsage(u model.Usage) model.Check {
	if !u.Present {
		return model.Check{
			Name:    "usage_parsed",
			Status:  model.StatusWarn,
			Message: "no usage field in body (error response or non-terminal SSE chunk?)",
		}
	}
	d := map[string]any{}
	// Token fields that are legitimately never 0 in a successful response —
	// filter zero to keep the output clean. Cache fields CAN be 0; when the
	// response body actually emitted them, ParseUsage still records presence,
	// but at L1 we only surface non-zero to avoid cluttering the output.
	// (At L3 we'll need per-field presence tracking; deferred to v0.2.)
	addIfNonZero(d, "prompt_tokens", u.PromptTokens)
	addIfNonZero(d, "completion_tokens", u.CompletionTokens)
	addIfNonZero(d, "total_tokens", u.TotalTokens)
	addIfNonZero(d, "input_tokens", u.InputTokens)
	addIfNonZero(d, "output_tokens", u.OutputTokens)
	addIfNonZero(d, "cache_creation_input_tokens", u.CacheCreationInputTokens)
	addIfNonZero(d, "cache_read_input_tokens", u.CacheReadInputTokens)
	return model.Check{Name: "usage_parsed", Status: model.StatusPass, Details: d}
}

func addIfNonZero(m map[string]any, k string, v int) {
	if v != 0 {
		m[k] = v
	}
}

func nextSteps(vnd, trace string, p Params) []model.NextStep {
	steps := []model.NextStep{}

	if url := vendor.DashboardURL(vnd, trace); url != "" {
		steps = append(steps, model.NextStep{
			Level:  "L2",
			Action: "verify_trace_id_on_vendor_dashboard",
			URL:    url,
		})
	} else if trace != "" {
		steps = append(steps, model.NextStep{
			Level:  "L2",
			Action: "look_up_trace_id_in_vendor_console",
		})
	}

	cmd := "pr-audit replay"
	switch {
	case p.HeadersPath != "" && p.BodyPath != "":
		cmd += fmt.Sprintf(" --headers %s --body %s", p.HeadersPath, p.BodyPath)
	case p.ResponsePath != "":
		cmd += " --response " + p.ResponsePath
	}
	cmd += " --vendor-key $YOUR_KEY   # available in v0.2"
	steps = append(steps, model.NextStep{
		Level:   "L3",
		Action:  "run_replay_with_own_vendor_key",
		Command: cmd,
	})
	return steps
}
