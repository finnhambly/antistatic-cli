package cmd

import (
	"fmt"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/finnhambly/antistatic-cli/internal/update"
	"github.com/spf13/cobra"
)

// Version is set by goreleaser at build time.
var Version = "dev"

var (
	jsonOutput bool
	cfg        *config.Config
	client     *api.Client
)

var rootCmd = &cobra.Command{
	Use:   "antistatic",
	Short: "CLI for Antistatic Exchange",
	Long: `Antistatic is a command-line interface for Antistatic Exchange.

Browse markets, view forecasts, manage positions, and place trades
from the terminal. Works for both humans and AI agents.

Set ANTISTATIC_TOKEN or run "antistatic login" to authenticate.
Set ANTISTATIC_URL to override the default server (https://antistatic.exchange).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		client = api.NewClient(cfg)
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		interactive := output.IsTTY() && !jsonOutput
		update.MaybeNotify(Version, cfg, interactive)
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output raw JSON (default when stdout is not a terminal)")
	rootCmd.Version = Version
}

// requireAuth is a helper that checks for a configured token and returns
// a clear error if missing.
func requireAuth() error {
	if !client.HasAuth() {
		return fmt.Errorf("authentication required: run \"antistatic login\" (browser OAuth) or set ANTISTATIC_TOKEN")
	}
	return nil
}
