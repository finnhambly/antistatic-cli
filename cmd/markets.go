package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var marketsCmd = &cobra.Command{
	Use:   "markets",
	Short: "List open markets",
	Long: `List open markets on Antistatic Exchange.

By default, lists markets ordered by activity. Use --query to search.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")

		params := url.Values{}
		if query != "" {
			params.Set("q", query)
		} else {
			params.Set("open", "1")
		}
		if limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", limit))
		}
		if offset > 0 {
			params.Set("offset", fmt.Sprintf("%d", offset))
		}

		resp, err := client.Get("/markets", params)
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

		var markets []struct {
			Code   string `json:"code"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Type   string `json:"type"`
		}
		if err := json.Unmarshal(data, &markets); err != nil {
			output.JSON(data)
			return nil
		}

		if len(markets) == 0 {
			if query != "" {
				fmt.Printf("No markets found matching %q.\n", query)
			} else {
				fmt.Println("No open markets.")
			}
			return nil
		}

		headers := []string{"CODE", "TITLE", "STATUS", "TYPE"}
		rows := make([][]string, len(markets))
		for i, m := range markets {
			title := m.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			rows[i] = []string{m.Code, title, m.Status, m.Type}
		}
		output.Table(headers, rows)

		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search markets by keyword",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		limit, _ := cmd.Flags().GetInt("limit")

		params := url.Values{"q": {query}}
		if limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", limit))
		}

		resp, err := client.Get("/markets", params)
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

		var markets []struct {
			Code  string `json:"code"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(data, &markets); err != nil {
			output.JSON(data)
			return nil
		}

		if len(markets) == 0 {
			fmt.Printf("No markets found matching %q.\n", query)
			return nil
		}

		headers := []string{"CODE", "TITLE"}
		rows := make([][]string, len(markets))
		for i, m := range markets {
			title := m.Title
			if len(title) > 70 {
				title = title[:67] + "..."
			}
			rows[i] = []string{m.Code, title}
		}
		output.Table(headers, rows)

		return nil
	},
}

func init() {
	marketsCmd.Flags().StringP("query", "q", "", "Search query")
	marketsCmd.Flags().IntP("limit", "l", 0, "Maximum number of results")
	marketsCmd.Flags().IntP("offset", "o", 0, "Number of results to skip")

	searchCmd.Flags().IntP("limit", "l", 0, "Maximum number of results")

	rootCmd.AddCommand(marketsCmd)
	rootCmd.AddCommand(searchCmd)
}
