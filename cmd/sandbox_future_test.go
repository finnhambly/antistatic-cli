package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func bodyJSON(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeTempNamed(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sandboxCmd() *cobra.Command { return newSandboxCmd("sandbox", false) }

func TestSandboxStart(t *testing.T) {
	chars := writeTempNamed(t, "chars.json", `[{"name":"Ana","tagline":"PM","bio":"secret"}]`)
	requests, err := runAgainstServer(t, sandboxCmd(),
		[]string{"start", "--template", "wargame-your-life", "--bio", "A nurse", "--planner", "client", "--mode", "god", "--characters", chars},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			w.WriteHeader(201)
			w.Write([]byte(`{"data":{"run_id":712,"kind":"future","clock":"2026-10-10"}}`))
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].method != "POST" || requests[0].path != "/api/v1/sandbox/runs" {
		t.Fatalf("requests = %+v", requests)
	}
	want := `{"bio":"A nurse","characters":[{"bio":"secret","name":"Ana","tagline":"PM"}],"mode":"god","planner":"client","template":"wargame-your-life"}`
	if got := bodyJSON(t, requests[0].body); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	if _, err := runAgainstServer(t, sandboxCmd(), []string{"start"}, nil); err == nil {
		t.Error("start without a template, premise or curriculum should be refused")
	}
}

func TestSandboxTurn(t *testing.T) {
	var progress bytes.Buffer
	prev := progressOut
	progressOut = &progress
	defer func() { progressOut = prev }()

	reply := func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
		w.Write([]byte(`{"data":{"run_id":712,"status":"planner_job","job_id":"pj_1","turn_id":"x"}}`))
	}

	// No --turn-id: one is generated and printed for a retry.
	requests, err := runAgainstServer(t, sandboxCmd(),
		[]string{"turn", "712", "--action", "Ask for a raise", "--decree", "Move to Leeds", "--character", "c2", "--info", "When is my review?", "--until", "auto"}, reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].method != "POST" || requests[0].path != "/api/v1/sandbox/runs/712/turns" {
		t.Fatalf("requests = %+v", requests)
	}
	turnID, _ := requests[0].body["turn_id"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(turnID) {
		t.Errorf("turn_id = %q, want a UUID", turnID)
	}
	if !strings.Contains(progress.String(), "--turn-id "+turnID) {
		t.Errorf("stderr %q doesn't give the turn id", progress.String())
	}
	entries, _ := json.Marshal(requests[0].body["entries"])
	want := `[{"character":"c2","text":"Ask for a raise","type":"action"},{"character":"c2","text":"Move to Leeds","type":"decree"},{"text":"When is my review?","type":"info"}]`
	if string(entries) != want {
		t.Errorf("entries = %s, want %s", entries, want)
	}
	if requests[0].body["until"] != "auto" {
		t.Errorf("until = %v", requests[0].body["until"])
	}

	// A retry reuses the id; --entries and --answer build the entries.
	file := writeTempNamed(t, "entries.json", `[{"type":"action","text":"Ask for a raise"}]`)
	progress.Reset()
	requests, err = runAgainstServer(t, sandboxCmd(),
		[]string{"turn", "712", "--turn-id", turnID, "--entries", file, "--answer", "q1=Yes, in March"}, reply)
	if err != nil {
		t.Fatal(err)
	}
	if requests[0].body["turn_id"] != turnID {
		t.Errorf("turn_id = %v, want %s", requests[0].body["turn_id"], turnID)
	}
	if progress.Len() != 0 {
		t.Errorf("a given turn id shouldn't be announced: %q", progress.String())
	}
	entries, _ = json.Marshal(requests[0].body["entries"])
	want = `[{"text":"Ask for a raise","type":"action"},{"answers":"q1","text":"Yes, in March","type":"feed"}]`
	if string(entries) != want {
		t.Errorf("entries = %s, want %s", entries, want)
	}
}

func TestPrintTurnNextSteps(t *testing.T) {
	out := captureStdout(t, func() {
		printTurn(712, "t1", jsonMap{"status": "planner_job", "job_id": "pj_9", "expires_at": "soon"})
	})
	if !strings.Contains(out, "planner job pj_9") || !strings.Contains(out, "--turn-id t1") {
		t.Errorf("planner_job output = %q", out)
	}
	out = captureStdout(t, func() {
		printTurn(712, "t1", jsonMap{"status": "done", "script": "**A month**\n\nWhat do you do?"})
	})
	if !strings.HasPrefix(out, "**A month**\n\nWhat do you do?\n") {
		t.Errorf("script not printed verbatim: %q", out)
	}
}

func TestSandboxOrders(t *testing.T) {
	reply := func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
		w.Write([]byte(`{"data":{"run_id":712,"character":"c3","entries":[],"submitted_at":null}}`))
	}
	requests, err := runAgainstServer(t, sandboxCmd(),
		[]string{"orders", "712", "--character", "c3", "--action", "Announce a pause", "--draft"}, reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].method != "PUT" || requests[0].path != "/api/v1/sandbox/runs/712/orders" {
		t.Fatalf("requests = %+v", requests)
	}
	want := `{"character":"c3","entries":[{"text":"Announce a pause","type":"action"}],"submit":false}`
	if got := bodyJSON(t, requests[0].body); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	// Without entries, orders reads them.
	requests, err = runAgainstServer(t, sandboxCmd(), []string{"orders", "712", "--character", "c3"}, reply)
	if err != nil {
		t.Fatal(err)
	}
	if requests[0].method != "GET" || requests[0].query != "character=c3" {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestSandboxSetupEndsOnNone(t *testing.T) {
	requests, err := runAgainstServer(t, sandboxCmd(),
		[]string{"setup", "712", "--ends-on", "none", "--pace", "month", "--scout=false"},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			w.Write([]byte(`{"data":{"run_id":712,"kind":"future"}}`))
		})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ends_on":null,"pace":"month","scout":false}`
	if got := bodyJSON(t, requests[0].body); requests[0].method != "PATCH" || got != want {
		t.Errorf("%s body = %s, want %s", requests[0].method, got, want)
	}
}

func TestSandboxDeleteRun(t *testing.T) {
	requests, err := runAgainstServer(t, sandboxCmd(), []string{"delete", "712", "-y"},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			w.Write([]byte(`{"data":{"run_id":712,"deleted":true}}`))
		})
	if err != nil {
		t.Fatal(err)
	}
	if requests[0].method != "DELETE" || requests[0].path != "/api/v1/sandbox/runs/712" || bodyJSON(t, requests[0].body) != `{"confirm":true}` {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestSandboxPlanner(t *testing.T) {
	plan := writeTempNamed(t, "plan.json", `{"branches":[]}`)
	cases := []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"planner", "job", "pj_1"}, "GET", "/api/v1/sandbox/planner_jobs/pj_1", "null"},
		{[]string{"planner", "plan", "pj_1", "--plan", plan}, "POST", "/api/v1/sandbox/planner_jobs/pj_1/plan", `{"plan":{"branches":[]}}`},
		{[]string{"planner", "notes", "pj_1"}, "GET", "/api/v1/sandbox/planner_jobs/pj_1/notes", "null"},
	}
	for _, tc := range cases {
		var out string
		var requests []recordedRequest
		var err error
		out = captureStdout(t, func() {
			requests, err = runAgainstServer(t, sandboxCmd(), tc.args,
				func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
					w.Write([]byte(`{"data":{"job_id":"pj_1","status":"open"}}`))
				})
		})
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if len(requests) != 1 || requests[0].method != tc.method || requests[0].path != tc.path || bodyJSON(t, requests[0].body) != tc.body {
			t.Errorf("%v: requests = %+v", tc.args, requests)
		}
		if !strings.Contains(out, `"job_id": "pj_1"`) {
			t.Errorf("%v: output %q isn't the JSON", tc.args, out)
		}
	}
}

