package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "v0.1.0-dev"

var rootCmd = &cobra.Command{
	Use:           "pr-audit",
	Short:         "PrimeRouter response integrity audit",
	Long:          "pr-audit audits PrimeRouter LLM-gateway response integrity via a three-tier trust model (L1 local hash, L2 vendor dashboard, L3 replay).",
	SilenceErrors: true,
	SilenceUsage:  true,
	Version:       version,
}

func init() {
	rootCmd.SetVersionTemplate("pr-audit {{.Version}}\n")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
