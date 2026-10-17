package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/config"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with browser OAuth",
	Long: `Log in to Antistatic Exchange.

By default, this opens a browser and completes OAuth (recommended).
For non-interactive environments, pass --token or set ANTISTATIC_TOKEN.`,
	Args: cobra.NoArgs,
	RunE: runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show login and encryption state",
	Args:  cobra.NoArgs,
	RunE:  runStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out and lock this machine",
	RunE:  runAuthLogout,
}

func init() {
	addLoginFlags(authLoginCmd)

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)
	rootCmd.AddCommand(authCmd)

	// Top-level aliases for convenience.
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Alias for \"auth login\"",
		Args:  cobra.NoArgs,
		RunE:  runAuthLogin,
	}
	addLoginFlags(loginCmd)

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show login and encryption state",
		Args:  cobra.NoArgs,
		RunE:  runStatus,
	}

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out and lock this machine",
		RunE:  runAuthLogout,
	}

	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(logoutCmd)
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	token, _ := cmd.Flags().GetString("token")

	if token == "" {
		return runOAuthBrowserLogin(cmd)
	}

	if !strings.HasPrefix(token, "axk_") {
		output.Warn("Token doesn't start with 'axk_' — are you sure this is correct?")
	}

	// Under the lock, so a refresh in another process can't overwrite it.
	unlock, err := config.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	cfg.Token = token
	cfg.ClearOAuthState()
	cfg.AuthServer = config.NormalizeURL(cfg.ResolveBaseURL())
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println("Token saved.")
	return nil
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	// Under the lock, so a refresh in another process can't save the login back.
	unlock, err := config.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := forgetKeysLocked(); err != nil {
		return err
	}
	cfg.Token = ""
	cfg.ClearOAuthState()
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Println("Token removed from config.")
	if os.Getenv("ANTISTATIC_TOKEN") != "" {
		output.Warn("ANTISTATIC_TOKEN environment variable is still set.")
	}
	return nil
}

func addLoginFlags(loginCmd *cobra.Command) {
	loginCmd.Flags().StringP("token", "t", "", "API token for non-interactive login")
	loginCmd.Flags().Bool("no-browser", false, "Print login URL instead of opening a browser automatically")
}
