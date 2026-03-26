package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var positionsCmd = &cobra.Command{
	Use:   "positions [code]",
	Short: "List positions",
	Long: `List your positions across all markets, or for a specific market.

If a market code is given, shows positions for that market only.
Otherwise, shows all positions across all markets.

Use --market to filter without positional args (agent-friendly).
Use --summary for one aggregated row per market.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		marketFlag, _ := cmd.Flags().GetString("market")
		summary, _ := cmd.Flags().GetBool("summary")
		marketCode := strings.TrimSpace(marketFlag)

		if len(args) == 1 {
			if marketCode != "" {
				return fmt.Errorf("provide market either as positional arg or --market, not both")
			}
			marketCode = strings.TrimSpace(args[0])
		}

		path := "/positions"
		params := url.Values{}

		// Keep the legacy direct endpoint for positional-arg detail mode.
		if len(args) == 1 && marketFlag == "" && !summary {
			path = "/markets/" + marketCode + "/positions"
		} else {
			if marketCode != "" {
				params.Set("market_code", marketCode)
			}
			if summary {
				params.Set("summary", "market")
			}
		}

		resp, err := client.Get(path, params)
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

		if summary {
			var summaries []struct {
				MarketCode    string  `json:"market_code"`
				PositionCount int     `json:"position_count"`
				NetShares     float64 `json:"net_shares"`
				NetCost       float64 `json:"net_cost"`
				Shares        float64 `json:"shares"`
				Cost          float64 `json:"cost"`
			}
			if err := json.Unmarshal(data, &summaries); err != nil {
				output.JSON(data)
				return nil
			}

			if len(summaries) == 0 {
				fmt.Println("No positions.")
				return nil
			}

			headers := []string{"MARKET", "POSITIONS", "SHARES", "COST"}
			rows := make([][]string, len(summaries))
			for i, row := range summaries {
				shares := row.NetShares
				if shares == 0 {
					shares = row.Shares
				}
				cost := row.NetCost
				if cost == 0 {
					cost = row.Cost
				}
				rows[i] = []string{
					row.MarketCode,
					fmt.Sprintf("%d", row.PositionCount),
					fmt.Sprintf("%.2f", shares),
					fmt.Sprintf("%.2f", cost),
				}
			}
			output.Table(headers, rows)
			return nil
		}

		var positions []struct {
			MarketCode     string  `json:"market_code"`
			SubmarketLabel string  `json:"submarket_label"`
			Probability    float64 `json:"probability"`
			NetShares      float64 `json:"net_shares"`
			NetCost        float64 `json:"net_cost"`
			Shares         float64 `json:"shares"`
			Cost           float64 `json:"cost"`
			Submarket      *struct {
				Label  string `json:"label"`
				Market *struct {
					Code string `json:"code"`
				} `json:"market"`
			} `json:"submarket"`
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
			market := p.MarketCode
			if market == "" && p.Submarket != nil && p.Submarket.Market != nil {
				market = p.Submarket.Market.Code
			}
			submarket := p.SubmarketLabel
			if submarket == "" && p.Submarket != nil {
				submarket = p.Submarket.Label
			}
			shares := p.NetShares
			if shares == 0 {
				shares = p.Shares
			}
			cost := p.NetCost
			if cost == 0 {
				cost = p.Cost
			}
			rows[i] = []string{
				market,
				submarket,
				fmt.Sprintf("%.1f%%", p.Probability*100),
				fmt.Sprintf("%.2f", shares),
				fmt.Sprintf("%.2f", cost),
			}
		}
		output.Table(headers, rows)

		return nil
	},
}

func init() {
	positionsCmd.Flags().String("market", "", "Filter positions to a market code")
	positionsCmd.Flags().Bool("summary", false, "Show one aggregated row per market")

	rootCmd.AddCommand(positionsCmd)
}
