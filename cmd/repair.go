package cmd

import (
	"errors"
	"fmt"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/spf13/cobra"
)

// repairMode is sent as the API's "repair" field on trades and pending edits.
// Empty means the server rejects bars that break a ladder's order.
var repairMode string

func addLadderFlags(cmd *cobra.Command) {
	cmd.Flags().String("repair", "", `How the server handles bars that break a ladder's order: omit to reject them, or "fill" to keep your bars exactly and move the other bars in those ladders by the least amount`)
	cmd.Flags().Bool("interpolate", false, "Interpolate between the bars you set before sending (client-side shaping)")
}

// readLadderFlags records --repair and reports whether to interpolate client-side.
func readLadderFlags(cmd *cobra.Command) (bool, error) {
	repair, _ := cmd.Flags().GetString("repair")
	if repair != "" && repair != "fill" {
		return false, fmt.Errorf(`--repair must be "fill" or omitted`)
	}
	repairMode = repair
	interpolate, _ := cmd.Flags().GetBool("interpolate")
	return interpolate, nil
}

func withRepair(body map[string]interface{}) map[string]interface{} {
	if repairMode != "" {
		body["repair"] = repairMode
	}
	return body
}

// ladderHint adds the CLI spelling of the server's repair suggestion.
func ladderHint(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "ladder_violation" && repairMode == "" {
		return fmt.Errorf("%w\nRerun with --repair fill to keep your bars and move the others by the least amount", err)
	}
	return err
}
