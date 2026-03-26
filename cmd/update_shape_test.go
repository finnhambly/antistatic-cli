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

func assertAlmostEqual(t *testing.T, got, want float64, message string) {
	t.Helper()
	if math.Abs(got-want) > 1.0e-9 {
		t.Fatalf("%s: got %.12f want %.12f", message, got, want)
	}
}
