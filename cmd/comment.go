package cmd

import (
	"fmt"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var commentCmd = &cobra.Command{
	Use:   "comment <code> <text>",
	Short: "Post a comment on a market",
	Long: `Post a comment on a market.

Example:
  antistatic comment us-troops-iran "I think this is underpriced"`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		text := strings.Join(args[1:], " ")

		body := map[string]interface{}{
			"body": text,
		}

		resp, err := client.Post("/markets/"+code+"/comments", body)
		if err != nil {
			return err
		}

		data, err := resp.Data()
		if err != nil {
			return err
		}

		if jsonOutput || !output.IsTTY() {
			output.JSON(data)
			return nil
		}

		fmt.Println("Comment posted.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(commentCmd)
}
