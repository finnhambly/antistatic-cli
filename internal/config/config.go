package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	UpdateCheckedAt   string `json:"update_checked_at,omitempty"`
	UpdateLatest      string `json:"update_latest,omitempty"`
}

// configDir returns the OS-appropriate config directory.
func configDir() (string, error) {
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

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ResolveToken returns the effective token, preferring the ANTISTATIC_TOKEN
// environment variable over the saved config.
func (c *Config) ResolveToken() string {
	if t := os.Getenv("ANTISTATIC_TOKEN"); t != "" {
		return t
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
	c.OAuthClientID = ""
	c.OAuthRefreshToken = ""
	c.OAuthTokenExpiry = ""
}
