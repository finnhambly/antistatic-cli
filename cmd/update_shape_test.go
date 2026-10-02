package cmd

import (
	"testing"
)

func TestProbabilityUpdatesToPayload_UsesSubmarketRefAndDecimalString(t *testing.T) {
	payload := probabilityUpdatesToPayload([]probabilityUpdate{
		{SubmarketID: 42, Probability: 0.75},
	})

	if got := payload[0]["submarket"]; got != "sm_42" {
		t.Fatalf("unexpected submarket ref: %v", got)
	}
	if got := payload[0]["probability"]; got != "0.75" {
		t.Fatalf("unexpected probability payload: %v", got)
	}
	if _, exists := payload[0]["submarket_id"]; exists {
		t.Fatalf("payload should prefer submarket over submarket_id")
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
