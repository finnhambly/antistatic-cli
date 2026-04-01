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
