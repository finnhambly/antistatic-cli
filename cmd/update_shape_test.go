package cmd

import (
	"math"
	"testing"
)

func TestInterpolateLadder_CountPinsOuterToBaseline(t *testing.T) {
	ids := []int{1, 2, 3, 4}
	baseline := map[int]float64{
		1: 0.1,
		2: 0.2,
		3: 0.3,
		4: 0.4,
	}
	current := map[int]float64{
		1: 0.1,
		2: 0.5, // anchor (+0.3 delta)
		3: 0.3,
		4: 0.4,
	}
	anchor := map[int]bool{2: true}

	interpolateLadder(ids, []int{1}, baseline, current, anchor, true)

	assertAlmostEqual(t, current[1], baseline[1], "left boundary should remain pinned to baseline")
	assertAlmostEqual(t, current[4], baseline[4], "right boundary should remain pinned to baseline")
}

func TestInterpolateLadder_DateExtrapolatesOuterSegments(t *testing.T) {
	ids := []int{1, 2, 3, 4}
	baseline := map[int]float64{
		1: 0.1,
		2: 0.2,
		3: 0.3,
		4: 0.4,
	}
	current := map[int]float64{
		1: 0.1,
		2: 0.5, // anchor (+0.3 delta)
		3: 0.3,
		4: 0.4,
	}
	anchor := map[int]bool{2: true}
	anchorDeltaBits := probToBits(current[2]) - probToBits(baseline[2])

	interpolateLadder(ids, []int{1}, baseline, current, anchor, false)

	assertAlmostEqual(
		t,
		current[1],
		clampProb(bitsToProb(probToBits(baseline[1])+anchorDeltaBits)),
		"left outer segment should extrapolate anchor delta",
	)
	assertAlmostEqual(
		t,
		current[3],
		clampProb(bitsToProb(probToBits(baseline[3])+anchorDeltaBits)),
		"right outer segment should extrapolate anchor delta",
	)
	assertAlmostEqual(
		t,
		current[4],
		clampProb(bitsToProb(probToBits(baseline[4])+anchorDeltaBits)),
		"right tail should not snap back to baseline",
	)
}

func TestBuildShapedProbabilityUpdates_AssignsFixedForAnchorsAndGenerated(t *testing.T) {
	baseline := map[int]float64{
		1: 0.20,
		2: 0.30,
		3: 0.40,
	}
	current := map[int]float64{
		1: 0.25,
		2: 0.35,
		3: 0.45,
	}
	anchor := map[int]bool{
		1: true,
		3: true,
	}
	anchorFixed := map[int]bool{
		1: false,
	}

	input := []probabilityUpdate{
		{SubmarketID: 1, Probability: 0.25, IsFixed: boolPtr(false)},
		{SubmarketID: 3, Probability: 0.45},
	}

	updates := buildShapedProbabilityUpdates(baseline, current, anchor, anchorFixed, input, nil)
	if len(updates) != 3 {
		t.Fatalf("expected three updates, got %d", len(updates))
	}

	byID := make(map[int]probabilityUpdate, len(updates))
	for _, update := range updates {
		byID[update.SubmarketID] = update
	}

	if byID[1].IsFixed == nil || *byID[1].IsFixed {
		t.Fatalf("anchor with explicit fixed=false should remain false")
	}
	if byID[2].IsFixed == nil || *byID[2].IsFixed {
		t.Fatalf("generated non-anchor should be explicit is_fixed=false")
	}
	if byID[3].IsFixed == nil || !*byID[3].IsFixed {
		t.Fatalf("anchor without explicit fixed should default to true")
	}
}

