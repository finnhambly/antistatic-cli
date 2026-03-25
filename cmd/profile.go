package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

type profileSummary struct {
	UserID                   int     `json:"user_id"`
	ForecastingPoints        float64 `json:"forecasting_points"`
	PrivateForecastingPoints float64 `json:"private_forecasting_points"`
	TippingProfit            float64 `json:"tipping_profit"`
}

type profileHistoryRow struct {
	Code                    string  `json:"code"`
	Title                   string  `json:"title"`
	Status                  string  `json:"status"`
	Visibility              string  `json:"visibility"`
	PointsStaked            float64 `json:"pointsStaked"`
	PointsWonLost           float64 `json:"pointsWonLost"`
	CommunityExpectedPoints float64 `json:"communityExpectedPoints"`
	UserExpectedPoints      float64 `json:"userExpectedPoints"`
	Liquidity               float64 `json:"liquidity"`
	Next24hLiquidityChange  float64 `json:"next24hLiquidityChange"`
	LastPositionAt          string  `json:"lastPositionAt"`
}

type profileHistoryPayload struct {
	UserID     int                 `json:"user_id"`
	Rows       []profileHistoryRow `json:"rows"`
	Pagination struct {
		Limit      int `json:"limit"`
		Offset     int `json:"offset"`
		TotalCount int `json:"total_count"`
	} `json:"pagination"`
}

type profileLiquidityPayload struct {
	UserID        int                 `json:"user_id"`
	AsOf          string              `json:"as_of"`
	HorizonHours  int                 `json:"horizon_hours"`
	TotalCount    int                 `json:"total_count"`
	DecayingCount int                 `json:"decaying_count"`
	Markets       []profileHistoryRow `json:"markets"`
}

var profileCmd = &cobra.Command{
	Use:     "profile",
	Aliases: []string{"me"},
	Short:   "View profile analytics",
	Long: `View account-level analytics derived from your forecast history.

Use subcommands:
  summary         overall points and tipping totals
  history         market-level forecast history table
  liquidity-decay markets with fastest 24h liquidity decline`,
}

var profileSummaryCmd = &cobra.Command{
	Use:     "summary",
	Aliases: []string{"totals"},
	Short:   "Show overall points won/lost summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		resp, err := client.Get("/profile/summary", nil)
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

		var summary profileSummary
		if err := json.Unmarshal(data, &summary); err != nil {
			output.JSON(data)
			return nil
		}

		output.KeyValue([][2]string{
			{"User ID", strconv.Itoa(summary.UserID)},
			{"Forecasting points", signed(summary.ForecastingPoints)},
			{"Private-market points", signed(summary.PrivateForecastingPoints)},
			{"Tipping points", signed(summary.TippingProfit)},
		})
		return nil
	},
}

var profileHistoryCmd = &cobra.Command{
	Use:     "history",
	Aliases: []string{"forecast-history"},
	Short:   "Show forecast history details",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")

		params := url.Values{}
		if limit > 0 {
			params.Set("limit", strconv.Itoa(limit))
		}
		if offset > 0 {
			params.Set("offset", strconv.Itoa(offset))
		}

		resp, err := client.Get("/profile/forecast-history", params)
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

		var payload profileHistoryPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			output.JSON(data)
			return nil
		}

		if len(payload.Rows) == 0 {
			fmt.Println("No forecast history rows.")
			return nil
		}

		headers := []string{
			"CODE",
			"TITLE",
			"VIS",
			"STAKED",
			"WON/LOST",
			"COMM EXP",
			"YOUR EXP",
			"LIQ",
			"24H ΔLIQ",
			"LAST POSITION",
		}
		rows := make([][]string, len(payload.Rows))
		for i, r := range payload.Rows {
			title := r.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}

			rows[i] = []string{
				r.Code,
				title,
				r.Visibility,
				fmt.Sprintf("%.2f", r.PointsStaked),
				signed(r.PointsWonLost),
				signed(r.CommunityExpectedPoints),
				signed(r.UserExpectedPoints),
				fmt.Sprintf("%.2f", r.Liquidity),
				signed(r.Next24hLiquidityChange),
				compactTime(r.LastPositionAt),
			}
		}
		output.Table(headers, rows)
		fmt.Printf(
			"Showing %d rows (offset %d, total %d).\n",
			len(payload.Rows),
			payload.Pagination.Offset,
			payload.Pagination.TotalCount,
		)
		return nil
	},
}

var profileLiquidityDecayCmd = &cobra.Command{
	Use:     "liquidity-decay",
	Aliases: []string{"decay"},
	Short:   "Show markets with fastest liquidity decay",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		limit, _ := cmd.Flags().GetInt("limit")

		params := url.Values{}
		if limit > 0 {
			params.Set("limit", strconv.Itoa(limit))
		}

		resp, err := client.Get("/profile/liquidity-decay", params)
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

		var payload profileLiquidityPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			output.JSON(data)
			return nil
		}

		if len(payload.Markets) == 0 {
			fmt.Println("No markets with positions.")
			return nil
		}

		headers := []string{"CODE", "TITLE", "VIS", "24H ΔLIQ", "LIQ", "STAKED", "LAST POSITION"}
		rows := make([][]string, len(payload.Markets))
		for i, r := range payload.Markets {
			title := r.Title
			if len(title) > 46 {
				title = title[:43] + "..."
			}

			rows[i] = []string{
				r.Code,
				title,
				r.Visibility,
				signed(r.Next24hLiquidityChange),
				fmt.Sprintf("%.2f", r.Liquidity),
				fmt.Sprintf("%.2f", r.PointsStaked),
				compactTime(r.LastPositionAt),
			}
		}
		output.Table(headers, rows)
		fmt.Printf(
			"As of %s. Showing %d of %d markets (%d decaying).\n",
			compactTime(payload.AsOf),
			len(payload.Markets),
			payload.TotalCount,
			payload.DecayingCount,
		)
		return nil
	},
}

func signed(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f", v)
	}
	if v < 0 {
		return fmt.Sprintf("%.2f", v)
	}
	return "0.00"
}

func compactTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}

	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	return value
}

func init() {
	profileHistoryCmd.Flags().IntP("limit", "l", 50, "Maximum number of rows")
	profileHistoryCmd.Flags().IntP("offset", "o", 0, "Rows to skip")
	profileLiquidityDecayCmd.Flags().IntP("limit", "l", 50, "Maximum number of markets")

	profileCmd.AddCommand(profileSummaryCmd)
	profileCmd.AddCommand(profileHistoryCmd)
	profileCmd.AddCommand(profileLiquidityDecayCmd)
	rootCmd.AddCommand(profileCmd)
}
