package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
	"github.com/spf13/cobra"
)

type recordedRequest struct {
	method string
	path   string
	query  string
	asBot  string
	body   map[string]interface{}
}

// runAgainstServer executes command against an httptest server and returns
// the requests it made.
func runAgainstServer(t *testing.T, command *cobra.Command, args []string, handler func(w http.ResponseWriter, r *http.Request, body map[string]interface{})) ([]recordedRequest, error) {
	t.Helper()
	var requests []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authentication")
		}
		var body map[string]interface{}
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
				}
			}
		}
		requests = append(requests, recordedRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, asBot: r.Header.Get("X-Antistatic-As"), body: body})
		w.Header().Set("Content-Type", "application/json")
		handler(w, r, body)
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_URL", server.URL)
	t.Setenv("ANTISTATIC_TOKEN", "test-token")
	previous := client
	client = api.NewClient(&config.Config{})
	client.AsBot = asBot || envAsBot()
	defer func() { client = previous }()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs(args)
	err := command.Execute()
	return requests, err
}

const settingsJSON = `{"settings":{"email":{"mentions":true,"replies":true,"new_comments":false,"market_resolution":true,"forecast_resolution":true,"double_down_opportunities":true},"auto_double_down":false,"subscribe_to_own_market_comments":true,"follow_own_markets":true,"sensitive_settings_url":"https://antistatic.exchange/users/settings"}}`

func TestSettingsSet(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		status    int
		wantPatch string
		wantError string
	}{
		{name: "nested and top level", args: []string{"set", "email.replies=false", "auto_double_down=true"}, wantPatch: `{"auto_double_down":true,"email":{"replies":false}}`},
		{name: "strict boolean", args: []string{"set", "email.replies=no"}, wantError: "must be true or false"},
		{name: "unknown key", args: []string{"set", "email.bogus=true"}, wantError: `unknown setting "email.bogus"`},
		{name: "read-only url", args: []string{"set", "sensitive_settings_url=x"}, wantError: "unknown setting"},
		{name: "missing equals", args: []string{"set", "follow_own_markets"}, wantError: "expected key=value"},
		{name: "server 422", args: []string{"set", "follow_own_markets=false"}, status: 422, wantPatch: `{"follow_own_markets":false}`, wantError: "not allowed here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests, err := runAgainstServer(t, newSettingsCmd(), tc.args, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
				if r.URL.Path != "/api/v1/me/settings" {
					t.Errorf("unexpected route %s", r.URL.Path)
				}
				if r.Method == http.MethodPatch && tc.status != 0 {
					w.WriteHeader(tc.status)
					io.WriteString(w, `{"error":{"code":"unknown_setting","message":"not allowed here"}}`)
					return
				}
				io.WriteString(w, settingsJSON)
			})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v; want %q", err, tc.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var patch *recordedRequest
			for i := range requests {
				if requests[i].method == http.MethodPatch {
					patch = &requests[i]
				}
			}
			if tc.wantPatch == "" {
				if patch != nil {
					t.Fatalf("invalid input sent a PATCH: %#v", patch.body)
				}
				return
			}
			if patch == nil {
				t.Fatal("no PATCH sent")
			}
			got, _ := json.Marshal(patch.body)
			if string(got) != tc.wantPatch {
				t.Errorf("patch = %s; want %s", got, tc.wantPatch)
			}
		})
	}
}

