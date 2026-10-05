package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
)

func TestFeedbackSendsMessageAndKind(t *testing.T) {
	captured := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/feedback" {
			t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		captured <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"ok":true,"kind":"suggestion"}`)
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_URL", server.URL)
	t.Setenv("ANTISTATIC_TOKEN", "test-token")
	previous := client
	client = api.NewClient(&config.Config{})
	defer func() { client = previous }()

	command := newFeedbackCmd()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"--suggestion", "Quotes", "need", "targets"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	body := <-captured
	if body["message"] != "Quotes need targets" || body["kind"] != "suggestion" || body["client"] != "CLI" {
		t.Fatalf("unexpected body: %#v", body)
	}
}
