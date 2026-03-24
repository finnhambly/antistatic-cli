package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var quoteCmd = &cobra.Command{
	Use:   "quote <code>",
	Short: "Get a trade quote",
	Long: `Get a cost quote for placing a trade on a market.

The quote shows what it would cost to move probabilities to your
desired values, without actually placing the trade.

Pass probability updates as JSON via --updates, or pipe JSON to stdin.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		params := url.Values{}

		// The quote endpoint accepts the same params as the trade endpoint
		// but via GET query string
		updatesJSON, _ := cmd.Flags().GetString("updates")
		if updatesJSON != "" {
			params.Set("updates", updatesJSON)
		}

		resp, err := client.Get("/markets/"+code+"/quote", params)
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

		// Try structured rendering
		var quote struct {
			TotalCost float64 `json:"total_cost"`
			Trades    []struct {
				SubmarketID    int     `json:"submarket_id"`
				SubmarketLabel string  `json:"submarket_label"`
				FromProb       float64 `json:"from_probability"`
				ToProb         float64 `json:"to_probability"`
				Cost           float64 `json:"cost"`
				Shares         float64 `json:"shares"`
			} `json:"trades"`
		}
		if err := json.Unmarshal(data, &quote); err != nil {
			output.JSON(data)
			return nil
		}

		if len(quote.Trades) > 0 {
			headers := []string{"SUBMARKET", "FROM", "TO", "COST", "SHARES"}
			rows := make([][]string, len(quote.Trades))
			for i, t := range quote.Trades {
				rows[i] = []string{
					t.SubmarketLabel,
					fmt.Sprintf("%.1f%%", t.FromProb*100),
					fmt.Sprintf("%.1f%%", t.ToProb*100),
					fmt.Sprintf("%.4f", t.Cost),
					fmt.Sprintf("%.4f", t.Shares),
				}
			}
			output.Table(headers, rows)
			fmt.Printf("\nTotal cost: %.4f points\n", quote.TotalCost)
		} else {
			output.JSON(data)
		}

		return nil
	},
}

func init() {
	quoteCmd.Flags().String("updates", "", "Probability updates as JSON (e.g. '{\"submarket_id\": 1, \"probability\": 0.7}')")
	rootCmd.AddCommand(quoteCmd)
}
