package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/spf13/cobra"
)

// restOfCurve is sent as the API's "rest_of_curve" field on trades and
// pending edits. Empty leaves the other bars alone, so the server refuses
// updates that would put a curve out of order.
var restOfCurve string

func addRestOfCurveFlag(cmd *cobra.Command) {
	cmd.Flags().String("rest-of-curve", "", `What happens to the bars you didn't send on each curve you change: "leave" (default; out-of-order updates are refused), "keep-in-order" (they move only as far as order needs) or "interpolate" (bars between yours sit on a straight line through them)`)
}

// readRestOfCurveFlag records --rest-of-curve.
func readRestOfCurveFlag(cmd *cobra.Command) error {
	value, _ := cmd.Flags().GetString("rest-of-curve")
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "_")
	switch value {
	case "", "leave":
		restOfCurve = ""
	case "keep_in_order", "interpolate":
		restOfCurve = value
	default:
		return fmt.Errorf(`--rest-of-curve must be "leave", "keep-in-order" or "interpolate"`)
	}
	return nil
}

func withRestOfCurve(body map[string]interface{}) map[string]interface{} {
	if restOfCurve != "" {
		body["rest_of_curve"] = restOfCurve
	}
	return body
}

// outOfOrderHint adds the CLI spelling of the server's suggestion.
func outOfOrderHint(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "out_of_order" && restOfCurve == "" {
		return fmt.Errorf("%w\nRerun with --rest-of-curve keep-in-order (other bars move only as far as order needs) or --rest-of-curve interpolate", err)
	}
	return err
}
