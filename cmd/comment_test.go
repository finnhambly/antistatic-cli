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
)

func TestCommentRequests(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		parent    float64
		body      string
		status    int
		wantError string
	}{
		{name: "top level", args: []string{"example", "Useful", "context"}, body: "Useful context"},
		{name: "threaded multiline", args: []string{"example", "--reply-to", "116", "--body", "Evidence\n\n[Source](https://example.org)"}, parent: 116, body: "Evidence\n\n[Source](https://example.org)"},
		{name: "zero parent", args: []string{"example", "--reply-to", "0", "--body", "Context"}, wantError: "positive comment ID"},
		{name: "negative parent", args: []string{"example", "--reply-to=-1", "--body", "Context"}, wantError: "positive comment ID"},
		{name: "non-numeric parent", args: []string{"example", "--reply-to", "abc", "--body", "Context"}, wantError: "invalid argument"},
		{name: "empty reply", args: []string{"example", "--reply-to", "116", "--body", " "}, wantError: "cannot be empty"},
		{name: "server rejects foreign parent", args: []string{"example", "--reply-to", "42", "--body", "Context"}, parent: 42, body: "Context", status: 422, wantError: "invalid_parent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan map[string]interface{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/markets/example/comments" {
					t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing authentication")
				}
				var body map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				captured <- body
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					io.WriteString(w, `{"error":{"code":"invalid_parent","message":"Parent must belong to this market"}}`)
					return
				}
				w.WriteHeader(http.StatusCreated)
				io.WriteString(w, `{"data":{"id":120,"parent_id":116}}`)
			}))
			defer server.Close()
			t.Setenv("ANTISTATIC_URL", server.URL)
			t.Setenv("ANTISTATIC_TOKEN", "test-token")
			previous := client
			client = api.NewClient(&config.Config{})
			defer func() { client = previous }()
			command := newCommentCmd()
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(tc.args)
			err := command.Execute()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v; want %q", err, tc.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && tc.status == 0 {
				select {
				case <-captured:
					t.Fatal("invalid input made a write request")
				default:
				}
				return
			}
			select {
			case body := <-captured:
				if body["body"] != tc.body || body["format"] != "markdown" {
					t.Errorf("wrong body: %#v", body)
				}
				parent, exists := body["parent_id"]
				if tc.parent == 0 && exists {
					t.Errorf("top-level request unexpectedly includes parent_id: %#v", parent)
				}
				if tc.parent != 0 && parent != tc.parent {
					t.Errorf("parent = %#v; want %v", parent, tc.parent)
				}
			default:
				t.Fatal("no request received")
			}
		})
	}
}
