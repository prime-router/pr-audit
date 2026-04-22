package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/primerouter/pr-audit/internal/output"
	"github.com/primerouter/pr-audit/internal/verify"
)

const verifyUsage = `pr-audit verify — check PrimeRouter response integrity (L1 self-consistency)

Usage:
  pr-audit verify --headers <file> --body <file> [--format human|json]
  pr-audit verify --response <file>                [--format human|json]

Input modes:
  Separate files (recommended; from 'curl -D headers.txt -o body.bin'):
    --headers <file>   HTTP headers file
    --body    <file>   HTTP body file (raw bytes)

  Combined file (from 'curl -i'):
    --response <file>  headers + blank line + body in one file

Options:
  --format human|json  Output format (default: human)
  --help               Print this help

Exit codes:
  0   L1 passed, or L1 unavailable (missing evidence headers — not a failure)
  10  L1 FAIL — local hash does not match declared value
  11  Missing required evidence headers (strict mode — reserved, not yet used)
  20  Input parse error
  99  Internal error
`

func runVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, verifyUsage) }

	headers := fs.String("headers", "", "HTTP headers file")
	body := fs.String("body", "", "HTTP body file")
	response := fs.String("response", "", "combined headers+body file (curl -i)")
	format := fs.String("format", "human", "output format: human|json")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *format != "human" && *format != "json" {
		fmt.Fprintf(os.Stderr, "invalid --format: %q (want human|json)\n", *format)
		return 2
	}

	// Input selection: either (--headers + --body) or --response, not both.
	hasSplit := *headers != "" || *body != ""
	hasCombined := *response != ""
	if hasSplit && hasCombined {
		fmt.Fprintln(os.Stderr, "cannot mix --response with --headers/--body")
		return 2
	}
	if !hasSplit && !hasCombined {
		fmt.Fprint(os.Stderr, verifyUsage)
		return 2
	}
	if hasSplit && (*headers == "" || *body == "") {
		fmt.Fprintln(os.Stderr, "both --headers and --body are required in split mode")
		return 2
	}

	result := verify.Run(verify.Params{
		HeadersPath:  *headers,
		BodyPath:     *body,
		ResponsePath: *response,
	})

	if *format == "json" {
		if err := output.RenderJSON(os.Stdout, result); err != nil {
			fmt.Fprintf(os.Stderr, "render json: %v\n", err)
			return 99
		}
	} else {
		output.RenderHuman(os.Stdout, result)
	}
	return result.ExitCode
}
