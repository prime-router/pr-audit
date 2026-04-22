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

	fmt.Fprintf(w, "pr-audit verify v%s\n\n", r.Version)

	if r.Outcome == model.OutcomeParseError {
		fmt.Fprintln(w, c.red("[✗] Input could not be parsed"))
		for _, ck := range r.Checks {
			if ck.Message != "" {
				fmt.Fprintf(w, "    %s\n", ck.Message)
			}
		}
		fmt.Fprintf(w, "\nResult: %s\nExit code: %d\n", c.red("PARSE ERROR"), r.ExitCode)
		return
	}

	fmt.Fprintln(w, c.bold("[L1 · Self-consistency checks]"))
	for _, ck := range r.Checks {
		renderCheck(w, c, ck)
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
		fmt.Fprintln(w, "  This is not a tampering signal — only that integrity attestation")
		fmt.Fprintln(w, "  is not yet enabled. Absence of evidence ≠ evidence of absence.")
	case model.OutcomeL1Fail:
		fmt.Fprintf(w, "Result: %s\n", c.red("L1 FAIL"))
		fmt.Fprintln(w, "  Local SHA256 does not match PrimeRouter's declared value.")
		fmt.Fprintln(w, "  Preserve response and declared header; confirm with L2/L3 before")
		fmt.Fprintln(w, "  concluding this is intentional tampering (could also be a bug).")
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
	fmt.Fprintln(w, "\n"+c.yellow("⚠  To obtain stronger evidence:"))
	for _, s := range r.NextSteps {
		switch s.Level {
		case "L2":
			fmt.Fprintln(w, "\n  "+c.bold("[L2 · External attestation]"))
			if s.URL != "" {
				fmt.Fprintln(w, "    Open the vendor dashboard and confirm this request exists:")
				fmt.Fprintln(w, "      "+s.URL)
			} else {
				fmt.Fprintln(w, "    Look up the trace-id in your upstream vendor's console.")
			}
		case "L3":
			fmt.Fprintln(w, "\n  "+c.bold("[L3 · End-to-end replay (v0.2)]"))
			fmt.Fprintln(w, "    Re-run the same request with your own vendor key:")
			fmt.Fprintln(w, "      "+s.Command)
		}
	}
}

func exitCodeGloss(r model.Result) string {
	switch r.Outcome {
	case model.OutcomeSelfConsistent:
		return "L1 passed"
	case model.OutcomeL1Unavailable:
		return "L1 unavailable — not a failure"
	case model.OutcomeL1Fail:
		return "L1 failed — see details above"
	case model.OutcomeParseError:
		return "parse error"
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
		return "[✓]", c.green
	case model.StatusFail:
		return "[✗]", c.red
	case model.StatusWarn:
		return "[!]", c.yellow
	case model.StatusSkip:
		return "[-]", c.dim
	default:
		return "[?]", c.dim
	}
}
