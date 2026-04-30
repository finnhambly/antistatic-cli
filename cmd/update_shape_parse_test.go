package cmd

import "testing"

func TestParseProbabilityUpdates_AcceptsTypedMapSlice(t *testing.T) {
	raw := []map[string]interface{}{
		{
			"submarket":   "sm_101",
			"probability": 0.42,
		},
	}

	updates, err := parseProbabilityUpdates(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected one update, got %d", len(updates))
	}
	if updates[0].SubmarketID != 101 {
		t.Fatalf("unexpected submarket id: %d", updates[0].SubmarketID)
	}
	if updates[0].Probability != 0.42 {
		t.Fatalf("unexpected probability: %.6f", updates[0].Probability)
	}
}

func TestParseProbabilityUpdates_AcceptsSubmarketRefAndDecimalString(t *testing.T) {
	raw := []map[string]interface{}{
		{
			"submarket":   "sm_101",
			"probability": "0.42",
		},
	}

	updates, err := parseProbabilityUpdates(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected one update, got %d", len(updates))
	}
	if updates[0].SubmarketID != 101 {
		t.Fatalf("unexpected submarket id: %d", updates[0].SubmarketID)
	}
	if updates[0].Probability != 0.42 {
		t.Fatalf("unexpected probability: %.6f", updates[0].Probability)
	}
}

func TestParseProbabilityUpdatesWithDefault_DefaultsMissingIsFixedTrue(t *testing.T) {
	defaultFixed := true
	raw := []map[string]interface{}{
		{
			"submarket":   "sm_11",
			"probability": 0.33,
		},
	}

	updates, err := parseProbabilityUpdatesWithDefault(raw, &defaultFixed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected one update, got %d", len(updates))
	}
	if updates[0].IsFixed == nil {
		t.Fatalf("expected is_fixed to be defaulted")
	}
	if !*updates[0].IsFixed {
		t.Fatalf("expected defaulted is_fixed=true")
	}
}

func TestParseProbabilityUpdatesWithDefault_PreservesExplicitFalse(t *testing.T) {
	defaultFixed := true
	raw := []map[string]interface{}{
		{
			"submarket":   "sm_12",
			"probability": 0.44,
			"is_fixed":    false,
		},
		{
			"submarket":   "sm_13",
			"probability": 0.55,
			"isFixed":     false,
		},
	}

	updates, err := parseProbabilityUpdatesWithDefault(raw, &defaultFixed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected two updates, got %d", len(updates))
	}
	for i, update := range updates {
		if update.IsFixed == nil {
			t.Fatalf("update %d: expected explicit is_fixed to be preserved", i)
		}
		if *update.IsFixed {
			t.Fatalf("update %d: expected explicit false to be preserved", i)
		}
	}
}

func TestParseProbabilityUpdates_RejectsLegacySubmarketID(t *testing.T) {
	raw := []map[string]interface{}{
		{
			"submarket_id": 101,
			"probability":  0.42,
		},
	}

	if _, err := parseProbabilityUpdates(raw); err == nil {
		t.Fatalf("expected legacy submarket_id payload to be rejected")
	}
}
