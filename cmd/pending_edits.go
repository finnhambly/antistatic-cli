package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

type draftPlanOptions struct {
	Threshold     float64
	Probability   float64
	InterpolateTo *float64
	FromGroup     string
	ToGroup       string
	NextGroups    int
	Tolerance     float64
}

type draftPlanLine struct {
	Group       string
	SubmarketID int
	Threshold   float64
	Probability float64
}

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
	RunE: runPendingEdits,
}

var draftCmd = &cobra.Command{
	Use:   "draft <code>",
	Short: "Create or review draft edits (agent-friendly)",
	Long: `Draft probability updates for human review before trading.

This is an alias for "pending-edits" and is recommended for AI agents
that should run updates by a human before submitting a trade.`,
	Args: cobra.ExactArgs(1),
	RunE: runPendingEdits,
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

	return updatePendingEditsBody(code, body)
}

func updatePendingEditsBody(code string, body map[string]interface{}) error {
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

func runPendingEdits(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	code := args[0]
	clear, _ := cmd.Flags().GetBool("clear")
	updatesJSON, _ := cmd.Flags().GetString("updates")
	mode, _ := cmd.Flags().GetString("mode")
	plannerMode := isDraftPlannerMode(cmd)

	if clear {
		if plannerMode || updatesJSON != "" {
			return fmt.Errorf("--clear cannot be combined with draft planning flags or --updates")
		}
		return clearPendingEdits(code)
	}

	if plannerMode {
		if updatesJSON != "" {
			return fmt.Errorf("use either draft planning flags or --updates JSON, not both")
		}
		return runDraftPlanner(cmd, code, mode)
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
}

func isDraftPlannerMode(cmd *cobra.Command) bool {
	flags := []string{
		"threshold",
		"probability",
		"interpolate-to",
		"from-group",
		"to-group",
		"next-groups",
		"threshold-tolerance",
		"apply",
	}

	for _, name := range flags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func runDraftPlanner(cmd *cobra.Command, code, mode string) error {
	if !cmd.Flags().Changed("threshold") || !cmd.Flags().Changed("probability") {
		return fmt.Errorf("draft planner requires both --threshold and --probability")
	}

	threshold, _ := cmd.Flags().GetFloat64("threshold")
	probability, _ := cmd.Flags().GetFloat64("probability")
	interpolateTo, _ := cmd.Flags().GetFloat64("interpolate-to")
	fromGroup, _ := cmd.Flags().GetString("from-group")
	toGroup, _ := cmd.Flags().GetString("to-group")
	nextGroups, _ := cmd.Flags().GetInt("next-groups")
	tolerance, _ := cmd.Flags().GetFloat64("threshold-tolerance")
	apply, _ := cmd.Flags().GetBool("apply")

	if probability < 0 || probability > 1 {
		return fmt.Errorf("--probability must be between 0 and 1")
	}
	if cmd.Flags().Changed("interpolate-to") && (interpolateTo < 0 || interpolateTo > 1) {
		return fmt.Errorf("--interpolate-to must be between 0 and 1")
	}
	if threshold < 0 {
		return fmt.Errorf("--threshold must be non-negative")
	}
	if nextGroups < 0 {
		return fmt.Errorf("--next-groups must be >= 0")
	}
	if tolerance <= 0 {
		return fmt.Errorf("--threshold-tolerance must be > 0")
	}
	if nextGroups > 0 && toGroup != "" {
		return fmt.Errorf("use either --next-groups or --to-group")
	}

	opts := draftPlanOptions{
		Threshold:   threshold,
		Probability: probability,
		FromGroup:   fromGroup,
		ToGroup:     toGroup,
		NextGroups:  nextGroups,
		Tolerance:   tolerance,
	}
	if cmd.Flags().Changed("interpolate-to") {
		opts.InterpolateTo = &interpolateTo
	}

	plan, selectedGroups, missingGroups, err := buildDraftPlan(code, opts)
	if err != nil {
		return err
	}

	if jsonOutput || !output.IsTTY() {
		preview := map[string]interface{}{
			"selected_groups": selectedGroups,
			"missing_groups":  missingGroups,
			"updates":         draftPlanAsUpdates(plan),
			"mode":            mode,
			"apply":           apply,
		}

		data, _ := json.Marshal(preview)
		if !apply {
			output.JSON(data)
			return nil
		}
	}

	if output.IsTTY() {
		fmt.Printf(
			"Planned %d update(s) for threshold %.3f across %d group(s).\n",
			len(plan),
			threshold,
			len(selectedGroups),
		)

		if len(missingGroups) > 0 {
			fmt.Printf(
				"Skipped %d group(s) with no threshold match: %s\n",
				len(missingGroups),
				strings.Join(missingGroups, ", "),
			)
		}

		headers := []string{"GROUP", "SUBMARKET ID", "THRESHOLD", "PROBABILITY"}
		rows := make([][]string, 0, len(plan))
		for _, line := range plan {
			rows = append(rows, []string{
				line.Group,
				fmt.Sprintf("%d", line.SubmarketID),
				fmt.Sprintf("%.3f", line.Threshold),
				fmt.Sprintf("%.2f%%", line.Probability*100),
			})
		}
		output.Table(headers, rows)
	}

	if !apply {
		if output.IsTTY() {
			fmt.Println("Dry run only. Re-run with --apply to save these pending edits.")
		}
		return nil
	}

	body := map[string]interface{}{
		"updates": draftPlanAsUpdates(plan),
		"mode":    mode,
	}
	return updatePendingEditsBody(code, body)
}

func buildDraftPlan(code string, opts draftPlanOptions) ([]draftPlanLine, []string, []string, error) {
	params := url.Values{}
	params.Set("include", "full")
	params.Set("limit", "1000000")

	resp, err := client.Get("/markets/"+code+"/forecast", params)
	if err != nil {
		return nil, nil, nil, err
	}

	data, err := resp.Data()
	if err != nil {
		return nil, nil, nil, err
	}

	var forecast struct {
		Forecast map[string][]struct {
			ID        int      `json:"id"`
			Label     string   `json:"label"`
			Threshold *float64 `json:"threshold"`
		} `json:"forecast"`
	}
	if err := json.Unmarshal(data, &forecast); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing forecast response: %w", err)
	}
	if len(forecast.Forecast) == 0 {
		return nil, nil, nil, fmt.Errorf("no grouped forecast data available; try specifying --group and verify market type")
	}

	groups := make([]string, 0, len(forecast.Forecast))
	for group := range forecast.Forecast {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	selectedGroups, err := selectDraftPlanGroups(groups, opts.FromGroup, opts.ToGroup, opts.NextGroups)
	if err != nil {
		return nil, nil, nil, err
	}

	lines := make([]draftPlanLine, 0, len(selectedGroups))
	missingGroups := make([]string, 0)

	for _, group := range selectedGroups {
		submarketID, matchedThreshold, ok := matchThresholdSubmarket(
			forecast.Forecast[group],
			opts.Threshold,
			opts.Tolerance,
		)
		if !ok {
			missingGroups = append(missingGroups, group)
			continue
		}

		lines = append(lines, draftPlanLine{
			Group:       group,
			SubmarketID: submarketID,
			Threshold:   matchedThreshold,
			Probability: opts.Probability,
		})
	}

	if len(lines) == 0 {
		return nil, selectedGroups, missingGroups, fmt.Errorf(
			"no submarkets matched threshold %.3f in selected groups",
			opts.Threshold,
		)
	}

	if opts.InterpolateTo != nil && len(lines) > 1 {
		start := opts.Probability
		end := *opts.InterpolateTo
		span := float64(len(lines) - 1)

		for i := range lines {
			fraction := float64(i) / span
			lines[i].Probability = roundProbability(start + (end-start)*fraction)
		}
	} else {
		for i := range lines {
			lines[i].Probability = roundProbability(opts.Probability)
		}
	}

	return lines, selectedGroups, missingGroups, nil
}

func selectDraftPlanGroups(groups []string, fromGroup, toGroup string, nextGroups int) ([]string, error) {
	if len(groups) == 0 {
		return nil, fmt.Errorf("market has no projection groups")
	}

	startIdx := 0
	if fromGroup != "" {
		idx := findGroupIndex(groups, fromGroup)
		if idx < 0 {
			return nil, fmt.Errorf("from-group %q not found", fromGroup)
		}
		startIdx = idx
	} else if nextGroups > 0 {
		startIdx = defaultNextGroupStartIndex(groups)
	}

	if nextGroups > 0 {
		end := startIdx + nextGroups
		if end > len(groups) {
			end = len(groups)
		}
		return groups[startIdx:end], nil
	}

	endIdx := len(groups) - 1
	if toGroup != "" {
		idx := findGroupIndex(groups, toGroup)
		if idx < 0 {
			return nil, fmt.Errorf("to-group %q not found", toGroup)
		}
		endIdx = idx
	}

	if endIdx < startIdx {
		return nil, fmt.Errorf("to-group must be >= from-group in group order")
	}

	return groups[startIdx : endIdx+1], nil
}

func defaultNextGroupStartIndex(groups []string) int {
	now := time.Now().UTC()
	isoYear, isoWeek := now.ISOWeek()

	candidates := []string{
		fmt.Sprintf("%04d-W%02d", isoYear, isoWeek),
		now.Format("2006-01-02"),
		now.Format("2006-01"),
		fmt.Sprintf("%04d", now.Year()),
	}

	for _, candidate := range candidates {
		idx := sort.SearchStrings(groups, candidate)
		if idx >= 0 && idx < len(groups) {
			return idx
		}
	}

	return 0
}

func findGroupIndex(groups []string, target string) int {
	idx := sort.SearchStrings(groups, target)
	if idx >= 0 && idx < len(groups) && groups[idx] == target {
		return idx
	}
	return -1
}

func matchThresholdSubmarket(
	submarkets []struct {
		ID        int      `json:"id"`
		Label     string   `json:"label"`
		Threshold *float64 `json:"threshold"`
	},
	target float64,
	tolerance float64,
) (int, float64, bool) {
	bestDiff := math.MaxFloat64
	bestID := 0
	bestThreshold := 0.0

	for _, submarket := range submarkets {
		if submarket.Threshold == nil {
			continue
		}
		diff := math.Abs(*submarket.Threshold - target)
		if diff <= tolerance && diff < bestDiff {
			bestDiff = diff
			bestID = submarket.ID
			bestThreshold = *submarket.Threshold
		}
	}

	if bestID == 0 {
		return 0, 0, false
	}
	return bestID, bestThreshold, true
}

func draftPlanAsUpdates(plan []draftPlanLine) []map[string]interface{} {
	updates := make([]map[string]interface{}, 0, len(plan))
	for _, line := range plan {
		updates = append(updates, map[string]interface{}{
			"submarket_id": line.SubmarketID,
			"probability":  line.Probability,
		})
	}
	return updates
}

func roundProbability(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func init() {
	addPendingEditFlags(pendingEditsCmd)
	addPendingEditFlags(draftCmd)

	rootCmd.AddCommand(pendingEditsCmd)
	rootCmd.AddCommand(draftCmd)
}

func addPendingEditFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("clear", false, "Clear all pending edits")
	cmd.Flags().String("updates", "", "Probability updates as JSON")
	cmd.Flags().String("mode", "merge", "Update mode: merge (default) or replace")
	cmd.Flags().Float64("threshold", 0, "Draft planner: threshold to target (e.g. 70)")
	cmd.Flags().Float64("probability", 0, "Draft planner: target probability for selected groups (0..1)")
	cmd.Flags().Float64("interpolate-to", 0, "Draft planner: optional ending probability (0..1) for linear interpolation")
	cmd.Flags().String("from-group", "", "Draft planner: first projection group (inclusive)")
	cmd.Flags().String("to-group", "", "Draft planner: last projection group (inclusive)")
	cmd.Flags().Int("next-groups", 0, "Draft planner: select next N groups (from --from-group or current period)")
	cmd.Flags().Float64("threshold-tolerance", 0.001, "Draft planner: threshold match tolerance")
	cmd.Flags().Bool("apply", false, "Draft planner: apply planned updates (without this, command previews only)")
}
