package cmd

import (
	"fmt"
	"os"
	"strings"

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
	RunE: runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove saved API token",
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
		RunE:  runAuthLogin,
	}
	addLoginFlags(loginCmd)

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Alias for \"auth status\"",
		RunE:  runAuthStatus,
	}

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Alias for \"auth logout\"",
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

	cfg.Token = token
	cfg.ClearOAuthState()
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println("Token saved.")
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	token := cfg.ResolveToken()
	baseURL := cfg.ResolveBaseURL()

	if token == "" {
		fmt.Println("Not authenticated.")
		fmt.Println("Run \"antistatic login\" (or \"antistatic auth login\") or set ANTISTATIC_TOKEN.")
		return nil
	}

	source := "config file"
	if os.Getenv("ANTISTATIC_TOKEN") != "" {
		source = "ANTISTATIC_TOKEN env var"
	}

	// Mask the token: show prefix + first 8 chars + ...
	masked := token
	if len(token) > 12 {
		masked = token[:12] + "..."
	}

	pairs := [][2]string{
		{"Server", baseURL},
		{"Token", masked},
		{"Source", source},
	}

	if source == "config file" {
		mode := "API token"
		if cfg.OAuthClientID != "" && cfg.OAuthRefreshToken != "" {
			mode = "OAuth session"
		}
		pairs = append(pairs, [2]string{"Mode", mode})
		if cfg.OAuthTokenExpiry != "" {
			pairs = append(pairs, [2]string{"Access token expiry", cfg.OAuthTokenExpiry})
		}
	}

	output.KeyValue(pairs)

	return nil
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
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
