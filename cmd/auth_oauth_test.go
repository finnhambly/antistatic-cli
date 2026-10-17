package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/finnhambly/antistatic-cli/internal/config"
)

func TestOAuthClientIDDerivesFromBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://antistatic.exchange":   "https://antistatic.exchange/oauth/clients/cli.json",
		"https://antistatic.exchange/":  "https://antistatic.exchange/oauth/clients/cli.json",
		"http://localhost:4000":         "http://localhost:4000/oauth/clients/cli.json",
		"https://staging.example.com//": "https://staging.example.com/oauth/clients/cli.json",
	}

	for base, want := range cases {
		if got := oauthClientID(base); got != want {
			t.Errorf("oauthClientID(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestOAuthClientIDFollowsAntistaticURL(t *testing.T) {
	c := &config.Config{}

	t.Setenv("ANTISTATIC_URL", "")
	if got, want := oauthClientID(c.ResolveBaseURL()), config.DefaultBaseURL+"/oauth/clients/cli.json"; got != want {
		t.Errorf("default client_id = %q, want %q", got, want)
	}

	t.Setenv("ANTISTATIC_URL", "http://localhost:4000/")
	if got, want := oauthClientID(c.ResolveBaseURL()), "http://localhost:4000/oauth/clients/cli.json"; got != want {
		t.Errorf("ANTISTATIC_URL client_id = %q, want %q", got, want)
	}
}

func TestBuildAuthorizeURLUsesMetadataClientID(t *testing.T) {
	base := "https://antistatic.exchange"
	raw := buildAuthorizeURL(base, oauthClientID(base), "http://127.0.0.1:43123/callback", "st", "ch")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing authorize URL: %v", err)
	}
	if u.Path != "/oauth/authorize" {
		t.Errorf("path = %q", u.Path)
	}

	q := u.Query()
	checks := map[string]string{
		"client_id":             "https://antistatic.exchange/oauth/clients/cli.json",
		"redirect_uri":          "http://127.0.0.1:43123/callback",
		"response_type":         "code",
		"code_challenge":        "ch",
		"code_challenge_method": "S256",
		"state":                 "st",
		"scope":                 "read write comment offline_access",
	}
	for key, want := range checks {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestCheckOAuthIssuer(t *testing.T) {
	base := "https://antistatic.exchange"

	if err := checkOAuthIssuer(base, ""); err == nil {
		t.Error("missing iss accepted")
	}
	if err := checkOAuthIssuer(base, "https://antistatic.exchange/"); err != nil {
		t.Errorf("matching iss rejected: %v", err)
	}
	if err := checkOAuthIssuer(base, "https://evil.example"); err == nil {
		t.Error("mismatched iss accepted")
	}
}

func TestPasteCodeLogin(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
	}))
	defer srv.Close()

	var printed bytes.Buffer
	var challenge, redirect string
	orig := readLine
	defer func() { readLine = orig }()
	readLine = func(prompt string) (string, error) {
		// The URL is printed before the prompt.
		lines := strings.Split(strings.TrimSpace(printed.String()), "\n")
		u, err := url.Parse(lines[len(lines)-1])
		if err != nil {
			t.Fatalf("parsing printed URL: %v", err)
		}
		challenge = u.Query().Get("code_challenge")
		redirect = u.Query().Get("redirect_uri")
		return "  axcode_pasted \n", nil
	}

	clientID := oauthClientID(srv.URL)
	tokens, err := pasteCodeLogin(&printed, srv.URL, clientID, "verifier-abc")
	if err != nil {
		t.Fatalf("pasteCodeLogin: %v", err)
	}
	if tokens.AccessToken != "at" || tokens.RefreshToken != "rt" {
		t.Errorf("tokens = %+v", tokens)
	}
	if redirect != srv.URL+"/oauth/code" {
		t.Errorf("redirect_uri = %q", redirect)
	}
	if challenge != pkceChallenge("verifier-abc") {
		t.Errorf("code_challenge = %q", challenge)
	}
	checks := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"code":          "axcode_pasted",
		"redirect_uri":  srv.URL + "/oauth/code",
		"code_verifier": "verifier-abc",
	}
	for key, want := range checks {
		if got := gotForm.Get(key); got != want {
			t.Errorf("token %s = %q, want %q", key, got, want)
		}
	}
}

func TestPasteCodeLoginRefusesAnEmptyCode(t *testing.T) {
	orig := readLine
	defer func() { readLine = orig }()
	readLine = func(string) (string, error) { return " \n", nil }

	base := "http://127.0.0.1:1"
	if _, err := pasteCodeLogin(io.Discard, base, oauthClientID(base), "v"); err == nil {
		t.Error("empty code accepted")
	}
}
