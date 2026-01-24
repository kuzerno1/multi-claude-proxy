// Package cmd contains the CLI commands for multi-claude-proxy.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	// Version is set at build time
	Version = "dev"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "multi-claude-proxy",
	Short: "A multi-provider proxy for Claude Code CLI",
	Long: `Multi-Claude-Proxy is a proxy server that exposes an Anthropic-compatible API
backed by multiple providers (Antigravity, OpenAI, etc.).

It enables using Claude Code CLI with various model backends while maintaining
full compatibility with the Anthropic Messages API.`,
	Version: Version,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Global flags can be added here
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
}
