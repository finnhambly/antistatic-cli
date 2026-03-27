package cmd

import "testing"

func TestParseProbabilityUpdates_AcceptsTypedMapSlice(t *testing.T) {
	raw := []map[string]interface{}{
		{
			"submarket_id": 101,
			"probability":  0.42,
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
