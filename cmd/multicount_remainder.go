package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

const (
	multicountDisplayedRemainderEpsilon = 0.15
	multicountRemainderPasses           = 6
)

type multicountRemainderRequest struct {
	Fill   bool
	Remove bool
	Group  string
}

type multicountRemainderReport struct {
	IsMulticount    bool
	Group           string
	BeforeRaw       float64
	BeforeEffective float64
	AfterRaw        float64
	AfterEffective  float64
	Applied         float64
	Adjusted        bool
	Action          string
	SuggestedAction string
	SuggestedGroup  string
	NoopReason      string
}

type pendingEditState struct {
	Probability    float64
	HasProbability bool
	IsFixed        bool
	HasIsFixed     bool
}

type multicountForecastEnvelope struct {
	Type       string                     `json:"type"`
	Multicount *multicountForecastMeta    `json:"multicount"`
	Forecast   map[string][]forecastPoint `json:"forecast"`
}

type multicountForecastMeta struct {
	Total    float64  `json:"total"`
	Entities []string `json:"entities"`
}

type multicountShiftPoint struct {
	ID          int
	Width       float64
	Probability float64
	IsFixed     bool
	WorkingBits float64
}

func addMulticountRemainderFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("fill-remainder", false, "Multicount only: fill missing remainder in one group")
	cmd.Flags().Bool("remove-remainder", false, "Multicount only: remove excess remainder in one group")
	cmd.Flags().String("multicount-group", "", "Multicount group/entity to adjust (defaults to first configured entity)")
}

func parseMulticountRemainderRequest(cmd *cobra.Command) (multicountRemainderRequest, error) {
	fill, _ := cmd.Flags().GetBool("fill-remainder")
	remove, _ := cmd.Flags().GetBool("remove-remainder")
	group, _ := cmd.Flags().GetString("multicount-group")
	group = strings.TrimSpace(group)

	if fill && remove {
		return multicountRemainderRequest{}, fmt.Errorf("use either --fill-remainder or --remove-remainder")
	}
	if group != "" && !fill && !remove {
		return multicountRemainderRequest{}, fmt.Errorf("--multicount-group requires --fill-remainder or --remove-remainder")
	}

	return multicountRemainderRequest{Fill: fill, Remove: remove, Group: group}, nil
}

func (r multicountRemainderRequest) Enabled() bool {
	return r.Fill || r.Remove
}

func (r multicountRemainderRequest) Action() string {
	if r.Fill {
		return "fill"
	}
	if r.Remove {
		return "remove"
	}
	return ""
}

