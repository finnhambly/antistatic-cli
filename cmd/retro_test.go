package cmd

import "testing"

func TestFormatRetroDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2019-06-01T00:00:00Z", "1 June 2019"},
		{"2020-12-31T00:00:00Z", "31 December 2020"},
		// A bare date, in case the server ever sends one.
		{"2021-12-31", "2021-12-31"},
		{"", "—"},
	}

	for _, tc := range cases {
		if got := formatRetroDate(tc.in); got != tc.want {
			t.Errorf("formatRetroDate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateRetro(t *testing.T) {
	if got := truncateRetro("short", 10); got != "short" {
		t.Errorf("expected untouched, got %q", got)
	}

	got := truncateRetro("a question longer than the limit", 10)
	if len([]rune(got)) != 10 {
		t.Errorf("expected 10 runes, got %d (%q)", len([]rune(got)), got)
	}
}

// The next checkpoint must carry no learner-facing metadata: even its date can
// reveal the timing of a selected paper or event. Guard the shape we decode.
func TestRetroRunHasNoFutureMetadata(t *testing.T) {
	var run retroRun
	if _, ok := any(&run).(interface{ NextTitle() string }); ok {
		t.Fatal("retroRun should not expose a title for the next checkpoint")
	}
	if _, ok := any(&run).(interface{ NextOccurredAt() string }); ok {
		t.Fatal("retroRun should not expose a date for the next checkpoint")
	}
}
