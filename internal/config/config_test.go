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
