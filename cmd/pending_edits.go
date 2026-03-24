package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var pendingEditsCmd = &cobra.Command{
	Use:   "pending-edits <code>",
	Short: "View or manage pending probability edits",
	Long: `View, update, or clear pending probability edits for a market.

Pending edits are unsaved position changes that persist across sessions
and devices. They are not yet submitted as trades.

Without flags, shows current pending edits.
Use --clear to delete all pending edits.
Use --updates to set or merge edits (pipe JSON or use the flag).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		clear, _ := cmd.Flags().GetBool("clear")
		updatesJSON, _ := cmd.Flags().GetString("updates")
		mode, _ := cmd.Flags().GetString("mode")

		if clear {
			return clearPendingEdits(code)
		}

		if updatesJSON != "" {
			return updatePendingEdits(code, updatesJSON, mode)
		}

		// Check stdin
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			stdinData, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}
			return updatePendingEdits(code, string(stdinData), mode)
		}

		// Default: show pending edits
		return showPendingEdits(code)
	},
}

func showPendingEdits(code string) error {
	resp, err := client.Get("/markets/"+code+"/pending-edits", nil)
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

	var edits map[string]interface{}
	if err := json.Unmarshal(data, &edits); err != nil || len(edits) == 0 {
		fmt.Println("No pending edits.")
		return nil
	}

	headers := []string{"SUBMARKET ID", "PROBABILITY", "FIXED"}
	rows := make([][]string, 0)
	for id, val := range edits {
		if entry, ok := val.(map[string]interface{}); ok {
			prob := "-"
			if p, ok := entry["probability"].(float64); ok {
				prob = fmt.Sprintf("%.1f%%", p*100)
			}
			fixed := ""
			if f, ok := entry["is_fixed"].(bool); ok && f {
				fixed = "yes"
			}
			rows = append(rows, []string{id, prob, fixed})
		}
	}
	output.Table(headers, rows)
	return nil
}

func updatePendingEdits(code, updatesRaw, mode string) error {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(updatesRaw), &body); err != nil {
		// Try as array of updates
		var updates []interface{}
		if err2 := json.Unmarshal([]byte(updatesRaw), &updates); err2 != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		body = map[string]interface{}{"updates": updates}
	}

	if mode != "" {
		body["mode"] = mode
	}

	resp, err := client.Put("/markets/"+code+"/pending-edits", body)
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

	fmt.Println("Pending edits updated.")
	return nil
}

func clearPendingEdits(code string) error {
	_, err := client.Delete("/markets/" + code + "/pending-edits")
	if err != nil {
		return err
	}

	if !jsonOutput && output.IsTTY() {
		fmt.Println("Pending edits cleared.")
	}
	return nil
}

func init() {
	pendingEditsCmd.Flags().Bool("clear", false, "Clear all pending edits")
	pendingEditsCmd.Flags().String("updates", "", "Probability updates as JSON")
	pendingEditsCmd.Flags().String("mode", "merge", "Update mode: merge (default) or replace")

	rootCmd.AddCommand(pendingEditsCmd)
}
