package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var positionsCmd = &cobra.Command{
	Use:   "positions [code]",
	Short: "List positions",
	Long: `List your positions across all markets, or for a specific market.

If a market code is given, shows positions for that market only.
Otherwise, shows all positions across all markets.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		var path string
		if len(args) == 1 {
			path = "/markets/" + args[0] + "/positions"
		} else {
			path = "/positions"
		}

		resp, err := client.Get(path, nil)
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

		var positions []struct {
			MarketCode    string  `json:"market_code"`
			SubmarketLabel string `json:"submarket_label"`
			Probability   float64 `json:"probability"`
			Shares        float64 `json:"shares"`
			Cost          float64 `json:"cost"`
		}
		if err := json.Unmarshal(data, &positions); err != nil {
			output.JSON(data)
			return nil
		}

		if len(positions) == 0 {
			fmt.Println("No positions.")
			return nil
		}

		headers := []string{"MARKET", "SUBMARKET", "PROBABILITY", "SHARES", "COST"}
		rows := make([][]string, len(positions))
		for i, p := range positions {
			rows[i] = []string{
				p.MarketCode,
				p.SubmarketLabel,
				fmt.Sprintf("%.1f%%", p.Probability*100),
				fmt.Sprintf("%.2f", p.Shares),
				fmt.Sprintf("%.2f", p.Cost),
			}
		}
		output.Table(headers, rows)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(positionsCmd)
}
