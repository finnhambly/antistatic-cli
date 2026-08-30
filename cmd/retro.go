package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// retroRun is the learner-facing view of a retrocasting run. The server reports
// the next checkpoint as a bare date and nothing else about it: a title like
// "phase 1 immunogenicity readout" would tell a learner what is coming.
type retroRun struct {
	RunID      int    `json:"run_id"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
	Curriculum struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"curriculum"`
	Checkpoint struct {
		Key        string `json:"key"`
		Sequence   int    `json:"sequence"`
		OccurredAt string `json:"occurred_at"`
		Title      string `json:"title"`
	} `json:"checkpoint"`
	NextOccurredAt string `json:"next_occurred_at"`
	Complete       bool   `json:"complete"`
	Blockers       []struct {
		Key   string `json:"key"`
		Title string `json:"title"`
	} `json:"blockers"`
	Questions []struct {
		Key          string `json:"key"`
		Title        string `json:"title"`
		MarketCode   string `json:"market_code"`
		Open         bool   `json:"open"`
		Resolved     bool   `json:"resolved"`
		Submitted    string `json:"submitted"`
		Contaminated bool   `json:"contaminated"`
	} `json:"questions"`
}

var retroCmd = &cobra.Command{
	Use:   "retro",
	Short: "Retrocasting: forecast the past under a simulated clock",
	Long: `Retrocasting runs question sets from the past under a simulated clock.

"antistatic retro status" shows where your runs stand. "antistatic retro advance"
moves one forward in time.

There is no command here for submitting a forecast, and that is deliberate: an
agent may move you through time, but the forecasting is yours to do. Use the web
UI, or "antistatic trade", for that.`,
}

var retroStatusCmd = &cobra.Command{
	Use:   "status [slug]",
	Short: "Show where your retrocasting runs stand",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		resp, err := client.Get("/retrocasting/runs", nil)
		if err != nil {
			return err
		}

		var runs []retroRun
		if err := resp.DataInto(&runs); err != nil {
			return err
		}

		if len(args) == 1 {
			slug := strings.TrimSpace(args[0])
			filtered := runs[:0:0]
			for _, run := range runs {
				if run.Curriculum.Slug == slug {
					filtered = append(filtered, run)
				}
			}
			runs = filtered
		}

		if jsonOutput || !output.IsTTY() {
			raw, err := json.Marshal(runs)
			if err != nil {
				return err
			}
			output.JSON(raw)
			return nil
		}

		if len(runs) == 0 {
			fmt.Println("No retrocasting runs. Visit /retrocasting to start one.")
			return nil
		}

		for i, run := range runs {
			if i > 0 {
				fmt.Println()
			}
			printRetroRun(run)
		}
		return nil
	},
}