func applyMulticountRemainder(
	code string,
	input []probabilityUpdate,
	usePendingBaseline bool,
	request multicountRemainderRequest,
) ([]probabilityUpdate, multicountRemainderReport, error) {
	report := multicountRemainderReport{Action: request.Action()}

	payload, err := fetchMulticountForecastEnvelope(code)
	if err != nil {
		return nil, report, err
	}
	if payload.Multicount == nil || payload.Multicount.Total <= 0 || len(payload.Forecast) == 0 {
		return input, report, nil
	}

	report.IsMulticount = true

	baselineProb := make(map[int]float64)
	currentProb := make(map[int]float64)
	baselineFixed := make(map[int]bool)
	currentFixed := make(map[int]bool)

	for _, points := range payload.Forecast {
		for _, point := range points {
			prob := clampProb(point.CommunityProbability)
			baselineProb[point.ID] = prob
			currentProb[point.ID] = prob
			baselineFixed[point.ID] = false
			currentFixed[point.ID] = false
		}
	}

	if usePendingBaseline {
		pendingStates, err := fetchPendingEditStates(code)
		if err == nil {
			for id, state := range pendingStates {
				if _, ok := baselineProb[id]; !ok {
					continue
				}
				if state.HasProbability {
					prob := clampProb(state.Probability)
					baselineProb[id] = prob
					currentProb[id] = prob
				}
				if state.HasIsFixed {
					baselineFixed[id] = state.IsFixed
					currentFixed[id] = state.IsFixed
				}
			}
		}
	}

	for _, update := range input {
		if _, ok := currentProb[update.SubmarketID]; !ok {
			continue
		}
		currentProb[update.SubmarketID] = clampProb(update.Probability)
		if update.IsFixed != nil {
			currentFixed[update.SubmarketID] = *update.IsFixed
		}
	}

	remainderRaw := payload.Multicount.Total - multicountCurrentExpectedTotal(payload.Forecast, currentProb)
	report.BeforeRaw = remainderRaw
	report.BeforeEffective = normalizeDisplayedRemainderDelta(remainderRaw)
	report.AfterRaw = remainderRaw
	report.AfterEffective = report.BeforeEffective

	report.SuggestedAction = suggestedRemainderAction(report.BeforeEffective)
	selectedGroup, err := selectMulticountGroup(payload.Multicount.Entities, payload.Forecast, request.Group)
	if err != nil {
		if request.Enabled() {
			return nil, report, err
		}
		selectedGroup = ""
	}
	report.SuggestedGroup = selectedGroup

	if request.Enabled() {
		report.Group = selectedGroup
		remaining := report.BeforeEffective
		if request.Fill && remaining <= 0 {
			report.NoopReason = "no missing remainder to fill"
		} else if request.Remove && remaining >= 0 {
			report.NoopReason = "no excess remainder to remove"
		} else {
			totalApplied := 0.0
			for pass := 0; pass < multicountRemainderPasses; pass++ {
				if math.Abs(remaining) <= shapeEpsilon {
					break
				}
				applied := applyExpectedValueDeltaToGroup(
					payload.Forecast[selectedGroup],
					currentProb,
					currentFixed,
					remaining,
				)
				if math.Abs(applied) <= shapeEpsilon {
					break
				}
				totalApplied += applied
				remainderRaw = payload.Multicount.Total - multicountCurrentExpectedTotal(payload.Forecast, currentProb)
				remaining = normalizeDisplayedRemainderDelta(remainderRaw)
			}

			report.Applied = totalApplied
			report.Adjusted = math.Abs(totalApplied) > shapeEpsilon
			report.AfterRaw = payload.Multicount.Total - multicountCurrentExpectedTotal(payload.Forecast, currentProb)
			report.AfterEffective = normalizeDisplayedRemainderDelta(report.AfterRaw)
		}
	}

	if !request.Enabled() {
		return input, report, nil
	}

	result := buildProbabilityUpdatesFromState(input, baselineProb, currentProb, baselineFixed, currentFixed)
	return result, report, nil
}

func fetchMulticountForecastEnvelope(code string) (multicountForecastEnvelope, error) {
	params := url.Values{}
	params.Set("include", "full")
	params.Set("limit", "1000000")

	resp, err := client.Get("/markets/"+code+"/forecast", params)
	if err != nil {
		return multicountForecastEnvelope{}, err
	}

	data, err := resp.Data()
	if err != nil {
		return multicountForecastEnvelope{}, err
	}

	var payload multicountForecastEnvelope
	if err := json.Unmarshal(data, &payload); err != nil {
		return multicountForecastEnvelope{}, fmt.Errorf("parsing forecast payload: %w", err)
	}

	return payload, nil
}

func fetchPendingEditStates(code string) (map[int]pendingEditState, error) {
	resp, err := client.Get("/markets/"+code+"/pending-edits", nil)
	if err != nil {
		return nil, err
	}

	data, err := resp.Data()
	if err != nil {
		return nil, err
	}

	var pending map[string]map[string]interface{}
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, err
	}

	out := make(map[int]pendingEditState, len(pending))
	for rawID, entry := range pending {
		id, err := strconv.Atoi(rawID)
		if err != nil {
			continue
		}
		state := pendingEditState{}
		if prob, ok := toFloat(entry["probability"]); ok {
			state.Probability = clampProb(prob)
			state.HasProbability = true
		}
		if fixed, ok := entry["is_fixed"].(bool); ok {
			state.IsFixed = fixed
			state.HasIsFixed = true
		}
		if fixed, ok := entry["isFixed"].(bool); ok {
			state.IsFixed = fixed
			state.HasIsFixed = true
		}
		out[id] = state
	}

	return out, nil
}

