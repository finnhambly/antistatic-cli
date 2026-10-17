package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolatedConfig(t *testing.T) {
	personal := t.TempDir()
	listening := t.TempDir()
	t.Setenv("ANTISTATIC_CONFIG_DIR", personal)
	if err := (&Config{Token: "personal"}).Save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTISTATIC_CONFIG_DIR", listening)
	c, err := Load()
	if err != nil || c.Token != "" {
		t.Fatalf("fresh profile: %#v %v", c, err)
	}
	if err := (&Config{Token: "listening", OAuthRefreshToken: "refresh"}).Save(); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || c.Token != "listening" {
		t.Fatalf("listening: %#v %v", c, err)
	}
	info, err := os.Stat(filepath.Join(listening, "config.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("config permissions")
	}
	t.Setenv("ANTISTATIC_CONFIG_DIR", personal)
	c, err = Load()
	if err != nil || c.Token != "personal" {
		t.Fatalf("personal changed: %#v %v", c, err)
	}
}

func TestBoundTo(t *testing.T) {
	prod, local := DefaultBaseURL, "http://localhost:4000"
	cases := []struct {
		name string
		c    Config
		base string
		want bool
	}{
		{"bound", Config{Token: "a", AuthServer: local + "/"}, local, true},
		{"bound elsewhere", Config{Token: "a", AuthServer: local}, prod, false},
		{"legacy OAuth", Config{Token: "a", OAuthClientID: local + "/oauth/clients/cli.json"}, local, true},
		{"legacy OAuth elsewhere", Config{Token: "a", OAuthClientID: local + "/oauth/clients/cli.json"}, prod, false},
		{"legacy OAuth, odd client id", Config{Token: "a", OAuthClientID: "x"}, prod, false},
		{"legacy API token", Config{Token: "axk_a"}, prod, false},
		{"legacy API token elsewhere", Config{Token: "axk_a"}, local, false},
		{"legacy API token, saved server", Config{Token: "axk_a", ServerURL: local}, local, false},
	}
	for _, tc := range cases {
		if got := tc.c.BoundTo(tc.base); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}
