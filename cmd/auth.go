package cmd

import (
	"fmt"
	"os"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with your browser",
	Args:  cobra.NoArgs,
	RunE:  runAuthLogin,
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
		Short: "Log in with your browser",
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
	return runOAuthBrowserLogin(cmd)
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	if err := forgetKeys(); err != nil {
		return err
	}
	cfg.Token = ""
	cfg.ClearOAuthState()
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Println("Logged out.")
	if os.Getenv("ANTISTATIC_TOKEN") != "" {
		output.Warn("ANTISTATIC_TOKEN environment variable is still set.")
	}
	return nil
}

func addLoginFlags(loginCmd *cobra.Command) {
	loginCmd.Flags().Bool("no-browser", false, "Print the login URL instead of opening a browser")
}
