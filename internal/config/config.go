package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultBaseURL = "https://antistatic.exchange"
	configFileName = "config.json"
	configDirName  = "antistatic"
)

// Config holds the persisted CLI configuration.
type Config struct {
	Token             string `json:"token,omitempty"`
	ServerURL         string `json:"server_url,omitempty"`
	OAuthClientID     string `json:"oauth_client_id,omitempty"`
	OAuthRefreshToken string `json:"oauth_refresh_token,omitempty"`
	OAuthTokenExpiry  string `json:"oauth_token_expiry,omitempty"`
	// AuthServer is the server the saved login is for; it is sent nowhere else.
	AuthServer string `json:"auth_server,omitempty"`
}

// Lock takes the config directory's lock, across processes, for a
// read-modify-write of the login or the saved keys. Call the returned
// function to release it. It isn't reentrant.
func Lock() (func(), error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = unlockFile(f)
		f.Close()
	}, nil
}

// NormalizeURL is a server URL without trailing slashes.
func NormalizeURL(u string) string { return strings.TrimRight(u, "/") }

// BoundTo reports whether the saved login belongs to the server base. A
// browser login saved before logins were bound is its client id's server
// (the client id is <server>/oauth/clients/cli.json); any other unbound
// login belongs to no server and needs a new login.
func (c *Config) BoundTo(base string) bool {
	server := c.AuthServer
	if server == "" {
		server = strings.TrimSuffix(c.OAuthClientID, "/oauth/clients/cli.json")
		if server == c.OAuthClientID {
			return false
		}
	}
	return NormalizeURL(server) == NormalizeURL(base)
}

// CheckServerURL refuses a server URL that isn't https, except on loopback.
func CheckServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid server URL %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("server URL %q must use https (http only for localhost)", raw)
}

// configDir returns the OS-appropriate config directory.
func configDir() (string, error) {
	if dir := os.Getenv("ANTISTATIC_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	return filepath.Join(dir, configDirName), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// Load reads the config from disk. Returns a zero Config if the file
// doesn't exist yet.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config to disk, creating the directory if needed.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	if err := WritePrivate(path, data); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Dir is the config directory (ANTISTATIC_CONFIG_DIR, else the OS's).
func Dir() (string, error) { return configDir() }

// WritePrivate writes a file only the user can read (mode 600), replacing
// it atomically; a new directory is made mode 700.
func WritePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ResolveToken returns the effective token, preferring the ANTISTATIC_TOKEN
// environment variable over the saved login, which is used only for the
// server it was made for.
func (c *Config) ResolveToken() string {
	if t := os.Getenv("ANTISTATIC_TOKEN"); t != "" {
		return t
	}
	if !c.BoundTo(c.ResolveBaseURL()) {
		return ""
	}
	return c.Token
}

// ResolveBaseURL returns the effective base URL, preferring the
// ANTISTATIC_URL environment variable, then the saved config, then
// the default.
func (c *Config) ResolveBaseURL() string {
	if u := os.Getenv("ANTISTATIC_URL"); u != "" {
		return u
	}
	if c.ServerURL != "" {
		return c.ServerURL
	}
	return DefaultBaseURL
}

// ClearOAuthState removes saved OAuth session fields.
func (c *Config) ClearOAuthState() {
	c.AuthServer = ""
	c.OAuthClientID = ""
	c.OAuthRefreshToken = ""
	c.OAuthTokenExpiry = ""
}
