package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

func newMarketExtendCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "extend <code>",
		Short: "Extend a how-much market's thresholds or periods",
		Long: `Extend a how-much market you own: a new top or bottom threshold (the ladder
continued at its spacing) and/or periods up to YYYY-MM (at its cadence).
--anchors prices the new bars: [{period, quantiles: {10, 50, 90}}].

  antistatic market extend CODE --to-threshold 70 --anchors anchors.json --preview`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			body := jsonMap{}
			thresholds := jsonMap{}
			for _, f := range []struct{ flag, key string }{{"to-threshold", "to"}, {"from-threshold", "from"}} {
				if cmd.Flags().Changed(f.flag) {
					v, _ := cmd.Flags().GetFloat64(f.flag)
					thresholds[f.key] = v
				}
			}
			if len(thresholds) > 0 {
				body["thresholds"] = thresholds
			}
			putString(cmd, body, "to-period", "to")
			if src, _ := cmd.Flags().GetString("anchors"); src != "" {
				anchors, err := readJSONArray(src, "--anchors")
				if err != nil {
					return err
				}
				body["anchors"] = anchors
			}
			if len(body) == 0 {
				return errors.New("give --to-threshold, --from-threshold or --to-period")
			}
			preview, _ := cmd.Flags().GetBool("preview")
			if preview {
				body["preview"] = true
			}
			resp, err := client.Post(api.Path("/markets/{code}/extend", args[0]), body)
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
			var done marketExtension
			if err := json.Unmarshal(data, &done); err != nil {
				output.JSON(data)
				return nil
			}
			verb := "Added"
			if done.Preview {
				verb = "Would add"
			}
			fmt.Printf("%s %d bars to %s", verb, done.Added, args[0])
			if done.Settled > 0 {
				fmt.Printf(", %d settled from recorded values", done.Settled)
			}
			fmt.Println(".")
			if len(done.NeedsValue) > 0 {
				fmt.Println("Needs a resolved value: periods ending " + strings.Join(done.NeedsValue, ", "))
			}
			rows := [][]string{}
			for _, b := range done.Bars {
				p := "—"
				if b.Probability != nil {
					p = strconv.FormatFloat(*b.Probability*100, 'f', 1, 64) + "%"
				}
				rows = append(rows, []string{formatSandboxDate(b.EndDate), "≥" + strconv.FormatFloat(b.Threshold, 'f', -1, 64), p})
			}
			if len(rows) > 0 {
				fmt.Println()
				output.Table([]string{"PERIOD END", "BAR", "P"}, rows)
			}
			return nil
		},
	}
	f := command.Flags()
	f.Float64("to-threshold", 0, "New top threshold")
	f.Float64("from-threshold", 0, "New bottom threshold")
	f.String("to-period", "", "Last new period, YYYY-MM")
	f.String("anchors", "", "Anchors as a JSON array, from FILE or -")
	f.Bool("preview", false, "Show the bars it would add without changing anything")
	return command
}

// marketExtension is POST /markets/{code}/extend.
type marketExtension struct {
	Preview    bool     `json:"preview"`
	Added      int      `json:"added"`
	Settled    int      `json:"settled"`
	NeedsValue []string `json:"needs_value"`
	Bars       []struct {
		Threshold   float64  `json:"threshold"`
		EndDate     string   `json:"end_date"`
		Probability *float64 `json:"probability"`
	} `json:"bars"`
}
