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
	Distribution  string
	Median        float64
	Sigma         float64
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

type draftForecastPoint struct {
	ID        int      `json:"id"`
	Label     string   `json:"label"`
	Threshold *float64 `json:"threshold"`
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
that should run updates by a human before submitting a trade.

Use --submit-pending to submit existing pending edits as a trade
without running the draft planner. This avoids needing --threshold
and --probability when you've already staged edits.`,
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

func updatePendingEditsWithRemainder(
	code,
	updatesRaw,
	mode string,
	autoShape bool,
	remainderRequest multicountRemainderRequest,
) error {
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

	return updatePendingEditsBody(code, body, autoShape, true, remainderRequest)
}

func updatePendingEditsBody(
	code string,
	body map[string]interface{},
	autoShape bool,
	usePendingBaseline bool,
	remainderRequest multicountRemainderRequest,
) error {
	updates, err := parseProbabilityUpdatesFromBody(body)
	if err != nil {
		return err
	}

	if autoShape {
		if len(updates) > 0 {
			shaped, report, err := shapeProbabilityUpdates(code, updates, shapeOptions{
				UsePendingBaseline: usePendingBaseline,
			})
			if err != nil {
				return err
			}
			body["updates"] = probabilityUpdatesToPayload(shaped)
			if output.IsTTY() && !jsonOutput && report.OutputCount != report.InputCount {
				fmt.Printf(
					"Auto-shaped updates: %d input -> %d applied.\n",
					report.InputCount,
					report.OutputCount,
				)
			}
			updates = shaped
		}
	}

	updates, remainderReport, err := applyMulticountRemainder(
		code,
		updates,
		usePendingBaseline,
		remainderRequest,
	)
	if err != nil {
		return err
	}
	if remainderRequest.Enabled() && !remainderReport.IsMulticount {
		return fmt.Errorf("--fill-remainder/--remove-remainder are only supported for multicount markets")
	}
	printMulticountRemainderNotice(code, remainderReport, remainderRequest)

	if updates != nil {
		body["updates"] = probabilityUpdatesToPayload(updates)
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

func runPendingEdits(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	code := args[0]
	clear, _ := cmd.Flags().GetBool("clear")
	updatesJSON, _ := cmd.Flags().GetString("updates")
	mode, _ := cmd.Flags().GetString("mode")
	noAutoShape, _ := cmd.Flags().GetBool("no-auto-shape")
	autoShape := !noAutoShape
	submit, _ := cmd.Flags().GetBool("submit")
	submitPending, _ := cmd.Flags().GetBool("submit-pending")
	plannerMode := isDraftPlannerMode(cmd)
	remainderRequest, err := parseMulticountRemainderRequest(cmd)
	if err != nil {
		return err
	}

	if clear {
		if plannerMode || updatesJSON != "" || remainderRequest.Enabled() || submit || submitPending {
			return fmt.Errorf("--clear cannot be combined with draft planning flags, --updates, remainder flags, or --submit/--submit-pending")
		}
		return clearPendingEdits(code)
	}

	if submitPending {
		if plannerMode || updatesJSON != "" {
			return fmt.Errorf("--submit-pending submits existing pending edits and cannot be combined with planning flags or --updates")
		}
		yes, _ := cmd.Flags().GetBool("yes")
		return submitPendingEditsAsTrade(code, autoShape, remainderRequest, yes)
	}

	if plannerMode {
		if updatesJSON != "" {
			return fmt.Errorf("use either draft planning flags or --updates JSON, not both")
		}
		return runDraftPlanner(cmd, code, mode, autoShape, remainderRequest)
	}

	if submit {
		return fmt.Errorf("--submit requires draft planning flags")
	}

	if updatesJSON != "" {
		return updatePendingEditsWithRemainder(code, updatesJSON, mode, autoShape, remainderRequest)
	}

	// Check stdin
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		stdinData, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
		return updatePendingEditsWithRemainder(code, string(stdinData), mode, autoShape, remainderRequest)
	}

	if remainderRequest.Enabled() {
		body := map[string]interface{}{
			"updates": []interface{}{},
			"mode":    mode,
		}
		return updatePendingEditsBody(code, body, false, true, remainderRequest)
	}

	// Default: show pending edits
	return showPendingEdits(code)
}

func isDraftPlannerMode(cmd *cobra.Command) bool {
	flags := []string{
		"threshold",
		"probability",
		"interpolate-to",
		"distribution",
		"median",
		"sigma",
		"from-group",
		"to-group",
		"next-groups",
		"threshold-tolerance",
		"submit",
	}

	for _, name := range flags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func runDraftPlanner(
	cmd *cobra.Command,
	code,
	mode string,
	autoShape bool,
	remainderRequest multicountRemainderRequest,
) error {
	distribution, _ := cmd.Flags().GetString("distribution")
	distribution = strings.ToLower(strings.TrimSpace(distribution))
	median, _ := cmd.Flags().GetFloat64("median")
	sigma, _ := cmd.Flags().GetFloat64("sigma")
	threshold, _ := cmd.Flags().GetFloat64("threshold")
	probability, _ := cmd.Flags().GetFloat64("probability")
	interpolateTo, _ := cmd.Flags().GetFloat64("interpolate-to")
	fromGroup, _ := cmd.Flags().GetString("from-group")
	toGroup, _ := cmd.Flags().GetString("to-group")
	nextGroups, _ := cmd.Flags().GetInt("next-groups")
	tolerance, _ := cmd.Flags().GetFloat64("threshold-tolerance")
	apply, _ := cmd.Flags().GetBool("apply")
	submit, _ := cmd.Flags().GetBool("submit")
	yes, _ := cmd.Flags().GetBool("yes")

	// --multicount-group doubles as a single-group selector when
	// --from-group/--to-group are not set
	if remainderRequest.Group != "" && fromGroup == "" && toGroup == "" && nextGroups == 0 {
		fromGroup = remainderRequest.Group
		toGroup = remainderRequest.Group
	}

	if apply && submit {
		return fmt.Errorf("use either --apply (save pending edits) or --submit (place trade), not both")
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

	useDistribution := distribution != "" || cmd.Flags().Changed("distribution")
	if useDistribution {
		if distribution == "" {
			return fmt.Errorf("--distribution is required when using distribution mode")
		}
		if distribution != "lognormal" {
			return fmt.Errorf("--distribution currently supports only: lognormal")
		}
		if cmd.Flags().Changed("threshold") || cmd.Flags().Changed("probability") || cmd.Flags().Changed("interpolate-to") {
			return fmt.Errorf("distribution mode cannot be combined with --threshold/--probability/--interpolate-to")
		}
		if median <= 0 {
			return fmt.Errorf("--median must be > 0")
		}
		if sigma <= 0 {
			return fmt.Errorf("--sigma must be > 0")
		}
	} else {
		if !cmd.Flags().Changed("threshold") || !cmd.Flags().Changed("probability") {
			return fmt.Errorf("draft planner requires both --threshold and --probability")
		}
		if probability < 0 || probability > 1 {
			return fmt.Errorf("--probability must be between 0 and 1")
		}
		if cmd.Flags().Changed("interpolate-to") && (interpolateTo < 0 || interpolateTo > 1) {
			return fmt.Errorf("--interpolate-to must be between 0 and 1")
		}
		if threshold < 0 {
			return fmt.Errorf("--threshold must be non-negative")
		}
	}

	opts := draftPlanOptions{
		Distribution: distribution,
		Median:       median,
		Sigma:        sigma,
		Threshold:    threshold,
		Probability:  probability,
		FromGroup:    fromGroup,
		ToGroup:      toGroup,
		NextGroups:   nextGroups,
		Tolerance:    tolerance,
	}
	if cmd.Flags().Changed("interpolate-to") {
		opts.InterpolateTo = &interpolateTo
	}

	var (
		plan           []draftPlanLine
		selectedGroups []string
		missingGroups  []string
		err            error
	)

	if useDistribution {
		plan, selectedGroups, missingGroups, err = buildDraftDistributionPlan(code, opts)
	} else {
		plan, selectedGroups, missingGroups, err = buildDraftPlan(code, opts)
	}
	if err != nil {
		return err
	}

	if output.IsTTY() && len(selectedGroups) > 1 {
		if useDistribution {
			fmt.Printf(
				"Distribution mode: applying %s(median=%.3f, sigma=%.3f) to %d selected group(s).\n",
				distribution,
				median,
				sigma,
				len(selectedGroups),
			)
		} else if opts.InterpolateTo == nil {
			fmt.Printf(
				"Applying a constant %.2f%% across %d selected group(s). Use --interpolate-to for a cross-group ramp.\n",
				probability*100,
				len(selectedGroups),
			)
		} else {
			fmt.Printf(
				"Interpolating across %d selected group(s): %.2f%% -> %.2f%%.\n",
				len(selectedGroups),
				probability*100,
				(*opts.InterpolateTo)*100,
			)
		}
	}

	updates := draftPlanAsProbabilityUpdates(plan)
	if autoShape {
		shaped, _, err := shapeProbabilityUpdates(code, updates, shapeOptions{
			UsePendingBaseline: true,
		})
		if err != nil {
			return err
		}
		updates = shaped
	}

	updates, remainderReport, err := applyMulticountRemainder(
		code,
		updates,
		true,
		remainderRequest,
	)
	if err != nil {
		return err
	}
	if remainderRequest.Enabled() && !remainderReport.IsMulticount {
		return fmt.Errorf("--fill-remainder/--remove-remainder are only supported for multicount markets")
	}
	if remainderRequest.Enabled() || !apply {
		printMulticountRemainderNotice(code, remainderReport, remainderRequest)
	}

	if (jsonOutput || !output.IsTTY()) && !submit {
		preview := map[string]interface{}{
			"selected_groups":      selectedGroups,
			"missing_groups":       missingGroups,
			"updates":              probabilityUpdatesToPayload(updates),
			"mode":                 mode,
			"apply":                apply,
			"multicount_remainder": multicountRemainderReportJSON(remainderReport),
		}
		if remainderReport.IsMulticount {
			preview["group_expected_values"] = buildGroupExpectedValuesJSON(code, true)
		}

		data, _ := json.Marshal(preview)
		if !apply {
			output.JSON(data)
			return nil
		}
	}

	if output.IsTTY() {
		if useDistribution {
			fmt.Printf(
				"Planned %d update(s) from %s distribution across %d group(s).\n",
				len(plan),
				distribution,
				len(selectedGroups),
			)
		} else {
			fmt.Printf(
				"Planned %d update(s) for threshold %.3f across %d group(s).\n",
				len(plan),
				threshold,
				len(selectedGroups),
			)
		}

		if len(missingGroups) > 0 {
			fmt.Printf(
				"Skipped %d group(s) with no threshold match: %s\n",
				len(missingGroups),
				strings.Join(missingGroups, ", "),
			)
		}

		headers := []string{"SUBMARKET ID", "PROBABILITY"}
		rows := make([][]string, 0, len(updates))
		for _, line := range updates {
			rows = append(rows, []string{
				fmt.Sprintf("%d", line.SubmarketID),
				fmt.Sprintf("%.2f%%", line.Probability*100),
			})
		}
		output.Table(headers, rows)

		if remainderReport.IsMulticount {
			printMulticountGroupBreakdown(code, true)
		}
	}

	if !apply && !submit {
		if output.IsTTY() {
			fmt.Println("Dry run only. Re-run with --apply to save these pending edits.")
		}
		return nil
	}

	if submit {
		return submitTradeFromDraft(code, updates, yes)
	}

	body := map[string]interface{}{
		"updates": probabilityUpdatesToPayload(updates),
		"mode":    mode,
	}
	return updatePendingEditsBody(code, body, false, true, multicountRemainderRequest{})
}

func buildDraftPlan(code string, opts draftPlanOptions) ([]draftPlanLine, []string, []string, error) {
	forecastGroups, err := fetchDraftForecastGroups(code)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(forecastGroups) == 0 {
		return nil, nil, nil, fmt.Errorf("no grouped forecast data available; try specifying --group and verify market type")
	}

	groups := make([]string, 0, len(forecastGroups))
	for group := range forecastGroups {
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
			forecastGroups[group],
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

func buildDraftDistributionPlan(
	code string,
	opts draftPlanOptions,
) ([]draftPlanLine, []string, []string, error) {
	forecastGroups, err := fetchDraftForecastGroups(code)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(forecastGroups) == 0 {
		return nil, nil, nil, fmt.Errorf("no grouped forecast data available; try specifying --group and verify market type")
	}

	groups := make([]string, 0, len(forecastGroups))
	for group := range forecastGroups {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	selectedGroups, err := selectDraftPlanGroups(groups, opts.FromGroup, opts.ToGroup, opts.NextGroups)
	if err != nil {
		return nil, nil, nil, err
	}

	lines := make([]draftPlanLine, 0)
	missingGroups := make([]string, 0)

	for _, group := range selectedGroups {
		groupPoints := forecastGroups[group]
		if len(groupPoints) == 0 {
			missingGroups = append(missingGroups, group)
			continue
		}

		sorted := append([]draftForecastPoint(nil), groupPoints...)
		sort.Slice(sorted, func(i, j int) bool {
			li := math.Inf(1)
			if sorted[i].Threshold != nil {
				li = *sorted[i].Threshold
			}
			lj := math.Inf(1)
			if sorted[j].Threshold != nil {
				lj = *sorted[j].Threshold
			}
			if li == lj {
				return sorted[i].ID < sorted[j].ID
			}
			return li < lj
		})

		for _, point := range sorted {
			if point.Threshold == nil {
				continue
			}
			probability := lognormalSurvivalProbability(*point.Threshold, opts.Median, opts.Sigma)
			lines = append(lines, draftPlanLine{
				Group:       group,
				SubmarketID: point.ID,
				Threshold:   *point.Threshold,
				Probability: roundProbability(clampProb(probability)),
			})
		}
	}

	if len(lines) == 0 {
		return nil, selectedGroups, missingGroups, fmt.Errorf("distribution produced no threshold-matched submarkets in selected groups")
	}

	return lines, selectedGroups, missingGroups, nil
}

func fetchDraftForecastGroups(code string) (map[string][]draftForecastPoint, error) {
	params := url.Values{}
	params.Set("include", "full")
	params.Set("limit", "0")
	params.Set("mode", "full")

	resp, err := client.Get("/markets/"+code+"/forecast", params)
	if err != nil {
		return nil, err
	}

	data, err := resp.Data()
	if err != nil {
		return nil, err
	}

	var forecast struct {
		ResponseMode string                          `json:"response_mode"`
		Forecast     map[string][]draftForecastPoint `json:"forecast"`
	}
	if err := json.Unmarshal(data, &forecast); err != nil {
		return nil, fmt.Errorf("parsing forecast response: %w", err)
	}
	if forecast.ResponseMode == "summary_index" {
		return nil, fmt.Errorf("received summary_index forecast; retry with include=full and limit=0")
	}
	return forecast.Forecast, nil
}

func lognormalSurvivalProbability(threshold, median, sigma float64) float64 {
	if threshold <= 0 {
		return 1
	}
	mu := math.Log(median)
	z := (math.Log(threshold) - mu) / sigma
	cdf := 0.5 * (1 + math.Erf(z/math.Sqrt2))
	return 1 - cdf
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
	submarkets []draftForecastPoint,
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

func draftPlanAsProbabilityUpdates(plan []draftPlanLine) []probabilityUpdate {
	updates := make([]probabilityUpdate, 0, len(plan))
	for _, line := range plan {
		updates = append(updates, probabilityUpdate{
			SubmarketID: line.SubmarketID,
			Probability: line.Probability,
		})
	}
	return updates
}

func roundProbability(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func submitPendingEditsAsTrade(
	code string,
	autoShape bool,
	remainderRequest multicountRemainderRequest,
	yes bool,
) error {
	pendingStates, err := fetchPendingEditStates(code)
	if err != nil {
		return fmt.Errorf("fetching pending edits: %w", err)
	}
	if len(pendingStates) == 0 {
		return fmt.Errorf("no pending edits to submit; use draft planning flags or --updates to create some first")
	}

	updates := make([]probabilityUpdate, 0, len(pendingStates))
	for id, state := range pendingStates {
		if !state.HasProbability {
			continue
		}
		update := probabilityUpdate{
			SubmarketID: id,
			Probability: state.Probability,
		}
		if state.HasIsFixed {
			fixed := state.IsFixed
			update.IsFixed = &fixed
		}
		updates = append(updates, update)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].SubmarketID < updates[j].SubmarketID })

	if len(updates) == 0 {
		return fmt.Errorf("pending edits contain no probability updates")
	}

	if autoShape {
		shaped, report, err := shapeProbabilityUpdates(code, updates, shapeOptions{
			UsePendingBaseline: false,
		})
		if err != nil {
			return err
		}
		if output.IsTTY() && !jsonOutput && report.OutputCount != report.InputCount {
			fmt.Printf(
				"Auto-shaped updates: %d input -> %d applied.\n",
				report.InputCount,
				report.OutputCount,
			)
		}
		updates = shaped
	}

	updates, remainderReport, err := applyMulticountRemainder(
		code,
		updates,
		false,
		remainderRequest,
	)
	if err != nil {
		return err
	}
	if remainderRequest.Enabled() && !remainderReport.IsMulticount {
		return fmt.Errorf("--fill-remainder/--remove-remainder are only supported for multicount markets")
	}
	printMulticountRemainderNotice(code, remainderReport, remainderRequest)

	if output.IsTTY() && !jsonOutput {
		fmt.Printf("Submitting %d pending edit(s) as trade on %s.\n", len(updates), code)
	}

	return submitTradeFromDraft(code, updates, yes)
}

func submitTradeFromDraft(code string, updates []probabilityUpdate, yes bool) error {
	body := map[string]interface{}{
		"updates": probabilityUpdatesToPayload(updates),
	}

	if output.IsTTY() && !jsonOutput {
		totalCost, quoteErr := previewTradeCost(code, updates)
		if quoteErr == nil {
			fmt.Printf("Estimated cost: %.4f points across %d submarket(s).\n", totalCost, len(updates))
		}

		if !yes {
			fmt.Printf("Submit trade on %s? [y/N] ", code)
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}
	}

	resp, err := client.Post("/markets/"+code+"/positions", body)
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

	fmt.Println("Trade placed successfully.")
	return nil
}

func previewTradeCost(code string, updates []probabilityUpdate) (float64, error) {
	totalCost := 0.0
	for _, update := range updates {
		qu := quoteUpdate{
			SubmarketID: update.SubmarketID,
			Probability: update.Probability,
		}
		line, err := requestSingleQuote(code, qu)
		if err != nil {
			return 0, err
		}
		totalCost += line.Cost
	}
	return totalCost, nil
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
	cmd.Flags().Float64("interpolate-to", 0, "Draft planner: optional ending probability (0..1) for linear interpolation across selected groups")
	cmd.Flags().String("distribution", "", "Draft planner: parametric distribution to fit across thresholds (currently: lognormal)")
	cmd.Flags().Float64("median", 0, "Draft planner distribution mode: median parameter")
	cmd.Flags().Float64("sigma", 0, "Draft planner distribution mode: sigma parameter")
	cmd.Flags().String("from-group", "", "Draft planner: first projection group (inclusive)")
	cmd.Flags().String("to-group", "", "Draft planner: last projection group (inclusive)")
	cmd.Flags().Int("next-groups", 0, "Draft planner: select next N groups (from --from-group or current period)")
	cmd.Flags().Float64("threshold-tolerance", 0.001, "Draft planner: threshold match tolerance")
	cmd.Flags().Bool("apply", false, "Draft planner: apply planned updates (without this, command previews only)")
	cmd.Flags().Bool("submit", false, "Draft planner: submit planned updates directly as a trade")
	cmd.Flags().Bool("submit-pending", false, "Submit existing pending edits as a trade (no planning flags needed)")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt when using --submit or --submit-pending")
	cmd.Flags().Bool("no-auto-shape", false, "Disable auto interpolation and monotonic shaping")
	addMulticountRemainderFlags(cmd)
}