var retroAdvanceCmd = &cobra.Command{
	Use:   "advance [slug]",
	Short: "Move a retrocasting run forward one checkpoint",
	Long: `Move a run forward one checkpoint.

The server refuses to advance while any question closing at that checkpoint has
neither a forecast nor an explicit skip, and reports which ones. Do not skip on a
learner's behalf to get past that: the point of the exercise is the forecast.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		runID, _ := cmd.Flags().GetInt("run")
		yes, _ := cmd.Flags().GetBool("yes")

		run, err := resolveRetroRun(runID, args)
		if err != nil {
			return err
		}

		if run.Complete {
			return fmt.Errorf("run %d (%s) has reached the end of its set", run.RunID, run.Curriculum.Slug)
		}

		if len(run.Blockers) > 0 {
			var titles []string
			for _, blocker := range run.Blockers {
				titles = append(titles, blocker.Title)
			}
			return fmt.Errorf(
				"%d question(s) still need a forecast or an explicit skip:\n  - %s\n\nForecast them first; do not skip on the learner's behalf",
				len(run.Blockers), strings.Join(titles, "\n  - "),
			)
		}

		if !yes && output.IsTTY() {
			fmt.Printf("Move %s from %s to %s? Anything revealed then cannot be unseen. [y/N] ",
				run.Curriculum.Slug, formatRetroDate(run.Checkpoint.OccurredAt), formatRetroDate(run.NextOccurredAt))
			var answer string
			fmt.Scanln(&answer)
			if !strings.EqualFold(strings.TrimSpace(answer), "y") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		resp, err := client.Post(fmt.Sprintf("/retrocasting/runs/%d/advance", run.RunID), nil)
		if err != nil {
			return retroAdvanceError(err)
		}

		var advanced retroRun
		if err := resp.DataInto(&advanced); err != nil {
			return err
		}

		if jsonOutput || !output.IsTTY() {
			raw, err := json.Marshal(advanced)
			if err != nil {
				return err
			}
			output.JSON(raw)
			return nil
		}

		fmt.Printf("Now at %s — %s\n\n", formatRetroDate(advanced.Checkpoint.OccurredAt), advanced.Checkpoint.Title)
		printRetroRun(advanced)
		return nil
	},
}

// resolveRetroRun picks the run to act on: an explicit --run id, a slug, or the
// only run there is.
func resolveRetroRun(runID int, args []string) (retroRun, error) {
	resp, err := client.Get("/retrocasting/runs", nil)
	if err != nil {
		return retroRun{}, err
	}

	var runs []retroRun
	if err := resp.DataInto(&runs); err != nil {
		return retroRun{}, err
	}

	if runID > 0 {
		for _, run := range runs {
			if run.RunID == runID {
				return run, nil
			}
		}
		return retroRun{}, fmt.Errorf("no run with id %d", runID)
	}

	if len(args) == 1 {
		slug := strings.TrimSpace(args[0])
		for _, run := range runs {
			if run.Curriculum.Slug == slug {
				return run, nil
			}
		}
		return retroRun{}, fmt.Errorf("no run for %q", slug)
	}

	switch len(runs) {
	case 0:
		return retroRun{}, fmt.Errorf("no retrocasting runs; visit /retrocasting to start one")
	case 1:
		return runs[0], nil
	default:
		var slugs []string
		for _, run := range runs {
			slugs = append(slugs, run.Curriculum.Slug)
		}
		return retroRun{}, fmt.Errorf("several runs; name one: %s", strings.Join(slugs, ", "))
	}
}

func retroAdvanceError(err error) error {
	var apiErr *api.APIError
	if ok := asAPIError(err, &apiErr); ok && apiErr.Code == "unanswered_items" {
		return fmt.Errorf("%s (forecast them first; do not skip on the learner's behalf)", apiErr.Message)
	}
	return err
}

func printRetroRun(run retroRun) {
	pairs := [][2]string{
		{"Set", fmt.Sprintf("%s (v%d)", run.Curriculum.Title, run.Version)},
		{"Slug", run.Curriculum.Slug},
		{"Date", formatRetroDate(run.Checkpoint.OccurredAt)},
		{"Checkpoint", fmt.Sprintf("%s (%d)", run.Checkpoint.Title, run.Checkpoint.Sequence)},
	}

	if run.Complete {
		pairs = append(pairs, [2]string{"Next", "complete"})
	} else {
		pairs = append(pairs, [2]string{"Next", formatRetroDate(run.NextOccurredAt)})
	}
	output.KeyValue(pairs)

	if len(run.Questions) > 0 {
		fmt.Println()
		rows := make([][]string, 0, len(run.Questions))
		for _, question := range run.Questions {
			state := "open"
			switch {
			case question.Resolved:
				state = "resolved"
			case !question.Open:
				state = "closed"
			}
			submitted := question.Submitted
			if submitted == "" {
				submitted = "—"
			}
			if question.Contaminated {
				state += ", contaminated"
			}
			rows = append(rows, []string{question.MarketCode, truncateRetro(question.Title, 54), state, submitted})
		}
		output.Table([]string{"Code", "Question", "State", "You"}, rows)
	}

	if len(run.Blockers) > 0 {
		fmt.Println()
		output.Warn(fmt.Sprintf("%d question(s) need a forecast or skip before advancing", len(run.Blockers)))
	}
}

func formatRetroDate(value string) string {
	if value == "" {
		return "—"
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		if len(value) >= 10 {
			return value[:10]
		}
		return value
	}
	return parsed.Format("2 January 2006")
}

func truncateRetro(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max-1] + "…"
}

func init() {
	retroAdvanceCmd.Flags().Int("run", 0, "Run id, when you have several runs")
	retroAdvanceCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	retroCmd.AddCommand(retroStatusCmd)
	retroCmd.AddCommand(retroAdvanceCmd)
	rootCmd.AddCommand(retroCmd)
}

// asAPIError unwraps an *api.APIError if that is what err is.
func asAPIError(err error, target **api.APIError) bool {
	if apiErr, ok := err.(*api.APIError); ok {
		*target = apiErr
		return true
	}
	return false
}
