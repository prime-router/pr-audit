package main

import (
	"fmt"
	"os"

	"github.com/primerouter/pr-audit/internal/output"
	"github.com/primerouter/pr-audit/internal/verify"
	"github.com/spf13/cobra"
)

const verifyLong = `pr-audit verify — check PrimeRouter response integrity (L1 self-consistency)

Input modes:
  Separate files (recommended; from 'curl -D headers.txt -o body.bin'):
    --headers <file>   HTTP headers file
    --body    <file>   HTTP body file (raw bytes)

  Combined file (from 'curl -i'):
    --response <file>  headers + blank line + body in one file

Exit codes:
  0   L1 passed, or L1 unavailable (missing evidence headers — not a failure)
  10  L1 FAIL — local hash does not match declared value
  11  Missing required evidence headers (strict mode — reserved, not yet used)
  20  Input parse error
  99  Internal error
`

var (
	verifyHeadersFlag  string
	verifyBodyFlag     string
	verifyResponseFlag string
	verifyFormatFlag   string
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify a saved PrimeRouter response (L1 self-consistency + L2 hints)",
	Long:  verifyLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		if verifyFormatFlag != "human" && verifyFormatFlag != "json" {
			fmt.Fprintf(os.Stderr, "invalid --format: %q (want human|json)\n", verifyFormatFlag)
			os.Exit(20)
		}

		hasSplit := verifyHeadersFlag != "" || verifyBodyFlag != ""
		hasCombined := verifyResponseFlag != ""
		if hasSplit && hasCombined {
			fmt.Fprintln(os.Stderr, "cannot mix --response with --headers/--body")
			os.Exit(20)
		}
		if !hasSplit && !hasCombined {
			_ = cmd.Help()
			os.Exit(20)
		}
		if hasSplit && (verifyHeadersFlag == "" || verifyBodyFlag == "") {
			fmt.Fprintln(os.Stderr, "both --headers and --body are required in split mode")
			os.Exit(20)
		}

		result := verify.Run(verify.Params{
			HeadersPath:  verifyHeadersFlag,
			BodyPath:     verifyBodyFlag,
			ResponsePath: verifyResponseFlag,
		})

		if verifyFormatFlag == "json" {
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

func init() {
	verifyCmd.Flags().StringVar(&verifyHeadersFlag, "headers", "", "HTTP headers file")
	verifyCmd.Flags().StringVar(&verifyBodyFlag, "body", "", "HTTP body file")
	verifyCmd.Flags().StringVar(&verifyResponseFlag, "response", "", "combined headers+body file (curl -i)")
	verifyCmd.Flags().StringVar(&verifyFormatFlag, "format", "human", "output format: human|json")
	rootCmd.AddCommand(verifyCmd)
}
