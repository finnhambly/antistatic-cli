package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatSandboxDate(t *testing.T) {
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
		if got := formatSandboxDate(tc.in); got != tc.want {
			t.Errorf("formatSandboxDate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateSandbox(t *testing.T) {
	if got := truncateSandbox("short", 10); got != "short" {
		t.Errorf("expected untouched, got %q", got)
	}

	got := truncateSandbox("a question longer than the limit", 10)
	if len([]rune(got)) != 10 {
		t.Errorf("expected 10 runes, got %d (%q)", len([]rune(got)), got)
	}
}

// The next checkpoint must carry no learner-facing metadata: even its date can
// reveal the timing of a selected paper or event. Guard the shape we decode.
func TestSandboxRunHasNoFutureMetadata(t *testing.T) {
	var run sandboxRun
	if _, ok := any(&run).(interface{ NextTitle() string }); ok {
		t.Fatal("sandboxRun should not expose a title for the next checkpoint")
	}
	if _, ok := any(&run).(interface{ NextOccurredAt() string }); ok {
		t.Fatal("sandboxRun should not expose a date for the next checkpoint")
	}
}

// "sandbox" is the command; "timelines" and "retro", its old names, are
// hidden copies of the same tree.
func TestSandboxOldNamesAreHiddenCopies(t *testing.T) {
	for _, name := range []string{"sandbox", "timelines", "retro"} {
		command, _, err := rootCmd.Find([]string{name})
		if err != nil || command.Name() != name {
			t.Fatalf("%s: not found (%v)", name, err)
		}
		if command.Hidden != (name != "sandbox") {
			t.Errorf("%s: Hidden = %v", name, command.Hidden)
		}
		for _, sub := range []string{"status", "advance", "push", "check", "publish", "grant"} {
			if found, _, err := command.Find([]string{sub}); err != nil || found.Name() != sub {
				t.Errorf("%s %s: not found", name, sub)
			}
		}
	}
}

// Without --curriculum, status shows the caller's runs.
func TestSandboxStatusWithoutCurriculumListsRuns(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"run_id":13,"status":"active","curriculum":{"slug":"rnai","title":"RNAi"},"checkpoint":{"key":"cp000","sequence":0,"occurred_at":"2001-05-25T00:00:00Z","title":"Briefing"},"questions":[]}]}`))
	}))
	defer server.Close()

	out, _, err := runSandbox(t, server, "status")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "GET /api/v1/sandbox/runs" {
		t.Fatalf("requests = %v", paths)
	}
	if !strings.Contains(out, "rnai") {
		t.Errorf("output %q doesn't name the run", out)
	}

	if _, _, err := runSandbox(t, server, "status", "rnai", "--curriculum", "rnai"); err == nil {
		t.Error("a slug and --curriculum together should be refused")
	}
}
