package cmd

import (
	"net/url"
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

	if err := checkOAuthIssuer(base, ""); err != nil {
		t.Errorf("missing iss should be accepted, got %v", err)
	}
	if err := checkOAuthIssuer(base, "https://antistatic.exchange/"); err != nil {
		t.Errorf("matching iss rejected: %v", err)
	}
	if err := checkOAuthIssuer(base, "https://evil.example"); err == nil {
		t.Error("mismatched iss accepted")
	}
}
