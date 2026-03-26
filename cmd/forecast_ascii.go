package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type asciiRenderOptions struct {
	Width     int
	MaxGroups int
	MaxPoints int
	Summary   bool
}

type asciiPoint struct {
	ID                   int      `json:"id"`
	Label                string   `json:"label"`
	CommunityProbability float64  `json:"community_probability"`
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
			return renderASCIISummaryGroups(groupMap, width, maxGroups)
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
			renderASCIIGroup(groupName, points, width, maxPoints, monotonicDirection, cumulative)
			maxRenderedGroups++
		}
		return nil
	}

	if opts.Summary {
		return renderASCIISummarySeries("curve", singleSeries, width)
	}

	sortASCIIPoints(singleSeries)
	renderASCIIGroup("curve", singleSeries, width, maxPoints, monotonicDirection, cumulative)
	return nil
}

func renderASCIISummaryGroups(groupMap map[string][]asciiPoint, width, maxGroups int) error {
	groupNames := make([]string, 0, len(groupMap))
	for name := range groupMap {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	fmt.Println("\nASCII summary (last point per group):")
	rendered := 0
	for _, groupName := range groupNames {
		if rendered >= maxGroups {
			fmt.Printf("(only first %d groups shown; increase --ascii-max-groups)\n", maxGroups)
			break
		}
		points := append([]asciiPoint(nil), groupMap[groupName]...)
		sortASCIIPoints(points)
		printASCIISummaryLine(groupName, points, width)
		rendered++
	}
	return nil
}

func renderASCIISummarySeries(name string, points []asciiPoint, width int) error {
	fmt.Println("\nASCII summary:")
	sortASCIIPoints(points)
	printASCIISummaryLine(name, points, width)
	return nil
}

func printASCIISummaryLine(groupName string, points []asciiPoint, width int) {
	if len(points) == 0 {
		fmt.Printf("%-12s n/a\n", groupName)
		return
	}

	last := points[len(points)-1]
	prob := clampProb(last.CommunityProbability)
	barCount := int(math.Round(prob * float64(width)))
	if barCount < 0 {
		barCount = 0
	}
	if barCount > width {
		barCount = width
	}
	bar := strings.Repeat("#", barCount) + strings.Repeat(".", width-barCount)

	label := last.Label
	if len(label) > 24 {
		label = label[:21] + "..."
	}

	fmt.Printf(
		"%-12s %6.2f%% |%s|  %s (%d points)\n",
		groupName,
		prob*100,
		bar,
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
) {
	fmt.Printf("\nGroup: %s\n", groupName)
	printMonotonicExpectation(direction, cumulative)

	violations := monotonicViolations(points, direction)
	if len(violations) == 0 {
		fmt.Println("Monotonic check: ok")
	} else {
		fmt.Printf("Monotonic check: %d violation(s)\n", len(violations))
		preview := violations
		if len(preview) > 8 {
			preview = preview[:8]
		}
		for _, v := range preview {
			fmt.Printf("  ! idx %d -> %d (%.2f%% -> %.2f%%)\n", v.prevIndex, v.currIndex, v.prevProb*100, v.currProb*100)
		}
		if len(violations) > len(preview) {
			fmt.Printf("  ... %d more\n", len(violations)-len(preview))
		}
	}

	printPoints := points
	if len(printPoints) > maxPoints {
		printPoints = printPoints[:maxPoints]
	}

	for idx, point := range printPoints {
		label := point.Label
		if len(label) > 30 {
			label = label[:27] + "..."
		}
		barCount := int(math.Round(clampProb(point.CommunityProbability) * float64(width)))
		if barCount < 0 {
			barCount = 0
		}
		if barCount > width {
			barCount = width
		}
		bar := strings.Repeat("#", barCount) + strings.Repeat(".", width-barCount)
		fmt.Printf(
			"[%02d] %-30s %6.2f%% |%s|\n",
			idx,
			label,
			clampProb(point.CommunityProbability)*100,
			bar,
		)
	}

	if len(points) > len(printPoints) {
		fmt.Printf("(showing first %d/%d points; increase --ascii-max-points)\n", len(printPoints), len(points))
	}
}

type monotonicViolation struct {
	prevIndex int
	currIndex int
	prevProb  float64
	currProb  float64
}

func monotonicViolations(points []asciiPoint, direction string) []monotonicViolation {
	if len(points) < 2 {
		return nil
	}

	out := make([]monotonicViolation, 0)
	for i := 1; i < len(points); i++ {
		prev := clampProb(points[i-1].CommunityProbability)
		curr := clampProb(points[i].CommunityProbability)

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

func printMonotonicExpectation(direction string, cumulative bool) {
	switch direction {
	case "down":
		fmt.Println("Expected monotonicity: non-increasing (higher thresholds should not have higher probabilities)")
	case "up":
		if cumulative {
			fmt.Println("Expected monotonicity: non-decreasing (later dates should not have lower probabilities)")
		} else {
			fmt.Println("Expected monotonicity: non-decreasing")
		}
	default:
		fmt.Println("Expected monotonicity: n/a")
	}
}
