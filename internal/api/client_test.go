package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finnhambly/antistatic-cli/internal/config"
)

func TestPathEscapesArguments(t *testing.T) {
	if got := Path("/markets/{code}/comments/{comment_id}", "a b/c", 7); got != "/markets/a%20b%2Fc/comments/7" {
		t.Fatalf("Path = %q", got)
	}
}

func TestUserAgentAndDeprecationWarning(t *testing.T) {
	var agents []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		w.Header().Set("Deprecation", "@1793491200")
		w.Header().Set("Sunset", "Mon, 01 Feb 2027 00:00:00 GMT")
		w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_TOKEN", "")
	t.Setenv("ANTISTATIC_URL", server.URL)

	c := NewClient(&config.Config{})
	c.Version = "1.2.3"
	var warn bytes.Buffer
	c.Warn = &warn

	for i := 0; i < 2; i++ {
		if _, err := c.Get("/markets", nil); err != nil {
			t.Fatal(err)
		}
	}

	if agents[0] != "antistatic-cli/1.2.3" {
		t.Errorf("User-Agent = %q", agents[0])
	}
	out := warn.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "GET /api/v1/markets is deprecated") ||
		!strings.Contains(out, "Mon, 01 Feb 2027") {
		t.Errorf("warning = %q", out)
	}
}