func TestMarketExtend(t *testing.T) {
	anchors := writeTempNamed(t, "anchors.json", `[{"period":"2026-11","quantiles":{"10":15,"50":35,"90":60}}]`)
	requests, err := runAgainstServer(t, newMarketExtendCmd(),
		[]string{"~12", "--to-threshold", "70", "--to-period", "2027-06", "--anchors", anchors, "--preview"},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			w.Write([]byte(`{"data":{"preview":true,"added":2,"settled":0,"needs_value":[],"bars":[]}}`))
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].method != "POST" || requests[0].path != "/api/v1/markets/~12/extend" {
		t.Fatalf("requests = %+v", requests)
	}
	want := `{"anchors":[{"period":"2026-11","quantiles":{"10":15,"50":35,"90":60}}],"preview":true,"thresholds":{"to":70},"to":"2027-06"}`
	if got := bodyJSON(t, requests[0].body); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// --run sends X-Antistatic-Run on every request the command makes.
func TestRunHeader(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		reply string
	}{
		{"show", []string{"show", "p712-1", "--run", "712"}, `{"data":{"code":"p712-1","title":"T"}}`},
		{"quote", []string{"quote", "p712-1", "--submarket", "sm_1", "--probability", "0.5", "--run", "712"}, `{"data":{"quotes":[]}}`},
		{"trade", []string{"trade", "p712-1", "--updates", `[{"submarket":"sm_1","probability":0.6}]`, "-y", "--run", "712"}, `{"data":{"total_cost":1}}`},
		{"points", []string{"points", "p712-1", "--run", "712"}, `{"data":{}}`},
		{"odds", []string{"odds", "p712-1", "--run", "712"}, `{"data":{}}`},
		{"comments", []string{"comments", "p712-1", "--run", "712"}, `{"data":{"comments":[]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests []recordedRequest
			var err error
			captureStdout(t, func() {
				requests, err = runAgainstServer(t, rootCmd, tc.args,
					func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
						w.Write([]byte(tc.reply))
					})
			})
			if len(requests) == 0 {
				t.Fatalf("no requests (err %v)", err)
			}
			for _, r := range requests {
				if r.run != "712" {
					t.Errorf("%s %s: X-Antistatic-Run = %q", r.method, r.path, r.run)
				}
			}
		})
	}

	// Without --run, no header.
	var requests []recordedRequest
	captureStdout(t, func() {
		requests, _ = runAgainstServer(t, newMarketExtendCmd(), []string{"~1", "--to-period", "2027-01"},
			func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
				w.Write([]byte(`{"data":{}}`))
			})
	})
	if len(requests) != 1 || requests[0].run != "" {
		t.Errorf("requests = %+v", requests)
	}
}
