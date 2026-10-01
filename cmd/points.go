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
	Short: "Show points won or lost under each outcome",
	Long: `Show the points you would win or lose under every possible resolution
outcome for a market, one table per ladder. Outcomes already ruled out by
resolved bars are marked.

Use --at (or --scenario) to query a specific resolution point.
This reports scenario points, not an account balance.`,
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

		var result struct {
			Scenarios []pointsScenario `json:"scenarios"`
			Groups    []struct {
				Group     string           `json:"group"`
				Ladder    string           `json:"ladder"`
				Scenarios []pointsScenario `json:"scenarios"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			output.JSON(data)
			return nil
		}

		switch {
		case len(result.Groups) > 0:
			for _, group := range result.Groups {
				title := group.Ladder
				if title == "" {
					title = group.Group
				}
				fmt.Printf("\n%s\n", title)
				printPointsScenarios(group.Scenarios)
			}
		case len(result.Scenarios) > 0:
			printPointsScenarios(result.Scenarios)
		default:
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

type pointsScenario struct {
	Resolution string  `json:"resolution"`
	Points     float64 `json:"points_won_lost"`
	Possible   *bool   `json:"possible"`
}

func printPointsScenarios(scenarios []pointsScenario) {
	rows := make([][]string, len(scenarios))
	for i, s := range scenarios {
		sign := ""
		if s.Points > 0 {
			sign = "+"
		}
		note := ""
		if s.Possible != nil && !*s.Possible {
			note = "ruled out"
		}
		rows[i] = []string{s.Resolution, fmt.Sprintf("%s%.2f", sign, s.Points), note}
	}
	output.Table([]string{"OUTCOME", "POINTS", ""}, rows)
}
