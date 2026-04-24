// Package output renders verify.Result as either human-readable text or JSON.
// Human format uses a tiny hand-rolled ANSI helper (NO_COLOR & non-TTY aware)
// so we don't pull in a color library.
package output

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/primerouter/pr-audit/internal/model"
)

// RenderHuman writes a terminal-friendly report. Color is applied only when
// stdout is a TTY and NO_COLOR is unset.
func RenderHuman(w io.Writer, r model.Result) {
	c := newColorizer(w)

	// Header is always "pr-audit v<X>" regardless of subcommand so the two
	// commands feel like one tool. Per spec discussion (2026-04-22 review).
	fmt.Fprintf(w, "pr-audit v%s\n\n", r.Version)

	if r.Outcome == model.OutcomeParseError {
		fmt.Fprintln(w, c.red("[\u2717] Input could not be parsed"))
		for _, ck := range r.Checks {
			if ck.Message != "" {
				fmt.Fprintf(w, "    %s\n", ck.Message)
			}
		}
		fmt.Fprintf(w, "\nResult: %s\nExit code: %d\n", c.red("PARSE ERROR"), r.ExitCode)
		return
	}

	fmt.Fprintln(w, c.bold("[L1 \u00b7 Self-consistency checks]"))
	for _, ck := range r.Checks {
		renderCheck(w, c, ck)
	}

	// L3 section is replay-only; verify never populates L3Checks. We key on
	// the slice (not Command) so RenderHuman remains a pure function of the
	// Result document.
	if len(r.L3Checks) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, c.bold("[L3 \u00b7 End-to-end replay]"))
		if r.L3Strategy != "" {
			fmt.Fprintf(w, "    %s\n", c.dim("strategy: "+string(r.L3Strategy)))
		}
		for _, ck := range r.L3Checks {
			renderCheck(w, c, ck)
		}
	}

	fmt.Fprintln(w)
	renderVerdict(w, c, r)
	renderMeta(w, c, r)
	renderNextSteps(w, c, r)

	fmt.Fprintf(w, "\nExit code: %d (%s)\n", r.ExitCode, exitCodeGloss(r))
}

func renderCheck(w io.Writer, c *colorizer, ck model.Check) {
	icon, tint := iconFor(c, ck.Status)
	label := checkLabel(ck)
	fmt.Fprintf(w, "%s %s\n", tint(icon), label)
	if ck.Message != "" {
		fmt.Fprintf(w, "    %s\n", ck.Message)
	}
	if len(ck.Details) > 0 {
		for _, k := range sortedKeys(ck.Details) {
			fmt.Fprintf(w, "    %s: %v\n", k, ck.Details[k])
		}
	}
}

func checkLabel(ck model.Check) string {
	switch ck.Name {
	case "header_presence":
		if ck.Status == model.StatusPass {
			return "Evidence headers present"
		}
		return "Evidence headers (partial)"
	case "sha256_match":
		switch ck.Status {
		case model.StatusPass:
			return "Body SHA256 matches declared value"
		case model.StatusWarn:
			return "Body SHA256 not declared by server"
		default:
			return "Body SHA256 DOES NOT match declared value"
		}
	case "usage_parsed":
		if ck.Status == model.StatusPass {
			return "Usage field parsed from body"
		}
		return "Usage field not parseable"
	case "parse_request":
		return "Replay request file"
	case "l3_strategy":
		switch ck.Status {
		case model.StatusPass:
			return "L3 strategy ready"
		case model.StatusSkip:
			return "L3 strategy not run"
		default:
			return "L3 strategy"
		}
	case "prompt_tokens_match":
		switch ck.Status {
		case model.StatusPass:
			return "prompt_tokens matches reported value"
		case model.StatusFail:
			return "prompt_tokens DOES NOT match reported value"
		case model.StatusWarn:
			return "prompt_tokens not reconciled (degraded)"
		case model.StatusSkip:
			return "prompt_tokens skipped"
		default:
			return "prompt_tokens"
		}
	case "model_match":
		switch ck.Status {
		case model.StatusPass:
			return "model field matches request"
		case model.StatusFail:
			return "model field DOES NOT match request"
		case model.StatusWarn:
			return "model field absent from response body"
		default:
			return "model_match"
		}
	default:
		return ck.Name
	}
}

