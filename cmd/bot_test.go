package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// captureStdout returns what fn wrote to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = previous
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestBotCommands(t *testing.T) {
	asBot = true // bot commands act as the owner even with --as-bot
	defer func() { asBot = false }()
	botJSON := `{"username":"finn-bot","paused":false,"profile_url":"https://x/u/finn-bot"}`

	cases := []struct {
		name, method, path, want string
		args                     []string
	}{
		{name: "show", args: nil, method: http.MethodGet, path: "/api/v1/me/bot"},
		{name: "create", args: []string{"create", "--username", "robo"}, method: http.MethodPost, path: "/api/v1/me/bot", want: `{"username":"robo"}`},
		{name: "rename", args: []string{"rename", "robo-2"}, method: http.MethodPatch, path: "/api/v1/me/settings", want: `{"bot":{"username":"robo-2"}}`},
		{name: "pause", args: []string{"pause"}, method: http.MethodPatch, path: "/api/v1/me/settings", want: `{"bot":{"paused":true}}`},
		{name: "resume", args: []string{"resume"}, method: http.MethodPatch, path: "/api/v1/me/settings", want: `{"bot":{"paused":false}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests []recordedRequest
			var err error
			out := captureStdout(t, func() {
				requests, err = runAgainstServer(t, newBotCmd(), tc.args, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
					if r.Method == http.MethodPatch {
						io.WriteString(w, `{"settings":{"bot":`+botJSON+`}}`)
						return
					}
					io.WriteString(w, `{"bot":`+botJSON+`}`)
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(requests) != 1 || requests[0].method != tc.method || requests[0].path != tc.path {
				t.Fatalf("requests = %#v", requests)
			}
			if requests[0].asBot != "" {
				t.Errorf("bot command sent the bot header")
			}
			if tc.want != "" {
				got, _ := json.Marshal(requests[0].body)
				if string(got) != tc.want {
					t.Errorf("body = %s; want %s", got, tc.want)
				}
			}
			if !strings.Contains(out, `"finn-bot"`) {
				t.Errorf("output = %s", out)
			}
		})
	}
}

func TestAsBotSendsHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag bool
		env  string
		want string
	}{
		{name: "off", want: ""},
		{name: "flag", flag: true, want: "bot"},
		{name: "env", env: "1", want: "bot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asBot = tc.flag
			defer func() { asBot = false }()
			t.Setenv("ANTISTATIC_AS_BOT", tc.env)
			var requests []recordedRequest
			captureStdout(t, func() {
				requests, _ = runAgainstServer(t, newSettingsCmd(), nil, func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
					io.WriteString(w, settingsJSON)
				})
			})
			if len(requests) != 1 || requests[0].asBot != tc.want {
				t.Fatalf("requests = %#v", requests)
			}
		})
	}
}

func TestResolveDryRunScopesToSubmarkets(t *testing.T) {
	out := captureStdout(t, func() {
		_, err := runAgainstServer(t, newResolveCmd(), []string{"dry", "--yes", "2026-05", "--dry-run"}, forecastHandler("dry", nil))
		if err != nil {
			t.Error(err)
		}
	})
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output = %s", out)
	}
	ids, _ := json.Marshal(payload["submarket_ids"])
	if string(ids) != "[11]" {
		t.Errorf("submarket_ids = %s", ids)
	}

	out = captureStdout(t, func() {
		_, err := runAgainstServer(t, newReopenCmd(), []string{"dry2", "--threshold", "2026-06", "--dry-run"}, forecastHandler("dry2", nil))
		if err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(strings.Join(strings.Fields(out), ""), `"submarket_ids":[12]`) {
		t.Errorf("reopen dry run = %s", out)
	}
}
