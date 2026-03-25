package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var commentEditCmd = &cobra.Command{
	Use:   "comment-edit <code> <comment-id> [text]",
	Short: "Edit an existing comment",
	Long: `Edit one of your own comments on a market.

Provide the new text as arguments, via --body flag, or pipe via stdin.
The body supports Markdown formatting (headings, bold, links, etc.).

Examples:
  antistatic comment-edit us-troops-iran 123 "Updated analysis here"
  antistatic comment-edit us-troops-iran 123 --body "## Updated\n\nNew analysis"
  echo "Multi-line comment" | antistatic comment-edit us-troops-iran 123`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		commentID, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid comment ID %q: must be a number", args[1])
		}

		text, err := resolveCommentBody(cmd, args[2:])
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

		resp, err := client.Put(fmt.Sprintf("/markets/%s/comments/%d", code, commentID), body)
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

		fmt.Printf("Comment %d updated.\n", commentID)
		return nil
	},
}

// resolveCommentBody returns comment text from --body flag, positional args, or stdin.
func resolveCommentBody(cmd *cobra.Command, textArgs []string) (string, error) {
	bodyFlag, _ := cmd.Flags().GetString("body")
	if bodyFlag != "" {
		return bodyFlag, nil
	}

	if len(textArgs) > 0 {
		return strings.Join(textArgs, " "), nil
	}

	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		stdinData, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return strings.TrimRight(string(stdinData), "\n"), nil
	}

	return "", fmt.Errorf("provide comment text as arguments, via --body, or pipe via stdin")
}

func init() {
	commentEditCmd.Flags().String("body", "", "Comment body text (supports Markdown; use for multi-line content)")
	rootCmd.AddCommand(commentEditCmd)
}
