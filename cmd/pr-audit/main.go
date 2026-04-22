package main

import (
	"fmt"
	"os"
)

var version = "v0.1.0-dev"

const usage = `pr-audit — PrimeRouter response integrity audit

Usage:
  pr-audit <command> [options]

Commands:
  verify      Verify a saved PrimeRouter response (L1 self-consistency + L2 hints)
  --version   Print version
  --help      Print this help

Run 'pr-audit <command> --help' for command-specific options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--version", "-v", "version":
		fmt.Println("pr-audit", version)
	case "--help", "-h", "help":
		fmt.Print(usage)
	case "verify":
		os.Exit(runVerify(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
