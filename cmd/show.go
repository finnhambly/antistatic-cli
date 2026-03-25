package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <code>",
	Short: "Show market details",
	Long: `Show details for a specific market by its code.

Use --fuzzy to enable fuzzy code resolution (prefix matching).
Resolution criteria are shown by default with a safe length cap.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code := args[0]
		fuzzy, _ := cmd.Flags().GetBool("fuzzy")
		maxResolutionChars, _ := cmd.Flags().GetInt("max-resolution-chars")
		maxBackgroundChars, _ := cmd.Flags().GetInt("max-background-chars")

		params := url.Values{}
		if fuzzy {
			params.Set("fuzzy", "true")
		}

		resp, err := client.Get("/markets/"+code, params)
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

		var market struct {
			Code               string `json:"code"`
			Title              string `json:"title"`
			Status             string `json:"status"`
			Type               string `json:"type"`
			Description        string `json:"description"`
			SubmarketCount     int    `json:"submarket_count"`
			ResolutionCriteria string `json:"resolution_criteria"`
			BackgroundInfoHTML string `json:"background_info_html"`
		}
		if err := json.Unmarshal(data, &market); err != nil {
			output.JSON(data)
			return nil
		}

		pairs := [][2]string{
			{"Code", market.Code},
			{"Title", market.Title},
			{"Status", market.Status},
			{"Type", market.Type},
		}
		if market.SubmarketCount > 0 {
			pairs = append(pairs, [2]string{"Submarkets", fmt.Sprintf("%d", market.SubmarketCount)})
		}
		if market.Description != "" {
			desc := market.Description
			if len(desc) > 200 {
				desc = desc[:197] + "..."
			}
			pairs = append(pairs, [2]string{"Description", desc})
		}
		if market.ResolutionCriteria != "" {
			pairs = append(pairs, [2]string{
				"Resolution",
				trimText(market.ResolutionCriteria, maxResolutionChars),
			})
		}
		if market.BackgroundInfoHTML != "" {
			background := htmlToText(market.BackgroundInfoHTML)
			pairs = append(pairs, [2]string{
				"Background",
				trimText(background, maxBackgroundChars),
			})
		}
		output.KeyValue(pairs)

		return nil
	},
}

func init() {
	showCmd.Flags().Bool("fuzzy", false, "Enable fuzzy code matching (prefix search)")
	showCmd.Flags().Int("max-resolution-chars", 800, "Max characters to print for resolution criteria")
	showCmd.Flags().Int("max-background-chars", 600, "Max characters to print for background info")
	rootCmd.AddCommand(showCmd)
}

func trimText(text string, maxChars int) string {
	clean := strings.TrimSpace(text)
	if maxChars > 0 && len(clean) > maxChars {
		return clean[:maxChars] + "..."
	}
	return clean
}

func htmlToText(html string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	text := re.ReplaceAllString(html, " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}
