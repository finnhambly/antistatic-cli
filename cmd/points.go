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
outcome for a market, one table per curve. Outcomes already ruled out by
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
		showAll, _ := cmd.Flags().GetBool("all")
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
				Curve     string           `json:"curve"`
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
				title := group.Group
				if title == "" {
					title = group.Curve
				}
				fmt.Printf("\n%s\n", title)
				printPointsScenarios(group.Scenarios, showAll)
			}
		case len(result.Scenarios) > 0:
			printPointsScenarios(result.Scenarios, showAll)
		default:
			output.JSON(data)
		}

		return nil
	},
}

func init() {
	pointsCmd.Flags().String("at", "", "Query specific resolution point")
	pointsCmd.Flags().String("scenario", "", "Alias of --at (scenario point for count/date markets)")
	pointsCmd.Flags().Bool("all", false, "Also list outcomes already ruled out by resolved bars")
	rootCmd.AddCommand(pointsCmd)
}

type pointsScenario struct {
	Resolution string  `json:"resolution"`
	Points     float64 `json:"points_won_lost"`
	Possible   *bool   `json:"possible"`
}

func printPointsScenarios(scenarios []pointsScenario, showAll bool) {
	rows := make([][]string, 0, len(scenarios))
	ruledOut := 0
	for _, s := range scenarios {
		impossible := s.Possible != nil && !*s.Possible
		if impossible && !showAll {
			ruledOut++
			continue
		}
		sign := ""
		if s.Points > 0 {
			sign = "+"
		}
		note := ""
		if impossible {
			note = "ruled out"
		}
		rows = append(rows, []string{s.Resolution, fmt.Sprintf("%s%.2f", sign, s.Points), note})
	}
	output.Table([]string{"OUTCOME", "POINTS", ""}, rows)
	if ruledOut > 0 {
		fmt.Printf("(%d outcomes already ruled out by resolved bars; --all lists them)\n", ruledOut)
	}
}