func TestSettingsShowAcceptsDataEnvelope(t *testing.T) {
	_, err := runAgainstServer(t, newSettingsCmd(), nil, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
		io.WriteString(w, `{"data":`+settingsJSON+`}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	settings, _, err := settingsFromResponse(&api.Response{Body: []byte(`{"data":` + settingsJSON + `}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	leaves := flattenSettings(settings)
	if leaves["email.double_down_opportunities"] != true || leaves["auto_double_down"] != false {
		t.Errorf("unexpected leaves %#v", leaves)
	}
	if _, ok := leaves["sensitive_settings_url"]; ok {
		t.Error("read-only url listed as a setting")
	}
}

func TestFollowAndWatch(t *testing.T) {
	cases := []struct {
		command *cobra.Command
		method  string
		path    string
	}{
		{newMarketToggleCmd("follow", "", "/follow", "following", true), http.MethodPut, "/api/v1/markets/ex/follow"},
		{newMarketToggleCmd("unfollow", "", "/follow", "following", false), http.MethodDelete, "/api/v1/markets/ex/follow"},
		{newMarketToggleCmd("watch", "", "/comment-subscription", "subscribed", true), http.MethodPut, "/api/v1/markets/ex/comment-subscription"},
		{newMarketToggleCmd("unwatch", "", "/comment-subscription", "subscribed", false), http.MethodDelete, "/api/v1/markets/ex/comment-subscription"},
	}
	for _, tc := range cases {
		t.Run(tc.command.Name(), func(t *testing.T) {
			requests, err := runAgainstServer(t, tc.command, []string{"ex"}, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
				io.WriteString(w, `{"following":true}`)
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(requests) != 1 || requests[0].method != tc.method || requests[0].path != tc.path {
				t.Fatalf("requests = %#v", requests)
			}
		})
	}
}

func forecastHandler(code string, then func(w http.ResponseWriter, r *http.Request, body map[string]interface{})) func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
	return func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/markets/"+code+"/forecast" {
			io.WriteString(w, `{"data":{"submarkets":[{"id":11,"label":"2026-05","group":"a"},{"id":12,"label":"2026-06","group":"a"},{"id":21,"label":"5000","group":"a"},{"id":22,"label":"5000","group":"b"}]}}`)
			return
		}
		then(w, r, body)
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		want      string
		wantError string
		noWrite   bool
	}{
		{name: "yes and no", args: []string{"--yes", "2026-05", "--no", "2026-06", "-f"}, want: `{"status":"resolved","submarket_ids":[11,12],"submarket_outcomes":[{"resolved_yes":true,"submarket_id":11},{"resolved_yes":false,"submarket_id":12}]}`},
		{name: "outcome pairs, id and known-at", args: []string{"--outcome", "2026-05=yes", "--outcome", "sm_99=no", "--known-at", "2026-05-14", "-f"}, want: `{"known_at":"2026-05-14T00:00:00Z","status":"resolved","submarket_ids":[11,99],"submarket_outcomes":[{"resolved_yes":true,"submarket_id":11},{"resolved_yes":false,"submarket_id":99}]}`},
		{name: "group disambiguates", args: []string{"--yes", "5000", "--group", "b", "-f"}, want: `{"status":"resolved","submarket_ids":[22],"submarket_outcomes":[{"resolved_yes":true,"submarket_id":22}]}`},
		{name: "ambiguous label", args: []string{"--yes", "5000"}, wantError: "pass --group", noWrite: true},
		{name: "unknown label", args: []string{"--yes", "2027-01"}, wantError: `no submarket matched label "2027-01"`, noWrite: true},
		{name: "no outcomes", args: nil, wantError: "at least one outcome", noWrite: true},
		{name: "bad outcome", args: []string{"--outcome", "2026-05=maybe"}, wantError: "LABEL=yes or LABEL=no", noWrite: true},
		{name: "duplicate", args: []string{"--yes", "2026-05", "--no", "2026-05"}, wantError: "more than once", noWrite: true},
		{name: "bad known-at", args: []string{"--yes", "2026-05", "--known-at", "May"}, wantError: "--known-at", noWrite: true},
		{name: "dry run", args: []string{"--yes", "2026-05", "--dry-run"}, noWrite: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := "res" + string(rune('a'+i))
			requests, err := runAgainstServer(t, newResolveCmd(), append([]string{code}, tc.args...), forecastHandler(code, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
				io.WriteString(w, `{"data":{"code":"x"},"resolution":{"voided_trades":2}}`)
			}))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v; want %q", err, tc.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var put *recordedRequest
			for i := range requests {
				if requests[i].method == http.MethodPut {
					put = &requests[i]
				}
			}
			if tc.noWrite {
				if put != nil {
					t.Fatalf("unexpected write %#v", put.body)
				}
				return
			}
			if put == nil || put.path != "/api/v1/markets/"+code+"/status" {
				t.Fatalf("no status PUT: %#v", requests)
			}
			got, _ := json.Marshal(put.body)
			if string(got) != tc.want {
				t.Errorf("payload = %s; want %s", got, tc.want)
			}
		})
	}
}

func TestReopen(t *testing.T) {
	requests, err := runAgainstServer(t, newReopenCmd(), []string{"reopena", "--threshold", "2026-06"}, forecastHandler("reopena", func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
		io.WriteString(w, `{"data":{}}`)
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(requests[len(requests)-1].body)
	if string(got) != `{"status":"resolved","submarket_ids":[12],"submarket_outcomes":[{"reopen":true,"submarket_id":12}]}` {
		t.Errorf("payload = %s", got)
	}

	_, err = runAgainstServer(t, newReopenCmd(), []string{"reopenb", "--threshold", "2026-06"}, forecastHandler("reopenb", func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
		w.WriteHeader(422)
		io.WriteString(w, `{"error":{"code":"undo_window_passed","message":"Undo window passed"}}`)
	}))
	if err == nil || !strings.Contains(err.Error(), "undo window has passed") {
		t.Fatalf("error = %v", err)
	}

	_, err = runAgainstServer(t, newReopenCmd(), []string{"reopenc"}, forecastHandler("reopenc", nil))
	if err == nil || !strings.Contains(err.Error(), "--threshold") {
		t.Fatalf("error = %v", err)
	}
}

func TestCloseAndOpen(t *testing.T) {
	for _, tc := range []struct {
		command *cobra.Command
		status  string
	}{
		{newStatusCmd("close", "", "closed", "Closed"), "closed"},
		{newStatusCmd("open", "", "open", "Opened"), "open"},
	} {
		requests, err := runAgainstServer(t, tc.command, []string{"ex"}, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			io.WriteString(w, `{"data":{}}`)
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(requests) != 1 || requests[0].method != http.MethodPut || requests[0].path != "/api/v1/markets/ex/status" || requests[0].body["status"] != tc.status {
			t.Fatalf("requests = %#v", requests)
		}
	}
}
