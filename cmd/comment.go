package cmd

import (
	"fmt"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var commentCmd = &cobra.Command{
	Use:   "comment <code> [text]",
	Short: "Post a comment on a market",
	Long: `Post a comment on a market. The body supports Markdown formatting.

Provide the text as arguments, via --body flag, or pipe via stdin.
For multi-line content (Markdown with headings, lists, etc.), use --body
or stdin since shell arguments collapse whitespace.

Examples:
  antistatic comment us-troops-iran "I think this is underpriced"
  antistatic comment us-troops-iran --body "## Analysis\n\nKey points:\n- Point one\n- Point two"
  echo "Multi-line markdown comment" | antistatic comment us-troops-iran

Use "antistatic comments <code>" to read existing comments.
Use "antistatic comment-edit <code> <id> <text>" to edit a comment.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		text, err := resolveCommentBody(cmd, args[1:])
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("comment body cannot be empty")
		}

		body := map[string]interface{}{
			"body":   text,
			"format": "markdown",
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
	commentCmd.Flags().String("body", "", "Comment body text (supports Markdown; use for multi-line content)")
	rootCmd.AddCommand(commentCmd)
}
