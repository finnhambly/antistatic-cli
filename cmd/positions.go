package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
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
Use --summary for one aggregated row per market.
Use --group-summary (with a market code) for one row per projection group.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		marketFlag, _ := cmd.Flags().GetString("market")
		summary, _ := cmd.Flags().GetBool("summary")
		groupSummary, _ := cmd.Flags().GetBool("group-summary")
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		marketCode := strings.TrimSpace(marketFlag)

		if len(args) == 1 {
			if marketCode != "" {
				return fmt.Errorf("provide market either as positional arg or --market, not both")
			}
			marketCode = strings.TrimSpace(args[0])
		}
		if summary && groupSummary {
			return fmt.Errorf("use either --summary or --group-summary")
		}
		if groupSummary && marketCode == "" {
			return fmt.Errorf("--group-summary requires a market code (positional arg or --market)")
		}

		path := "/positions"
		params := url.Values{}
		if limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", limit))
		}
		if offset > 0 {
			params.Set("offset", fmt.Sprintf("%d", offset))
		}

		// Preserve positional detail mode separately from cross-market listing.
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
			if groupSummary {
				summaries, err := buildGroupSummaries(marketCode, data)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(summaries)
				output.JSON(raw)
				return nil
			}
			output.JSON(data)
			return nil
		}

		if groupSummary {
			summaries, err := buildGroupSummaries(marketCode, data)
			if err != nil {
				return err
			}
			if len(summaries) == 0 {
				fmt.Println("No positions.")
				return nil
			}
			headers := []string{"GROUP", "POSITIONS", "SHARES", "COST"}
			rows := make([][]string, len(summaries))
			for i, row := range summaries {
				rows[i] = []string{
					row.Group,
					fmt.Sprintf("%d", row.PositionCount),
					fmt.Sprintf("%.2f", row.NetShares),
					fmt.Sprintf("%.2f", row.NetCost),
				}
			}
			output.Table(headers, rows)
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
			MarketCode       string  `json:"market_code"`
			SubmarketLabel   string  `json:"submarket_label"`
			Probability      float64 `json:"probability"`
			NetShares        float64 `json:"net_shares"`
			NetCost          float64 `json:"net_cost"`
			Shares           float64 `json:"shares"`
			Cost             float64 `json:"cost"`
			Submarket        string  `json:"submarket"`
			SubmarketDetails *struct {
				Label  string `json:"label"`
				Market *struct {
					Code string `json:"code"`
				} `json:"market"`
			} `json:"submarket_details"`
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
			if market == "" && p.SubmarketDetails != nil && p.SubmarketDetails.Market != nil {
				market = p.SubmarketDetails.Market.Code
			}
			submarket := p.SubmarketLabel
			if submarket == "" && p.SubmarketDetails != nil {
				submarket = p.SubmarketDetails.Label
			}
			if submarket == "" {
				submarket = p.Submarket
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
	positionsCmd.Flags().Bool("group-summary", false, "With a market code, show one aggregated row per projection group")
	positionsCmd.Flags().IntP("limit", "l", 0, "Maximum number of rows")
	positionsCmd.Flags().IntP("offset", "o", 0, "Number of rows to skip")

	rootCmd.AddCommand(positionsCmd)
}

type groupedPositionSummary struct {
	Group         string  `json:"group"`
	PositionCount int     `json:"position_count"`
	NetShares     float64 `json:"net_shares"`
	NetCost       float64 `json:"net_cost"`
}

func buildGroupSummaries(code string, positionsData json.RawMessage) ([]groupedPositionSummary, error) {
	var positions []struct {
		Submarket string   `json:"submarket"`
		NetShares *float64 `json:"net_shares"`
		NetCost   *float64 `json:"net_cost"`
		Shares    *float64 `json:"shares"`
		Cost      *float64 `json:"cost"`
	}
	if err := json.Unmarshal(positionsData, &positions); err != nil {
		return nil, fmt.Errorf("parsing positions for group summary: %w", err)
	}
	if len(positions) == 0 {
		return nil, nil
	}

	groupBySubmarket, err := fetchSubmarketGroups(code)
	if err != nil {
		return nil, err
	}

	acc := make(map[string]groupedPositionSummary)
	for _, position := range positions {
		group := groupBySubmarket[position.Submarket]
		if group == "" {
			group = "ungrouped"
		}
		row := acc[group]
		row.Group = group
		row.PositionCount++

		shares := 0.0
		if position.NetShares != nil {
			shares = *position.NetShares
		} else if position.Shares != nil {
			shares = *position.Shares
		}

		cost := 0.0
		if position.NetCost != nil {
			cost = *position.NetCost
		} else if position.Cost != nil {
			cost = *position.Cost
		}

		row.NetShares += shares
		row.NetCost += cost
		acc[group] = row
	}

	out := make([]groupedPositionSummary, 0, len(acc))
	for _, row := range acc {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Group < out[j].Group
	})
	return out, nil
}

func fetchSubmarketGroups(code string) (map[string]string, error) {
	data, err := fetchFullForecastData(code)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Submarkets []struct {
			Submarket       string `json:"submarket"`
			Group           string `json:"group"`
			ProjectionGroup string `json:"projection_group"`
		} `json:"submarkets"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing forecast groups for positions summary: %w", err)
	}

	out := make(map[string]string, len(payload.Submarkets))
	for _, submarket := range payload.Submarkets {
		group := submarket.ProjectionGroup
		if group == "" {
			group = submarket.Group
		}
		out[submarket.Submarket] = group
	}
	return out, nil
}
