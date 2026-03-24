package cmd

import (
	"bufio"
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
	Short: "Save an API token",
	Long: `Save an API token for authenticating with Antistatic Exchange.

Generate a token at https://antistatic.exchange/users/settings
or set the ANTISTATIC_TOKEN environment variable instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		token, _ := cmd.Flags().GetString("token")

		if token == "" {
			fmt.Print("Paste your API token (axk_...): ")
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading token: %w", err)
			}
			token = strings.TrimSpace(line)
		}

		if token == "" {
			return fmt.Errorf("no token provided")
		}

		if !strings.HasPrefix(token, "axk_") {
			output.Warn("Token doesn't start with 'axk_' — are you sure this is correct?")
		}

		cfg.Token = token
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		fmt.Println("Token saved.")
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		token := cfg.ResolveToken()
		baseURL := cfg.ResolveBaseURL()

		if token == "" {
			fmt.Println("Not authenticated.")
			fmt.Println("Run \"antistatic auth login\" or set ANTISTATIC_TOKEN.")
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

		output.KeyValue([][2]string{
			{"Server", baseURL},
			{"Token", masked},
			{"Source", source},
		})

		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove saved API token",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg.Token = ""
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Println("Token removed from config.")
		if os.Getenv("ANTISTATIC_TOKEN") != "" {
			output.Warn("ANTISTATIC_TOKEN environment variable is still set.")
		}
		return nil
	},
}

func init() {
	authLoginCmd.Flags().StringP("token", "t", "", "API token (or paste interactively)")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}
