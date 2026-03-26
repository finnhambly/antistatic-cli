package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var forecastCmd = &cobra.Command{
	Use:   "forecast <code>",
	Short: "Get forecast data for a market",
	Long: `Retrieve community aggregate forecast data for a market.

Use --for to query a specific point (date or threshold).
Use --group to filter by projection group (e.g. "2026-08").
Use --include to control detail level (summary, liquidity, full).
Use --ascii --summary for compact one-line group summaries.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code := args[0]
		forParam, _ := cmd.Flags().GetString("for")
		group, _ := cmd.Flags().GetString("group")
		year, _ := cmd.Flags().GetString("year")
		include, _ := cmd.Flags().GetString("include")
		curve, _ := cmd.Flags().GetBool("curve")
		limit, _ := cmd.Flags().GetInt("limit")
		requireFull, _ := cmd.Flags().GetBool("require-full")
		includeIDs, _ := cmd.Flags().GetBool("include-ids")
		ascii, _ := cmd.Flags().GetBool("ascii")
		asciiSummary, _ := cmd.Flags().GetBool("summary")
		asciiWidth, _ := cmd.Flags().GetInt("ascii-width")
		asciiMaxGroups, _ := cmd.Flags().GetInt("ascii-max-groups")
		asciiMaxPoints, _ := cmd.Flags().GetInt("ascii-max-points")
		machineOutput := jsonOutput || !output.IsTTY()

		if ascii && jsonOutput {
			return fmt.Errorf("--ascii cannot be combined with --json")
		}
		if ascii && forParam != "" && !curve {
			return fmt.Errorf("--ascii requires grouped or curve forecast data; omit --for or add --curve")
		}
		if includeIDs && ascii {
			return fmt.Errorf("--include-ids cannot be combined with --ascii")
		}
		if asciiSummary && !ascii {
			return fmt.Errorf("--summary is only supported with --ascii")
		}

		// Agent/machine consumers usually need a stable full response shape.
		if machineOutput && !cmd.Flags().Changed("limit") {
			requireFull = true
		}

		params := url.Values{}
		if forParam != "" {
			params.Set("for", forParam)
		}
		if group != "" {
			params.Set("group", group)
		}
		if year != "" {
			params.Set("year", year)
		}
		if include != "" {
			params.Set("include", include)
		}
		if curve {
			params.Set("curve", "true")
		}
		if includeIDs {
			include = "full"
			params.Set("include", include)
			requireFull = true
		}
		if ascii && include == "" {
			include = "full"
			params.Set("include", include)
		}
		if requireFull {
			if include == "" || include == "summary" {
				include = "full"
				params.Set("include", include)
			}
			params.Set("mode", "full")
		}
		if limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", limit))
		} else if ascii || requireFull {
			// Fetch all points for terminal plotting/sanity checks.
			params.Set("limit", "0")
		}

		resp, err := client.Get("/markets/"+code+"/forecast", params)
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

		if ascii {
			return renderASCIIForecast(data, asciiRenderOptions{
				Width:     asciiWidth,
				MaxGroups: asciiMaxGroups,
				MaxPoints: asciiMaxPoints,
				Summary:   asciiSummary,
			})
		}

		// Try to render a human-friendly summary
		var forecast struct {
			Market struct {
				Code  string `json:"code"`
				Title string `json:"title"`
				Type  string `json:"type"`
			} `json:"market"`
			ResponseMode   string                     `json:"response_mode"`
			Interpretation string                     `json:"interpretation"`
			Forecast       map[string]json.RawMessage `json:"forecast"`
			Submarkets     []map[string]interface{}   `json:"submarkets"`
			Matched        map[string]interface{}     `json:"matched"`
			Groups         []map[string]interface{}   `json:"groups"`
			Hint           string                     `json:"hint"`
		}
		if err := json.Unmarshal(data, &forecast); err != nil {
			output.JSON(data)
			return nil
		}

		// Print market header
		if forecast.Market.Title != "" {
			fmt.Printf("%s (%s)\n\n", forecast.Market.Title, forecast.Market.Code)
		}

		// Print interpretation if present
		if forecast.Interpretation != "" {
			fmt.Printf("  %s\n\n", forecast.Interpretation)
		}

		// If single match, show it
		if forecast.Matched != nil {
			if label, ok := forecast.Matched["label"].(string); ok {
				prob := ""
				if p, ok := forecast.Matched["community_probability"].(float64); ok {
					prob = fmt.Sprintf("%.1f%%", p*100)
				}
				fmt.Printf("  Matched: %s → %s\n", label, prob)
			}
			return nil
		}

		// If submarkets list (curve mode or full include)
		if len(forecast.Submarkets) > 0 {
			headers := []string{"LABEL", "PROBABILITY"}
			rows := make([][]string, 0, len(forecast.Submarkets))
			for _, sm := range forecast.Submarkets {
				label := fmt.Sprintf("%v", sm["label"])
				prob := ""
				if p, ok := sm["community_probability"].(float64); ok {
					prob = fmt.Sprintf("%.1f%%", p*100)
				}
				rows = append(rows, []string{label, prob})
			}
			output.Table(headers, rows)
			return nil
		}

		// If grouped forecast (compact index)
		if len(forecast.Forecast) > 0 {
			headers := []string{"GROUP", "SUBMARKETS", "DETAILS"}
			rows := make([][]string, 0, len(forecast.Forecast))
			for groupName, raw := range forecast.Forecast {
				var group struct {
					SubmarketCount int     `json:"submarket_count"`
					MeanProb       float64 `json:"mean_probability"`
				}
				if json.Unmarshal(raw, &group) == nil && group.SubmarketCount > 0 {
					rows = append(rows, []string{
						groupName,
						fmt.Sprintf("%d", group.SubmarketCount),
						fmt.Sprintf("mean %.1f%%", group.MeanProb*100),
					})
				} else {
					rows = append(rows, []string{groupName, "-", "-"})
				}
			}
			output.Table(headers, rows)
			return nil
		}

		if forecast.ResponseMode == "summary_index" || len(forecast.Groups) > 0 {
			headers := []string{"GROUP", "SUBMARKETS", "RANGE"}
			rows := make([][]string, 0, len(forecast.Groups))
			for _, row := range forecast.Groups {
				group := fmt.Sprintf("%v", row["group"])
				submarketCount := fmt.Sprintf("%v", row["submarkets"])
				rangeText := "-"
				if rawRange, ok := row["prob_range"].([]interface{}); ok && len(rawRange) == 2 {
					low, lowOK := rawRange[0].(float64)
					high, highOK := rawRange[1].(float64)
					if lowOK && highOK {
						rangeText = fmt.Sprintf("%.1f%%..%.1f%%", low*100, high*100)
					}
				}
				rows = append(rows, []string{group, submarketCount, rangeText})
			}
			if len(rows) > 0 {
				output.Table(headers, rows)
			}
			if forecast.Hint != "" {
				fmt.Printf("\n%s\n", forecast.Hint)
			}
			fmt.Println("Tip: re-run with --require-full (or --include-ids) for stable row-level forecast data.")
			return nil
		}

		// Fallback to JSON
		output.JSON(data)
		return nil
	},
}

func init() {
	forecastCmd.Flags().String("for", "", "Query specific point (date or threshold value)")
	forecastCmd.Flags().String("group", "", "Filter by projection group (e.g. 2026-08)")
	forecastCmd.Flags().String("year", "", "Filter by year")
	forecastCmd.Flags().String("include", "", "Detail level: summary, liquidity, or full")
	forecastCmd.Flags().Bool("curve", false, "Return all submarkets up to the queried point")
	forecastCmd.Flags().IntP("limit", "l", 0, "Maximum submarkets per group")
	forecastCmd.Flags().Bool("require-full", false, "Require full grouped forecast rows (mode=full)")
	forecastCmd.Flags().Bool("include-ids", false, "Force full forecast rows with submarket IDs for agent trading flows")
	forecastCmd.Flags().Bool("ascii", false, "Render ASCII bars with monotonicity checks")
	forecastCmd.Flags().Bool("summary", false, "With --ascii, show compact one-line summaries per group")
	forecastCmd.Flags().Int("ascii-width", 32, "ASCII chart width in characters")
	forecastCmd.Flags().Int("ascii-max-groups", 6, "Maximum groups to render in ASCII mode")
	forecastCmd.Flags().Int("ascii-max-points", 60, "Maximum points per group to print in ASCII mode")

	rootCmd.AddCommand(forecastCmd)
}