func renderVerdict(w io.Writer, c *colorizer, r model.Result) {
	switch r.Outcome {
	case model.OutcomeSelfConsistent:
		fmt.Fprintf(w, "Result: %s\n", c.green("SELF-CONSISTENT"))
		fmt.Fprintln(w, "  PrimeRouter's declared hash matches the body it delivered.")
		fmt.Fprintln(w, "  This rules out accidental corruption, but does NOT prove the body")
		fmt.Fprintln(w, "  equals what the upstream vendor actually returned.")
	case model.OutcomeL1Unavailable:
		fmt.Fprintf(w, "Result: %s\n", c.yellow("L1 UNAVAILABLE"))
		fmt.Fprintln(w, "  PrimeRouter did not emit x-upstream-sha256 for this response.")
		fmt.Fprintln(w, "  This is not a tampering signal \u2014 only that integrity attestation")
		fmt.Fprintln(w, "  is not yet enabled. Absence of evidence \u2260 evidence of absence.")
	case model.OutcomeL1Fail:
		fmt.Fprintf(w, "Result: %s\n", c.red("L1 FAIL"))
		fmt.Fprintln(w, "  Local SHA256 does not match PrimeRouter's declared value.")
		fmt.Fprintln(w, "  Preserve response and declared header; confirm with L2/L3 before")
		fmt.Fprintln(w, "  concluding this is intentional tampering (could also be a bug).")

	// ── L3 verdicts (replay only) ────────────────────────────────────
	case model.OutcomeNoEvidenceOfTampering:
		fmt.Fprintf(w, "Result: %s\n", c.green("NO EVIDENCE OF TAMPERING"))
		fmt.Fprintln(w, "  L1 self-consistency held AND L3 replay reconciled deterministic")
		fmt.Fprintln(w, "  fields (prompt_tokens, model) against the upstream vendor.")
		fmt.Fprintln(w, "  This is the strongest verdict pr-audit can produce; it does NOT")
		fmt.Fprintln(w, "  prove the assistant text itself is uncensored or unchanged.")
	case model.OutcomeL3Fail:
		fmt.Fprintf(w, "Result: %s\n", c.red("L3 FAIL"))
		fmt.Fprintln(w, "  Replay against the upstream vendor produced a different")
		fmt.Fprintln(w, "  prompt_tokens or model than PrimeRouter reported. This is a")
		fmt.Fprintln(w, "  tampering / mis-routing signal \u2014 stop and investigate before")
		fmt.Fprintln(w, "  trusting any answers from the same gateway run.")
	case model.OutcomeL3Skipped:
		fmt.Fprintf(w, "Result: %s\n", c.yellow("L3 SKIPPED"))
		fmt.Fprintln(w, "  L3 reconciliation could not run (vendor not supported or")
		fmt.Fprintln(w, "  vendor-key missing). L1's verdict above still stands; for")
		fmt.Fprintln(w, "  stronger evidence open the dashboard URL listed below.")
	case model.OutcomeL3Degraded:
		fmt.Fprintf(w, "Result: %s\n", c.yellow("L3 DEGRADED"))
		fmt.Fprintln(w, "  Only structural fields (e.g. model) were reconciled; prompt_tokens")
		fmt.Fprintln(w, "  could not be hard-checked (tools, multimodal, or upstream API down).")
		fmt.Fprintln(w, "  Treat as weaker than NO EVIDENCE OF TAMPERING; rely on L2 dashboard.")
	}
}

