package cmd

import "testing"

func TestBuildProbabilityUpdatesFromState_ChangedRowsCarryExplicitFixed(t *testing.T) {
	result := buildProbabilityUpdatesFromState(
		nil,
		map[int]float64{1: 0.20},
		map[int]float64{1: 0.25},
		map[int]bool{1: false},
		map[int]bool{1: false},
	)

	if len(result) != 1 {
		t.Fatalf("expected one update, got %d", len(result))
	}
	if result[0].IsFixed == nil {
		t.Fatalf("expected explicit is_fixed on changed row")
	}
	if *result[0].IsFixed {
		t.Fatalf("expected fixed=false to be preserved")
	}
}

func TestBuildProbabilityUpdatesFromState_FixedOnlyChangesCarryExplicitFixed(t *testing.T) {
	result := buildProbabilityUpdatesFromState(
		nil,
		map[int]float64{1: 0.20},
		map[int]float64{1: 0.20},
		map[int]bool{1: false},
		map[int]bool{1: true},
	)

	if len(result) != 1 {
		t.Fatalf("expected one update, got %d", len(result))
	}
	if result[0].IsFixed == nil || !*result[0].IsFixed {
		t.Fatalf("expected fixed-only change to emit is_fixed=true")
	}
}

func TestBuildProbabilityUpdatesFromState_InputFixedOverridesGeneratedState(t *testing.T) {
	override := true
	input := []probabilityUpdate{
		{SubmarketID: 1, Probability: 0.25, IsFixed: &override},
	}

	result := buildProbabilityUpdatesFromState(
		input,
		map[int]float64{1: 0.20},
		map[int]float64{1: 0.25},
		map[int]bool{1: false},
		map[int]bool{1: false},
	)

	if len(result) != 1 {
		t.Fatalf("expected one update, got %d", len(result))
	}
	if result[0].IsFixed == nil || !*result[0].IsFixed {
		t.Fatalf("expected explicit input fixed=true to override generated state")
	}
}
