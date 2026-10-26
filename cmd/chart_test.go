package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
)

func TestChartWritesTheCardWithItsDataAndOpensNothingWhenAsked(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		calls = append(calls, req.Method+" "+string(req.Params))
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/call":
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"**A market** · 20%"}],"structuredContent":{"note":"</script><b>"},"_meta":{"antistatic/chart":{"market":{"code":"~9"}}},"isError":false}}`)
		case "resources/read":
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"contents":[{"uri":"ui://antistatic/chart.html","text":"<html><script>card()</script></html>"}]}}`)
		}
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_URL", server.URL)
	t.Setenv("ANTISTATIC_TOKEN", "test-token")
	previous := client
	client = api.NewClient(&config.Config{})
	defer func() { client = previous }()
	opened := false
	previousOpen := openBrowser
	openBrowser = func(string) error { opened = true; return nil }
	defer func() { openBrowser = previousOpen }()

	out := filepath.Join(t.TempDir(), "chart.html")
	command := newChartCmd()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"~9", "--run", "4", "--out", out, "--no-open"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	if opened {
		t.Fatal("--no-open opened the browser")
	}
	if len(calls) != 2 || !strings.Contains(calls[0], `"name":"antistatic_show_market"`) ||
		!strings.Contains(calls[0], `"market_code":"~9"`) || !strings.Contains(calls[0], `"run_id":4`) {
		t.Fatalf("unexpected calls: %v", calls)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	// The card, the result's _meta and the arguments are all in the page.
	for _, want := range []string{`card()`, `antistatic/chart`, `"market_code":"~9"`, `<title>A market · Antistatic</title>`} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The data can't end the page's script: only the page's own tag closes it.
	if strings.Count(html, "</script>") != 1 {
		t.Fatalf("data closed the script: %d </script> tags", strings.Count(html, "</script>"))
	}
}
