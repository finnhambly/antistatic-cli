package api

import (
	"bytes"
	"io"
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

func TestPutRawSendsBytesAndErrorsCarryPathAndParts(t *testing.T) {
	var got []byte
	var contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(422)
		w.Write([]byte(`{"error":{"code":"missing_parts","message":"parts missing","path":"items[b1]","parts":["ab","cd"]}}`))
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_TOKEN", "")
	t.Setenv("ANTISTATIC_URL", server.URL)

	body := []byte(`{"b":"<x>","a":1.10}`)
	_, err := NewClient(&config.Config{}).PutRaw(Path("/drafts/{version}/parts/{sha256}", "1.1", "ab"), body)
	if !bytes.Equal(got, body) || contentType != "application/json" {
		t.Errorf("sent %q (%s)", got, contentType)
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "missing_parts" || apiErr.Path != "items[b1]" || len(apiErr.Parts) != 2 || apiErr.StatusCode != 422 {
		t.Fatalf("err = %#v", err)
	}
}

func TestNoRedirectsWithCredentials(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_TOKEN", "secret")
	t.Setenv("ANTISTATIC_URL", server.URL)
	c := NewClient(&config.Config{})
	if _, err := c.CallTool("antistatic_list_sandboxes", nil); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("MCP: %v", err)
	}
	if _, err := c.Get("/markets", nil); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("API: %v", err)
	}
	if leaked {
		t.Fatal("followed a redirect with the token")
	}
}

func TestRefreshReadsTheLoginAgainUnderTheLock(t *testing.T) {
	var refreshes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshes++
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant"}`))
		default:
			if r.Header.Get("Authorization") != "Bearer fresh" {
				t.Errorf("sent %q", r.Header.Get("Authorization"))
			}
			w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer server.Close()
	t.Setenv("ANTISTATIC_TOKEN", "")
	t.Setenv("ANTISTATIC_URL", server.URL)
	t.Setenv("ANTISTATIC_CONFIG_DIR", t.TempDir())

	stale := &config.Config{Token: "old", AuthServer: server.URL, OAuthClientID: "c", OAuthRefreshToken: "r1", OAuthTokenExpiry: "2000-01-01T00:00:00Z"}
	c := NewClient(stale)
	// Another process refreshed meanwhile and saved the rotated login.
	fresh := *stale
	fresh.Token, fresh.OAuthRefreshToken, fresh.OAuthTokenExpiry = "fresh", "r2", "2999-01-01T00:00:00Z"
	if err := fresh.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("/markets", nil); err != nil {
		t.Fatal(err)
	}
	if refreshes != 0 {
		t.Fatalf("redeemed the used refresh token %d times", refreshes)
	}
}