func normalizeDisplayedRemainderDelta(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if math.Abs(value) < multicountDisplayedRemainderEpsilon {
		return 0
	}
	return value
}

func suggestedRemainderAction(delta float64) string {
	if delta > shapeEpsilon {
		return "fill"
	}
	if delta < -shapeEpsilon {
		return "remove"
	}
	return ""
}

func selectMulticountGroup(
	entities []string,
	forecast map[string][]forecastPoint,
	requested string,
) (string, error) {
	if requested != "" {
		if _, ok := forecast[requested]; ok {
			return requested, nil
		}
		groups := sortedForecastGroups(forecast)
		if len(groups) > 12 {
			groups = groups[:12]
		}
		return "", fmt.Errorf("multicount group %q not found (available: %s)", requested, strings.Join(groups, ", "))
	}

	for _, entity := range entities {
		if _, ok := forecast[entity]; ok {
			return entity, nil
		}
	}

	groups := sortedForecastGroups(forecast)
	if len(groups) == 0 {
		return "", fmt.Errorf("market has no multicount groups")
	}
	return groups[0], nil
}

func multicountCurrentExpectedTotal(forecast map[string][]forecastPoint, probabilities map[int]float64) float64 {
	total := 0.0
	for _, points := range forecast {
		total += expectedValueForGroup(points, probabilities)
	}
	return total
}

func expectedValueForGroup(points []forecastPoint, probabilities map[int]float64) float64 {
	sorted := sortedGroupPoints(points)
	if len(sorted) == 0 {
		return 0
	}

	prevThreshold := 0.0
	sum := 0.0
	for _, point := range sorted {
		threshold := prevThreshold
		if point.Threshold != nil {
			threshold = *point.Threshold
		}
		width := threshold - prevThreshold
		prevThreshold = threshold
		if !(width > 0) {
			width = 1
		}

		prob, ok := probabilities[point.ID]
		if !ok {
			prob = point.CommunityProbability
		}
		sum += width * clampProb(prob)
	}
	return sum
}

func sortedGroupPoints(points []forecastPoint) []forecastPoint {
	sorted := append([]forecastPoint(nil), points...)
	sort.Slice(sorted, func(i, j int) bool {
		left := math.Inf(1)
		if sorted[i].Threshold != nil {
			left = *sorted[i].Threshold
		}
		right := math.Inf(1)
		if sorted[j].Threshold != nil {
			right = *sorted[j].Threshold
		}
		if left == right {
			return sorted[i].ID < sorted[j].ID
		}
		return left < right
	})
	return sorted
}

func applyExpectedValueDeltaToGroup(
	points []forecastPoint,
	current map[int]float64,
	fixed map[int]bool,
	deltaExpected float64,
) float64 {
	series, orderedIDs := buildMulticountShiftSeries(points, current, fixed)
	if len(series) == 0 {
		return 0
	}

	currentExpected := expectedValueForSeries(series)
	targetExpected := currentExpected + deltaExpected
	if math.Abs(targetExpected-currentExpected) <= shapeEpsilon {
		return 0
	}

	lambda := solveUniformBitShiftForTargetExpected(series, targetExpected)
	for _, point := range series {
		if point.IsFixed {
			continue
		}
		shifted := bitsToProb(point.WorkingBits + lambda)
		current[point.ID] = clampProb(shifted)
	}

	enforceDirectionalMonotonicity(orderedIDs, current, fixed, "down")
	return expectedValueForGroup(points, current) - currentExpected
}