func renderMeta(w io.Writer, c *colorizer, r model.Result) {
	var parts []string
	if r.Vendor != "" {
		parts = append(parts, "vendor="+r.Vendor)
	}
	if r.Model != "" {
		parts = append(parts, "model="+r.Model)
	}
	if r.TraceID != "" {
		parts = append(parts, "trace-id="+r.TraceID)
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, "\n  %s\n", c.dim(strings.Join(parts, "  ")))
	}
}

func renderNextSteps(w io.Writer, c *colorizer, r model.Result) {
	if len(r.NextSteps) == 0 {
		return
	}
	fmt.Fprintln(w, "\n"+c.yellow("\u26a0  To obtain stronger evidence:"))
	for _, s := range r.NextSteps {
		switch s.Level {
		case "L2":
			fmt.Fprintln(w, "\n  "+c.bold("[L2 \u00b7 External attestation]"))
			if s.URL != "" {
				fmt.Fprintln(w, "    Open the vendor dashboard and confirm this request exists:")
				fmt.Fprintln(w, "      "+s.URL)
			} else {
				fmt.Fprintln(w, "    Look up the trace-id in your upstream vendor's console.")
			}
		case "L3":
			fmt.Fprintln(w, "\n  "+c.bold("[L3 \u00b7 End-to-end replay]"))
			if s.Command != "" {
				fmt.Fprintln(w, "    Re-run the same request with your own vendor key:")
				fmt.Fprintln(w, "      "+s.Command)
			} else {
				fmt.Fprintln(w, "    Re-run the same request with your own vendor key (see `pr-audit replay --help`).")
			}
		}
	}
}

func exitCodeGloss(r model.Result) string {
	switch r.ExitCode {
	case 0:
		switch r.Outcome {
		case model.OutcomeSelfConsistent:
			return "L1 passed"
		case model.OutcomeL1Unavailable:
			return "L1 unavailable \u2014 not a failure"
		case model.OutcomeNoEvidenceOfTampering:
			return "L1 + L3 reconciled"
		case model.OutcomeL3Skipped:
			return "L3 skipped \u2014 not a failure"
		case model.OutcomeL3Degraded:
			return "L3 partial coverage"
		default:
			return "ok"
		}
	case 10:
		return "L1 failed \u2014 see details above"
	case 20:
		return "parse error"
	case 31:
		return "network: DNS resolution failed"
	case 32:
		return "network: TLS handshake / certificate failed"
	case 33:
		return "network: upstream timeout or 5xx"
	case 40:
		return "L3 failed \u2014 see details above"
	case 99:
		return "internal error"
	default:
		return "internal error"
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- colorizer ----------------------------------------------------------

type colorizer struct{ enabled bool }

func newColorizer(w io.Writer) *colorizer {
	if os.Getenv("NO_COLOR") != "" {
		return &colorizer{enabled: false}
	}
	// We only colorize when writing to a real terminal. For os.Stdout this
	// is detected via Stat; other writers default to no color.
	f, ok := w.(*os.File)
	if !ok {
		return &colorizer{enabled: false}
	}
	fi, err := f.Stat()
	if err != nil {
		return &colorizer{enabled: false}
	}
	return &colorizer{enabled: (fi.Mode() & os.ModeCharDevice) != 0}
}

func (c *colorizer) wrap(code, s string) string {
	if !c.enabled {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (c *colorizer) red(s string) string    { return c.wrap("31", s) }
func (c *colorizer) green(s string) string  { return c.wrap("32", s) }
func (c *colorizer) yellow(s string) string { return c.wrap("33", s) }
func (c *colorizer) bold(s string) string   { return c.wrap("1", s) }
func (c *colorizer) dim(s string) string    { return c.wrap("2", s) }

func iconFor(c *colorizer, s model.CheckStatus) (string, func(string) string) {
	switch s {
	case model.StatusPass:
		return "[\u2713]", c.green
	case model.StatusFail:
		return "[\u2717]", c.red
	case model.StatusWarn:
		return "[!]", c.yellow
	case model.StatusSkip:
		return "[-]", c.dim
	default:
		return "[?]", c.dim
	}
}
