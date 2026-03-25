package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var tradeCmd = &cobra.Command{
	Use:   "trade <code>",
	Short: "Submit probability updates as a trade",
	Long: `Place a trade on a market, updating submarket probabilities.

Pass probability updates via --updates as a JSON array, or pipe JSON to stdin.

For a review-first workflow, use "draft" (or "pending-edits") first, then
submit the trade once a human approves.

Example:
  antistatic trade my-market --updates '[{"submarket_id": 42, "probability": 0.75}]'
  echo '{"updates": [...]}' | antistatic trade my-market`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		updatesJSON, _ := cmd.Flags().GetString("updates")

		var body map[string]interface{}

		if updatesJSON != "" {
			// Parse --updates flag
			var updates []interface{}
			if err := json.Unmarshal([]byte(updatesJSON), &updates); err != nil {
				return fmt.Errorf("invalid --updates JSON: %w", err)
			}
			body = map[string]interface{}{"updates": updates}
		} else {
			// Try stdin
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				stdinData, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}
				if err := json.Unmarshal(stdinData, &body); err != nil {
					return fmt.Errorf("invalid JSON from stdin: %w", err)
				}
			} else {
				return fmt.Errorf("provide updates via --updates flag or pipe JSON to stdin")
			}
		}

		// Confirm if TTY and not --yes
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes && output.IsTTY() {
			fmt.Printf("Place trade on %s? [y/N] ", code)
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		resp, err := client.Post("/markets/"+code+"/positions", body)
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

		fmt.Println("Trade placed successfully.")

		var result struct {
			TotalCost float64 `json:"total_cost"`
			Positions int     `json:"position_count"`
		}
		if json.Unmarshal(data, &result) == nil {
			if result.TotalCost != 0 {
				fmt.Printf("Cost: %.4f points\n", result.TotalCost)
			}
		}

		return nil
	},
}

func init() {
	tradeCmd.Flags().String("updates", "", "Probability updates as JSON array")
	tradeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	rootCmd.AddCommand(tradeCmd)
}
