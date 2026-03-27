package cmd

import "testing"

func TestResolveUpdateLabelsInBody_AcceptsTypedMapSlice(t *testing.T) {
	body := map[string]interface{}{
		"updates": []map[string]interface{}{
			{
				"submarket_id": 55,
				"probability":  0.33,
			},
		},
	}

	if err := resolveUpdateLabelsInBody("ignored", body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