func buildMulticountShiftSeries(
	points []forecastPoint,
	current map[int]float64,
	fixed map[int]bool,
) ([]multicountShiftPoint, []int) {
	sorted := sortedGroupPoints(points)
	if len(sorted) == 0 {
		return nil, nil
	}

	series := make([]multicountShiftPoint, 0, len(sorted))
	orderedIDs := make([]int, 0, len(sorted))
	prevThreshold := 0.0

	for _, point := range sorted {
		threshold := prevThreshold
		if point.Threshold != nil {
			threshold = *point.Threshold
		}
		width := threshold - prevThreshold
		prevThreshold = threshold
		if !(width > 0) {
			width = 1
		}

		prob, ok := current[point.ID]
		if !ok {
			prob = point.CommunityProbability
		}
		prob = clampProb(prob)

		series = append(series, multicountShiftPoint{
			ID:          point.ID,
			Width:       width,
			Probability: prob,
			IsFixed:     fixed[point.ID],
			WorkingBits: probToBits(prob),
		})
		orderedIDs = append(orderedIDs, point.ID)
	}

	probs := make([]float64, len(series))
	bits := make([]float64, len(series))
	for i := range series {
		probs[i] = series[i].Probability
		bits[i] = series[i].WorkingBits
	}

	flatProbEpsilon := 1.0e-9
	start := 0
	for start < len(probs) {
		end := start
		for end+1 < len(probs) && math.Abs(probs[end+1]-probs[start]) <= flatProbEpsilon {
			end++
		}
		if end > start {
			spreadFlatRunBits(bits, start, end)
		}
		start = end + 1
	}

	minStep := 1.0e-4
	for i := 1; i < len(bits); i++ {
		if !(bits[i] < bits[i-1]-minStep) {
			bits[i] = bits[i-1] - minStep
		}
	}

	for i := range series {
		series[i].WorkingBits = bits[i]
	}

	return series, orderedIDs
}

func spreadFlatRunBits(bits []float64, start, end int) {
	runLength := end - start + 1
	if runLength <= 1 {
		return
	}

	minStep := 1.0e-4
	defaultTailStep := -0.35

	prevIdx := start - 1
	nextIdx := end + 1
	hasPrev := prevIdx >= 0
	hasNext := nextIdx < len(bits)

	if !hasPrev && !hasNext {
		for i := 1; i < len(bits); i++ {
			bits[i] = bits[i-1] + defaultTailStep
		}
		return
	}

	if hasPrev && hasNext {
		step := (bits[nextIdx] - bits[prevIdx]) / float64(runLength+1)
		if !(step < -minStep) {
			step = -minStep
		}
		for i := 0; i < runLength; i++ {
			bits[start+i] = bits[prevIdx] + step*float64(i+1)
		}
		return
	}

	if hasPrev {
		step := defaultTailStep
		if prevIdx >= 1 {
			localStep := bits[prevIdx] - bits[prevIdx-1]
			if !math.IsNaN(localStep) && !math.IsInf(localStep, 0) && localStep < -minStep {
				step = localStep
			}
		}
		if !(step < -minStep) {
			step = -0.05
		}
		for i := 0; i < runLength; i++ {
			bits[start+i] = bits[prevIdx] + step*float64(i+1)
		}
		return
	}

	step := defaultTailStep
	if nextIdx+1 < len(bits) {
		localStep := bits[nextIdx+1] - bits[nextIdx]
		if !math.IsNaN(localStep) && !math.IsInf(localStep, 0) && localStep < -minStep {
			step = localStep
		}
	}
	if !(step < -minStep) {
		step = -0.05
	}
	for i := runLength - 1; i >= 0; i-- {
		offset := runLength - i
		bits[start+i] = bits[nextIdx] - step*float64(offset)
	}
}

func expectedValueForSeries(series []multicountShiftPoint) float64 {
	sum := 0.0
	for _, point := range series {
		sum += point.Width * clampProb(point.Probability)
	}
	return sum
}

func expectedValueWithUniformShift(series []multicountShiftPoint, lambda float64) float64 {
	sum := 0.0
	for _, point := range series {
		prob := point.Probability
		if !point.IsFixed {
			prob = bitsToProb(point.WorkingBits + lambda)
		}
		sum += point.Width * clampProb(prob)
	}
	return sum
}