func TestBuildShapedProbabilityUpdates_DefaultsUnspecifiedAnchorToFixed(t *testing.T) {
	baseline := map[int]float64{1: 0.20}
	current := map[int]float64{1: 0.20}
	anchor := map[int]bool{1: true}

	input := []probabilityUpdate{
		{SubmarketID: 1, Probability: 0.20},
	}

	updates := buildShapedProbabilityUpdates(baseline, current, anchor, map[int]bool{}, input, nil)
	if len(updates) != 1 {
		t.Fatalf("expected one update, got %d", len(updates))
	}
	if updates[0].IsFixed == nil || !*updates[0].IsFixed {
		t.Fatalf("expected unchanged anchor to be emitted as is_fixed=true")
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func assertAlmostEqual(t *testing.T, got, want float64, message string) {
	t.Helper()
	if math.Abs(got-want) > 1.0e-9 {
		t.Fatalf("%s: got %.12f want %.12f", message, got, want)
	}
}

func TestBuildCountCrossGroupLadders_AlignsThresholdsAcrossGroups(t *testing.T) {
	groups := []string{"2026-01", "2026-02", "2026-03"}
	forecast := map[string][]forecastPoint{
		"2026-01": {
			{ID: 11, Threshold: floatPtr(10)},
			{ID: 12, Threshold: floatPtr(20)},
		},
		"2026-02": {
			{ID: 21, Threshold: floatPtr(10)},
			{ID: 22, Threshold: floatPtr(20)},
		},
		"2026-03": {
			{ID: 31, Threshold: floatPtr(10)},
			{ID: 32, Threshold: floatPtr(20)},
		},
	}

	ladders := buildCountCrossGroupLadders(groups, forecast)
	if len(ladders) != 2 {
		t.Fatalf("expected 2 cross-group ladders, got %d", len(ladders))
	}

	if !equalIntSlice(ladders[0], []int{11, 21, 31}) {
		t.Fatalf("unexpected first ladder: %#v", ladders[0])
	}
	if !equalIntSlice(ladders[1], []int{12, 22, 32}) {
		t.Fatalf("unexpected second ladder: %#v", ladders[1])
	}
}

func TestBuildCountCrossGroupLadders_UsesThresholdQuantization(t *testing.T) {
	groups := []string{"g1", "g2"}
	forecast := map[string][]forecastPoint{
		"g1": {
			{ID: 1, Threshold: floatPtr(70.0000)},
		},
		"g2": {
			{ID: 2, Threshold: floatPtr(70.0004)},
		},
	}

	ladders := buildCountCrossGroupLadders(groups, forecast)
	if len(ladders) != 1 {
		t.Fatalf("expected 1 ladder, got %d", len(ladders))
	}
	if !equalIntSlice(ladders[0], []int{1, 2}) {
		t.Fatalf("unexpected ladder: %#v", ladders[0])
	}
}

func TestBuildLadders_CountCDFUsesIncreasingMonotonicity(t *testing.T) {
	forecast := map[string][]forecastPoint{
		"g1": {
			{ID: 1, Threshold: floatPtr(100), StartingProbability: floatPtr(0.20)},
			{ID: 2, Threshold: floatPtr(101), StartingProbability: floatPtr(0.70)},
			{ID: 3, Threshold: floatPtr(102), StartingProbability: floatPtr(0.90)},
		},
	}

	ladders := buildLadders("count", false, forecast)
	if len(ladders) != 1 {
		t.Fatalf("expected 1 ladder, got %d", len(ladders))
	}
	if ladders[0].MonotonicDirection != "up" {
		t.Fatalf("expected CDF count ladder to increase, got %q", ladders[0].MonotonicDirection)
	}
}

func TestBuildLadders_CountExceedanceUsesDecreasingMonotonicity(t *testing.T) {
	forecast := map[string][]forecastPoint{
		"g1": {
			{ID: 1, Threshold: floatPtr(100), StartingProbability: floatPtr(0.90)},
			{ID: 2, Threshold: floatPtr(101), StartingProbability: floatPtr(0.70)},
			{ID: 3, Threshold: floatPtr(102), StartingProbability: floatPtr(0.20)},
		},
	}

	ladders := buildLadders("count", false, forecast)
	if len(ladders) != 1 {
		t.Fatalf("expected 1 ladder, got %d", len(ladders))
	}
	if ladders[0].MonotonicDirection != "down" {
		t.Fatalf("expected exceedance count ladder to decrease, got %q", ladders[0].MonotonicDirection)
	}
}

func TestInferASCIIDirection_CountCDFOverridesAPIHint(t *testing.T) {
	points := []asciiPoint{
		{ID: 1, Threshold: floatPtr(100), StartingProbability: floatPtr(0.20)},
		{ID: 2, Threshold: floatPtr(101), StartingProbability: floatPtr(0.70)},
		{ID: 3, Threshold: floatPtr(102), StartingProbability: floatPtr(0.90)},
	}

	if got := inferASCIIDirection(points, "starting", "count", "down"); got != "up" {
		t.Fatalf("expected CDF ASCII direction to override API hint, got %q", got)
	}
}

func TestInferDraftCountDirection_CDF(t *testing.T) {
	points := []draftForecastPoint{
		{ID: 1, Threshold: floatPtr(100), StartingProbability: floatPtr(0.20)},
		{ID: 2, Threshold: floatPtr(101), StartingProbability: floatPtr(0.70)},
		{ID: 3, Threshold: floatPtr(102), StartingProbability: floatPtr(0.90)},
	}

	if got := inferDraftCountDirection(points); got != "up" {
		t.Fatalf("expected CDF draft direction to increase, got %q", got)
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
