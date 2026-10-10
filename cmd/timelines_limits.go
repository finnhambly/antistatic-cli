package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/timelines"
)

// limitsCacheDir is where fetched limits are cached (a variable for tests).
var limitsCacheDir = func() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "antistatic"), nil
}

const limitsCacheTTL = time.Hour

func limitsCachePath() (string, error) {
	dir, err := limitsCacheDir()
	if err != nil {
		return "", err
	}
	host := "default"
	if u, err := url.Parse(client.BaseURL()); err == nil && u.Host != "" {
		host = u.Host
	}
	host = strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(host)
	return filepath.Join(dir, "limits-"+host+".json"), nil
}

// loadLimits returns the server's manifest limits: from a cache younger than
// an hour, else from GET /timelines/limits (refreshing the cache), else from
// a stale cache with a warning.
func loadLimits() (*timelines.Limits, error) {
	path, pathErr := limitsCachePath()
	var cached []byte
	if pathErr == nil {
		if info, err := os.Stat(path); err == nil {
			if data, err := os.ReadFile(path); err == nil {
				cached = data
				if time.Since(info.ModTime()) < limitsCacheTTL {
					if limits, err := parseLimits(data); err == nil {
						return limits, nil
					}
				}
			}
		}
	}

	resp, fetchErr := client.Get("/timelines/limits", nil)
	if fetchErr == nil {
		data, err := resp.Data()
		if err == nil {
			limits, err := parseLimits(data)
			if err == nil {
				if pathErr == nil {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
						_ = os.WriteFile(path, data, 0o600)
					}
				}
				return limits, nil
			}
			fetchErr = err
		} else {
			fetchErr = err
		}
	}

	if cached != nil {
		if limits, err := parseLimits(cached); err == nil {
			fmt.Fprintf(progressOut, "warning: couldn't fetch the timelines limits (%v); using the cached copy at %s\n", fetchErr, path)
			return limits, nil
		}
	}
	return nil, fmt.Errorf("fetching the timelines limits: %w (and no cached copy)", fetchErr)
}

func parseLimits(data []byte) (*timelines.Limits, error) {
	var limits timelines.Limits
	if err := json.Unmarshal(data, &limits); err != nil {
		return nil, fmt.Errorf("parsing the timelines limits: %w", err)
	}
	if len(limits.SchemaVersions) == 0 {
		return nil, fmt.Errorf("parsing the timelines limits: no schema_versions")
	}
	return &limits, nil
}
