package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Feedback goes to the same inbox as the site's "Report a problem", so
// people and agents using the CLI can say what was tricky and what would help.

func newFeedbackCmd() *cobra.Command {
	var suggestion bool
	cmd := &cobra.Command{
		Use:   "feedback [message]",
		Short: "Send feedback to the Antistatic team",
		Long: "Send a problem report or a suggestion to the Antistatic team (the same inbox as \"Report a problem\" on the site).\n" +
			"Pass the message as arguments, or pipe it on stdin.",
		Example: "  antistatic feedback \"antistatic quote fails on date markets with one bar\"\n" +
			"  antistatic feedback --suggestion < notes.md",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			message := strings.TrimSpace(strings.Join(args, " "))
			if message == "" && !output.IsTTY() {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				message = strings.TrimSpace(string(data))
			}
			if message == "" {
				return fmt.Errorf("feedback cannot be empty")
			}
			kind := "problem"
			if suggestion {
				kind = "suggestion"
			}
			resp, err := client.Post("/feedback", map[string]string{"message": message, "kind": kind, "client": "CLI"})
			if err != nil {
				return err
			}
			if jsonOutput || !output.IsTTY() {
				output.JSON(unwrapData(resp))
				return nil
			}
			fmt.Println("Sent. Thank you.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&suggestion, "suggestion", false, "Send as a suggestion rather than a problem")
	return cmd
}

func init() {
	rootCmd.AddCommand(newFeedbackCmd())
}
