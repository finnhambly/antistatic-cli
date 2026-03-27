package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

type asciiRenderOptions struct {
	Width     int
	MaxGroups int
	MaxPoints int
	Summary   bool
	Basis     string
}

type asciiPoint struct {
	ID                   int      `json:"id"`
	Label                string   `json:"label"`
	Probability          *float64 `json:"probability"`
	CommunityProbability *float64 `json:"community_probability"`
	StartingProbability  *float64 `json:"starting_probability"`
	Threshold            *float64 `json:"threshold"`
	ThresholdDate        string   `json:"threshold_date"`
}

func renderASCIIForecast(data json.RawMessage, opts asciiRenderOptions) error {
	width := opts.Width
	if width < 8 {
		width = 8
	}
	maxGroups := opts.MaxGroups
	if maxGroups <= 0 {
		maxGroups = 1
	}
	maxPoints := opts.MaxPoints
	if maxPoints <= 0 {
		maxPoints = 20
	}
	basis := strings.ToLower(strings.TrimSpace(opts.Basis))
	if basis == "" {
		basis = "starting"
	}
	if basis != "starting" && basis != "community" {
		return fmt.Errorf("invalid --ascii-basis %q (expected: starting or community)", opts.Basis)
	}

	var payload struct {
		Code          string          `json:"code"`
		Title         string          `json:"title"`
		Type          string          `json:"type"`
		ProbDirection string          `json:"prob_direction"`
		Cumulative    *bool           `json:"cumulative"`
		Forecast      json.RawMessage `json:"forecast"`
		Groups        []struct {
			Group string `json:"group"`
		} `json:"groups"`
		Hint   string `json:"hint"`
		Market struct {
			Code       string `json:"code"`
			Title      string `json:"title"`
			Type       string `json:"type"`
			Cumulative *bool  `json:"cumulative"`
		} `json:"market"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("parsing forecast response: %w", err)
	}

	code := payload.Code
	title := payload.Title
	marketType := payload.Type
	var cumulative bool
	if payload.Cumulative != nil {
		cumulative = *payload.Cumulative
	}

	if title == "" && payload.Market.Title != "" {
		title = payload.Market.Title
	}
	if code == "" && payload.Market.Code != "" {
		code = payload.Market.Code
	}
	if marketType == "" && payload.Market.Type != "" {
		marketType = payload.Market.Type
	}
	if payload.Cumulative == nil && payload.Market.Cumulative != nil {
		cumulative = *payload.Market.Cumulative
	}

	if title != "" {
		fmt.Printf("%s (%s)\n", title, code)
	}

	groupMap, singleSeries, err := parseForecastForASCII(payload.Forecast)
	if err != nil {
		return err
	}

	if len(groupMap) == 0 && len(singleSeries) == 0 {
		if payload.Hint != "" {
			fmt.Println(payload.Hint)
		}
		if len(payload.Groups) > 0 {
			fmt.Println("ASCII plot unavailable for indexed/summary forecast. Re-run with --include full.")
			return nil
		}
		fmt.Println("No plottable forecast series.")
		return nil
	}

	monotonicDirection := expectedMonotonicDirection(payload.ProbDirection, marketType)
	maxRenderedGroups := 0

	if len(groupMap) > 0 {
		if opts.Summary {
			return renderASCIISummaryGroups(groupMap, width, maxGroups, basis)
		}

		groupNames := make([]string, 0, len(groupMap))
		for name := range groupMap {
			groupNames = append(groupNames, name)
		}
		sort.Strings(groupNames)

		for _, groupName := range groupNames {
			if maxRenderedGroups >= maxGroups {
				fmt.Printf("\n(only first %d groups shown; increase --ascii-max-groups)\n", maxGroups)
				break
			}
			points := append([]asciiPoint(nil), groupMap[groupName]...)
			sortASCIIPoints(points)
			renderASCIIGroup(groupName, points, width, maxPoints, monotonicDirection, cumulative, basis)
			maxRenderedGroups++
		}
		return nil
	}

	if opts.Summary {
		return renderASCIISummarySeries("curve", singleSeries, width, basis)
	}

	sortASCIIPoints(singleSeries)
	renderASCIIGroup("curve", singleSeries, width, maxPoints, monotonicDirection, cumulative, basis)
	return nil
}

func renderASCIISummaryGroups(groupMap map[string][]asciiPoint, width, maxGroups int, basis string) error {
	groupNames := make([]string, 0, len(groupMap))
	for name := range groupMap {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	fmt.Printf("\nASCII summary (last point per group, basis=%s):\n", basisLabel(basis))
	rendered := 0
	for _, groupName := range groupNames {
		if rendered >= maxGroups {
			fmt.Printf("(only first %d groups shown; increase --ascii-max-groups)\n", maxGroups)
			break
		}
		points := append([]asciiPoint(nil), groupMap[groupName]...)
		sortASCIIPoints(points)
		printASCIISummaryLine(groupName, points, width, basis)
		rendered++
	}
	return nil
}

func renderASCIISummarySeries(name string, points []asciiPoint, width int, basis string) error {
	fmt.Printf("\nASCII summary (basis=%s):\n", basisLabel(basis))
	sortASCIIPoints(points)
	printASCIISummaryLine(name, points, width, basis)
	return nil
}

func printASCIISummaryLine(groupName string, points []asciiPoint, width int, basis string) {
	if len(points) == 0 {
		fmt.Printf("%-12s n/a\n", groupName)
		return
	}

	last := points[len(points)-1]
	prob := clampProb(pointProbabilityForBasis(last, basis))
	barCount := int(math.Round(prob * float64(width)))
	if barCount < 0 {
		barCount = 0
	}
	if barCount > width {
		barCount = width
	}
	bar := strings.Repeat("█", barCount) + strings.Repeat("░", width-barCount)

	label := compactASCIIPointLabel(last)
	if len(label) > 18 {
		label = label[:15] + "..."
	}

	fmt.Printf(
		"%-12s │%s│ %5.1f%%  %s (%d)\n",
		groupName,
		bar,
		prob*100,
		label,
		len(points),
	)
}

func parseForecastForASCII(raw json.RawMessage) (map[string][]asciiPoint, []asciiPoint, error) {
	var grouped map[string][]asciiPoint
	if err := json.Unmarshal(raw, &grouped); err == nil {
		return grouped, nil, nil
	}

	var list []asciiPoint
	if err := json.Unmarshal(raw, &list); err == nil {
		return nil, list, nil
	}

	return nil, nil, fmt.Errorf("ASCII plot requires full forecast submarket data")
}

func sortASCIIPoints(points []asciiPoint) {
	sort.Slice(points, func(i, j int) bool {
		li := sortKeyForPoint(points[i])
		lj := sortKeyForPoint(points[j])
		if li == lj {
			return points[i].ID < points[j].ID
		}
		return li < lj
	})
}

func sortKeyForPoint(point asciiPoint) float64 {
	if point.Threshold != nil {
		return *point.Threshold
	}
	if point.ThresholdDate != "" {
		if parsed, err := time.Parse(time.RFC3339, point.ThresholdDate); err == nil {
			return float64(parsed.Unix())
		}
	}
	return math.MaxFloat64
}

func expectedMonotonicDirection(probDirection, marketType string) string {
	if strings.EqualFold(probDirection, "decreasing") {
		return "down"
	}
	if strings.EqualFold(probDirection, "increasing") {
		return "up"
	}
	if strings.EqualFold(marketType, "count") {
		return "down"
	}
	return "up"
}

func renderASCIIGroup(
	groupName string,
	points []asciiPoint,
	width int,
	maxPoints int,
	direction string,
	cumulative bool,
	basis string,
) {
	fmt.Printf("\nGroup: %s\n", friendlyASCIIGroupName(groupName, points))

	violations := monotonicViolations(points, direction, basis)
	if len(violations) > 0 {
		fmt.Printf("Warning: %d monotonicity violation(s) for %s.\n", len(violations), basisLabel(basis))
	}

	printPoints := points
	if len(printPoints) > maxPoints {
		printPoints = printPoints[:maxPoints]
	}

	maxLabelWidth := 0
	labels := make([]string, 0, len(printPoints))
	for _, point := range printPoints {
		label := compactASCIIPointLabel(point)
		if label == "" {
			label = "?"
		}
		labels = append(labels, label)
		if len(label) > maxLabelWidth {
			maxLabelWidth = len(label)
		}
	}
	if maxLabelWidth > 22 {
		maxLabelWidth = 22
	}

	for idx, point := range printPoints {
		label := labels[idx]
		if len(label) > maxLabelWidth {
			label = label[:maxLabelWidth]
		}

		prob := clampProb(pointProbabilityForBasis(point, basis))
		barCount := int(math.Round(prob * float64(width)))
		if barCount < 0 {
			barCount = 0
		}
		if barCount > width {
			barCount = width
		}
		bar := strings.Repeat("█", barCount) + strings.Repeat("░", width-barCount)
		fmt.Printf(
			"  %-*s │%s│ %5.1f%%\n",
			maxLabelWidth,
			label,
			bar,
			prob*100,
		)
	}

	if len(points) > len(printPoints) {
		fmt.Printf("(showing first %d/%d points; increase --ascii-max-points)\n", len(printPoints), len(points))
	}

	_ = cumulative
}

type monotonicViolation struct {
	prevIndex int
	currIndex int
	prevProb  float64
	currProb  float64
}

func monotonicViolations(points []asciiPoint, direction, basis string) []monotonicViolation {
	if len(points) < 2 {
		return nil
	}

	out := make([]monotonicViolation, 0)
	for i := 1; i < len(points); i++ {
		prev := clampProb(pointProbabilityForBasis(points[i-1], basis))
		curr := clampProb(pointProbabilityForBasis(points[i], basis))

		bad := false
		if direction == "down" {
			bad = curr > prev+1.0e-9
		} else {
			bad = curr+1.0e-9 < prev
		}

		if bad {
			out = append(out, monotonicViolation{
				prevIndex: i - 1,
				currIndex: i,
				prevProb:  prev,
				currProb:  curr,
			})
		}
	}
	return out
}

func pointProbabilityForBasis(point asciiPoint, basis string) float64 {
	switch basis {
	case "community":
		if point.CommunityProbability != nil {
			return *point.CommunityProbability
		}
		if point.Probability != nil {
			return *point.Probability
		}
		if point.StartingProbability != nil {
			return *point.StartingProbability
		}
		return 0
	case "starting":
		if point.StartingProbability != nil {
			return *point.StartingProbability
		}
		if point.Probability != nil {
			return *point.Probability
		}
		if point.CommunityProbability != nil {
			return *point.CommunityProbability
		}
		return 0
	default:
		if point.CommunityProbability != nil {
			return *point.CommunityProbability
		}
		return 0
	}
}

func basisLabel(basis string) string {
	if basis == "starting" {
		return "starting_probability (trade line)"
	}
	return "community aggregate (context only)"
}

func friendlyASCIIGroupName(groupName string, points []asciiPoint) string {
	if len(points) == 0 {
		return groupName
	}
	prefix := labelPrefixBeforeComma(points[0].Label)
	if prefix != "" && looksLikeTimestampGroup(groupName) {
		return prefix
	}
	return groupName
}

func compactASCIIPointLabel(point asciiPoint) string {
	label := strings.TrimSpace(point.Label)
	if label == "" {
		if point.Threshold != nil {
			return fmt.Sprintf("%.3g", *point.Threshold)
		}
		return ""
	}

	if idx := strings.LastIndex(label, ","); idx >= 0 {
		label = strings.TrimSpace(label[idx+1:])
	}

	re := regexp.MustCompile(`^(>=|<=|>|<)\s*`)
	label = re.ReplaceAllString(label, "")
	label = strings.TrimSpace(label)
	label = strings.Join(strings.Fields(label), " ")
	return label
}

func labelPrefixBeforeComma(label string) string {
	idx := strings.Index(label, ",")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(label[:idx])
}

func looksLikeTimestampGroup(groupName string) bool {
	if len(groupName) < 10 {
		return false
	}
	return strings.Contains(groupName, "T") && strings.Count(groupName, "-") >= 2
}
