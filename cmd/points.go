package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var pointsCmd = &cobra.Command{
	Use:   "points <code>",
	Short: "Show P&L scenarios for your positions",
	Long: `Show what you would gain or lose under every possible resolution
outcome for a market.

Use --at (or --scenario) to query a specific resolution point.
This command reports scenario P&L, not an account balance.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		at, _ := cmd.Flags().GetString("at")
		scenario, _ := cmd.Flags().GetString("scenario")
		if at != "" && scenario != "" {
			return fmt.Errorf("use either --at or --scenario")
		}
		if at == "" {
			at = scenario
		}

		params := url.Values{}
		if at != "" {
			params.Set("at", at)
		}

		resp, err := client.Get("/markets/"+code+"/points", params)
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

		// Try to render scenarios table
		var scenarios []struct {
			Label  string  `json:"label"`
			Points float64 `json:"points"`
		}
		if err := json.Unmarshal(data, &scenarios); err != nil {
			// Might be a map/object — try that
			var result map[string]interface{}
			if err2 := json.Unmarshal(data, &result); err2 == nil {
				// Check for nested scenarios
				if scenariosRaw, ok := result["scenarios"]; ok {
					scenariosJSON, _ := json.Marshal(scenariosRaw)
					json.Unmarshal(scenariosJSON, &scenarios)
				}
			}
		}

		if len(scenarios) > 0 {
			headers := []string{"OUTCOME", "POINTS"}
			rows := make([][]string, len(scenarios))
			for i, s := range scenarios {
				sign := ""
				if s.Points > 0 {
					sign = "+"
				}
				rows[i] = []string{s.Label, fmt.Sprintf("%s%.2f", sign, s.Points)}
			}
			output.Table(headers, rows)
		} else {
			output.JSON(data)
		}

		return nil
	},
}

func init() {
	pointsCmd.Flags().String("at", "", "Query specific resolution point")
	pointsCmd.Flags().String("scenario", "", "Alias of --at (scenario point for count/date markets)")
	rootCmd.AddCommand(pointsCmd)
}
