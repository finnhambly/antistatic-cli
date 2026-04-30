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

Trades are quoted against starting_probability (house line), not
community_probability.

Updates may identify rows by "submarket" (sm_<id>), legacy "submarket_id", or by "label" (optionally with
"group"/"projection_group" when labels are ambiguous).

Example:
  antistatic trade my-market --updates '[{"submarket": "sm_42", "probability": "0.75"}]'
  echo '{"updates": [...]}' | antistatic trade my-market`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		updatesJSON, _ := cmd.Flags().GetString("updates")
		fromDraft, _ := cmd.Flags().GetBool("from-draft")
		noAutoShape, _ := cmd.Flags().GetBool("no-auto-shape")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		estimateCost, _ := cmd.Flags().GetBool("estimate-cost")
		autoShape := !noAutoShape
		remainderRequest, err := parseMulticountRemainderRequest(cmd)
		if err != nil {
			return err
		}

		var body map[string]interface{}

		if updatesJSON != "" {
			body, err = parseTradePayloadBytes([]byte(updatesJSON), fromDraft)
			if err != nil {
				return fmt.Errorf("invalid --updates JSON: %w", err)
			}
		} else {
			// Try stdin
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				stdinData, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}
				body, err = parseTradePayloadBytes(stdinData, fromDraft)
				if err != nil {
					return fmt.Errorf("invalid JSON from stdin: %w", err)
				}
			} else {
				return fmt.Errorf("provide updates via --updates flag or pipe JSON to stdin")
			}
		}

		if err := resolveUpdateLabelsInBody(code, body); err != nil {
			return err
		}

		defaultFixed := true
		updates, err := parseProbabilityUpdatesFromBodyWithDefault(body, &defaultFixed)
		if err != nil {
			return err
		}

		updates, remainderReport, err := shapeAndApplyRemainder(
			code, updates, autoShape, false, remainderRequest,
		)
		if err != nil {
			return err
		}
		printMulticountRemainderNotice(code, remainderReport, remainderRequest)
		body["updates"] = probabilityUpdatesToPayload(updates)

		estimatedCost := 0.0
		hasEstimate := false
		if len(updates) > 0 && (output.IsTTY() || dryRun || estimateCost) {
			totalCost, quoteErr := previewTradeCost(code, updates)
			if quoteErr == nil {
				estimatedCost = totalCost
				hasEstimate = true
				if output.IsTTY() {
					fmt.Printf("Estimated cost: %.4f points across %d submarket(s).\n", estimatedCost, len(updates))
				}
			}
		}

		if dryRun {
			preview := map[string]interface{}{
				"market_code": code,
				"updates":     body["updates"],
				"dry_run":     true,
			}
			if hasEstimate {
				preview["estimated_cost"] = estimatedCost
			}
			raw, _ := json.Marshal(preview)
			output.JSON(raw)
			return nil
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
	tradeCmd.Flags().Bool("from-draft", false, "Treat input as draft planner output ({\"updates\": [...]})")
	tradeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	tradeCmd.Flags().Bool("dry-run", false, "Preview shaped updates (and estimated cost) without placing a trade")
	tradeCmd.Flags().Bool("estimate-cost", false, "Estimate total trade cost before submission")
	tradeCmd.Flags().Bool("no-auto-shape", false, "Disable auto interpolation and monotonic shaping")
	addMulticountRemainderFlags(tradeCmd)

	rootCmd.AddCommand(tradeCmd)
}

func parseTradePayloadBytes(raw []byte, fromDraft bool) (map[string]interface{}, error) {
	var parsed interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}

	switch typed := parsed.(type) {
	case []interface{}:
		return map[string]interface{}{"updates": typed}, nil
	case map[string]interface{}:
		updatesRaw, ok := typed["updates"]
		if !ok {
			if fromDraft {
				return nil, fmt.Errorf("expected draft JSON object with an updates array")
			}
			return nil, fmt.Errorf("expected JSON updates array or object with updates array")
		}

		updates, ok := updatesRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("updates must be an array")
		}
		return map[string]interface{}{"updates": updates}, nil
	default:
		return nil, fmt.Errorf("expected JSON updates array or object with updates array")
	}
}
