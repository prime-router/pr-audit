package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/primerouter/pr-audit/internal/model"
	"github.com/primerouter/pr-audit/internal/output"
	"github.com/primerouter/pr-audit/internal/replay"
	"github.com/spf13/cobra"
)

const replayLong = `pr-audit replay — verify PrimeRouter response integrity end-to-end (L1 + L2 + L3)

Replays the original request directly against the upstream vendor using
your own API key, then compares deterministic fields (prompt_tokens,
model) against PrimeRouter's reported values.

Reconciliation strategies (auto-selected by upstream vendor):
  openai      tiktoken offline (no key needed for plain-text chat)
  anthropic   POST /v1/messages/count_tokens (key required)
  gemini      POST /v1beta/.../countTokens   (key required)
  zhipu / deepseek / moonshot / unknown      L3 SKIPPED

Vendor key (required for anthropic/gemini) can be supplied via:
  --vendor-key <key>                 # flag (takes precedence)
  PR_AUDIT_VENDOR_KEY=<key>          # environment variable
The key is used only against hard-coded upstream-vendor domains. It is
NEVER sent to PrimeRouter and NEVER appears in any output (human or JSON).

Exit codes:
  0   L1 passed and L3 passed / skipped / degraded
  10  L1 FAIL  — local hash does not match declared value (L3 not attempted)
  20  Input parse error (missing/invalid request.json or flags)
  31  Network: DNS resolution failed
  32  Network: TLS handshake / certificate failed
  33  Network: upstream timeout or 5xx
  40  L3 FAIL — prompt_tokens or model mismatch
  99  Internal error
`

var (
	replayHeadersFlag   string
	replayBodyFlag      string
	replayResponseFlag  string
	replayRequestFlag   string
	replayVendorKeyFlag string
	replayFormatFlag    string
)

var replayCmd = &cobra.Command{
	Use:   "replay",
	Short: "End-to-end replay verification (L1 + L2 + L3)",
	Long:  replayLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateReplayFlags(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(20)
		}

		// Resolve vendor key: CLI flag wins; env var is the fallback so
		// users can keep secrets out of shell history. Spec §5.2.
		key := replayVendorKeyFlag
		if key == "" {
			key = os.Getenv("PR_AUDIT_VENDOR_KEY")
		}

		result := replay.Run(model.ReplayParams{
			HeadersPath:  replayHeadersFlag,
			BodyPath:     replayBodyFlag,
			ResponsePath: replayResponseFlag,
			RequestPath:  replayRequestFlag,
			VendorKey:    key,
		})

		if replayFormatFlag == "json" {
			if err := output.RenderJSON(os.Stdout, result); err != nil {
				fmt.Fprintf(os.Stderr, "render json: %v\n", err)
				os.Exit(99)
			}
		} else {
			output.RenderHuman(os.Stdout, result)
		}
		os.Exit(result.ExitCode)
		return nil
	},
}

// validateReplayFlags enforces the same input-mode invariants as verify
// (mutually exclusive --response vs --headers/--body) plus the
// replay-specific --request requirement.
func validateReplayFlags() error {
	if replayFormatFlag != "human" && replayFormatFlag != "json" {
		return fmt.Errorf("invalid --format: %q (want human|json)", replayFormatFlag)
	}

	hasSplit := replayHeadersFlag != "" || replayBodyFlag != ""
	hasCombined := replayResponseFlag != ""
	if hasSplit && hasCombined {
		return errors.New("cannot mix --response with --headers/--body")
	}
	if !hasSplit && !hasCombined {
		return errors.New("provide either --headers + --body or --response")
	}
	if hasSplit && (replayHeadersFlag == "" || replayBodyFlag == "") {
		return errors.New("both --headers and --body are required in split mode")
	}
	if replayRequestFlag == "" {
		return errors.New("--request <file> is required for replay (the original request.json)")
	}
	return nil
}

func init() {
	replayCmd.Flags().StringVar(&replayHeadersFlag, "headers", "", "HTTP headers file (split mode)")
	replayCmd.Flags().StringVar(&replayBodyFlag, "body", "", "HTTP body file (split mode)")
	replayCmd.Flags().StringVar(&replayResponseFlag, "response", "", "combined headers+body file (curl -i)")
	replayCmd.Flags().StringVar(&replayRequestFlag, "request", "", "original request JSON file (required)")
	replayCmd.Flags().StringVar(&replayVendorKeyFlag, "vendor-key", "", "upstream vendor API key (or set PR_AUDIT_VENDOR_KEY)")
	replayCmd.Flags().StringVar(&replayFormatFlag, "format", "human", "output format: human|json")
	rootCmd.AddCommand(replayCmd)
}
