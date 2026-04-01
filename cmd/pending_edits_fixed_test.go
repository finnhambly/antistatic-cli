package cmd

import "testing"

func TestMarkDraftPlanInterpolationAnchors_MarksFirstAndLast(t *testing.T) {
	lines := []draftPlanLine{
		{SubmarketID: 1},
		{SubmarketID: 2},
		{SubmarketID: 3},
	}

	markDraftPlanInterpolationAnchors(lines, true)

	if !lines[0].IsAnchor {
		t.Fatalf("expected first line to be anchor")
	}
	if lines[1].IsAnchor {
		t.Fatalf("expected middle line to remain non-anchor")
	}
	if !lines[2].IsAnchor {
		t.Fatalf("expected last line to be anchor")
	}
}

func TestMarkDraftPlanInterpolationAnchors_NoopWhenDisabled(t *testing.T) {
	lines := []draftPlanLine{
		{SubmarketID: 1},
		{SubmarketID: 2},
	}

	markDraftPlanInterpolationAnchors(lines, false)

	if lines[0].IsAnchor || lines[1].IsAnchor {
		t.Fatalf("expected no anchors when interpolation is disabled")
	}
}

func TestDraftPlanAsProbabilityUpdates_AnchorsFixedAndGeneratedUnfixed(t *testing.T) {
	plan := []draftPlanLine{
		{SubmarketID: 10, Probability: 0.30, IsAnchor: true},
		{SubmarketID: 11, Probability: 0.40, IsAnchor: false},
		{SubmarketID: 12, Probability: 0.50, IsAnchor: true},
	}

	updates := draftPlanAsProbabilityUpdates(plan)
	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}

	if updates[0].IsFixed == nil || !*updates[0].IsFixed {
		t.Fatalf("expected first anchor to be fixed")
	}
	if updates[1].IsFixed == nil || *updates[1].IsFixed {
		t.Fatalf("expected generated middle point to be unfixed")
	}
	if updates[2].IsFixed == nil || !*updates[2].IsFixed {
		t.Fatalf("expected last anchor to be fixed")
	}
}