func solveUniformBitShiftForTargetExpected(series []multicountShiftPoint, target float64) float64 {
	if len(series) == 0 {
		return 0
	}
	lo := -24.0
	hi := 24.0

	minAtLo := expectedValueWithUniformShift(series, lo)
	maxAtHi := expectedValueWithUniformShift(series, hi)

	if target <= minAtLo {
		return lo
	}
	if target >= maxAtHi {
		return hi
	}

	for i := 0; i < 48; i++ {
		mid := (lo + hi) / 2
		value := expectedValueWithUniformShift(series, mid)
		if value < target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func buildProbabilityUpdatesFromState(
	input []probabilityUpdate,
	baselineProb map[int]float64,
	currentProb map[int]float64,
	baselineFixed map[int]bool,
	currentFixed map[int]bool,
) []probabilityUpdate {
	updatesByID := make(map[int]probabilityUpdate)

	for id, base := range baselineProb {
		current := currentProb[id]
		fixedChanged := currentFixed[id] != baselineFixed[id]
		if math.Abs(current-base) <= shapeEpsilon && !fixedChanged {
			continue
		}
		update := probabilityUpdate{
			SubmarketID: id,
			Probability: roundProbability(current),
		}
		if fixedChanged {
			fixed := currentFixed[id]
			update.IsFixed = &fixed
		}
		updatesByID[id] = update
	}

	for _, update := range input {
		if update.IsFixed == nil {
			continue
		}
		if existing, ok := updatesByID[update.SubmarketID]; ok {
			existing.IsFixed = update.IsFixed
			updatesByID[update.SubmarketID] = existing
			continue
		}
		if _, ok := baselineProb[update.SubmarketID]; ok {
			updatesByID[update.SubmarketID] = probabilityUpdate{
				SubmarketID: update.SubmarketID,
				Probability: roundProbability(currentProb[update.SubmarketID]),
				IsFixed:     update.IsFixed,
			}
			continue
		}
		updatesByID[update.SubmarketID] = update
	}

	for _, update := range input {
		if _, ok := baselineProb[update.SubmarketID]; ok {
			continue
		}
		updatesByID[update.SubmarketID] = update
	}

	result := make([]probabilityUpdate, 0, len(updatesByID))
	for _, update := range updatesByID {
		result = append(result, update)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SubmarketID < result[j].SubmarketID })
	return result
}

func printMulticountRemainderNotice(_ string, report multicountRemainderReport, request multicountRemainderRequest) {
	if !report.IsMulticount {
		return
	}

	if request.Enabled() {
		if report.NoopReason != "" {
			output.Warn(fmt.Sprintf("multicount remainder action skipped: %s", report.NoopReason))
			return
		}

		if report.Adjusted && output.IsTTY() && !jsonOutput {
			fmt.Printf(
				"Multicount %s remainder on group %s: %.1f -> %.1f\n",
				report.Action,
				report.Group,
				report.BeforeRaw,
				report.AfterRaw,
			)
		}

		if math.Abs(report.AfterEffective) >= multicountDisplayedRemainderEpsilon {
			output.Warn(
				fmt.Sprintf(
					"multicount remainder still %.1f after %s (constraints/fixed bars may limit full removal)",
					report.AfterRaw,
					report.Action,
				),
			)
		}
		return
	}

	if math.Abs(report.BeforeEffective) < multicountDisplayedRemainderEpsilon {
		return
	}

	action := report.SuggestedAction
	if action == "" {
		return
	}

	group := report.SuggestedGroup
	hint := fmt.Sprintf("rerun with --%s-remainder", action)
	if group != "" {
		hint += " --multicount-group " + group
	}

	output.Warn(
		fmt.Sprintf(
			"multicount remainder is %.1f across forecast; %s",
			report.BeforeRaw,
			hint,
		),
	)
}

func multicountRemainderReportJSON(report multicountRemainderReport) map[string]interface{} {
	if !report.IsMulticount {
		return nil
	}
	out := map[string]interface{}{
		"before":           report.BeforeRaw,
		"before_effective": report.BeforeEffective,
		"after":            report.AfterRaw,
		"after_effective":  report.AfterEffective,
		"group":            report.Group,
		"action":           report.Action,
		"suggested_action": report.SuggestedAction,
		"suggested_group":  report.SuggestedGroup,
		"adjusted":         report.Adjusted,
	}
	if report.NoopReason != "" {
		out["noop_reason"] = report.NoopReason
	}
	return out
}
