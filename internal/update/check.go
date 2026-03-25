package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/config"
)

const (
	latestReleaseURL = "https://api.github.com/repos/finnhambly/antistatic-cli/releases/latest"
	checkInterval    = 24 * time.Hour
)

// MaybeNotify prints a lightweight update notice when a newer release exists.
// It uses a cached daily check to avoid repeated network calls.
func MaybeNotify(currentVersion string, cfg *config.Config, interactive bool) {
	if !interactive || cfg == nil {
		return
	}
	if currentVersion == "" || currentVersion == "dev" {
		return
	}
	if disabledByEnv() {
		return
	}

	latest := cfg.UpdateLatest
	checkedRecently := false

	if cfg.UpdateCheckedAt != "" {
		if checkedAt, err := time.Parse(time.RFC3339, cfg.UpdateCheckedAt); err == nil {
			checkedRecently = time.Since(checkedAt) < checkInterval
		}
	}

	if !checkedRecently {
		if fetched, err := fetchLatestVersion(); err == nil && fetched != "" {
			latest = fetched
			cfg.UpdateLatest = fetched
			cfg.UpdateCheckedAt = time.Now().UTC().Format(time.RFC3339)
			_ = cfg.Save()
		}
	}

	if latest == "" {
		return
	}

	if isNewerVersion(latest, currentVersion) {
		fmt.Fprintf(
			os.Stderr,
			"Update available: antistatic %s -> %s. Run: brew upgrade antistatic\n",
			normalizeVersion(currentVersion),
			normalizeVersion(latest),
		)
	}
}

func disabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ANTISTATIC_NO_UPDATE_CHECK"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "antistatic-cli")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("github release check failed: %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.TagName, nil
}

func isNewerVersion(candidate, current string) bool {
	candidateParts := parseVersionParts(candidate)
	currentParts := parseVersionParts(current)
	if len(candidateParts) == 0 || len(currentParts) == 0 {
		return false
	}

	maxLen := len(candidateParts)
	if len(currentParts) > maxLen {
		maxLen = len(currentParts)
	}

	for i := 0; i < maxLen; i++ {
		cv := 0
		if i < len(candidateParts) {
			cv = candidateParts[i]
		}
		pv := 0
		if i < len(currentParts) {
			pv = currentParts[i]
		}
		if cv > pv {
			return true
		}
		if cv < pv {
			return false
		}
	}
	return false
}

func parseVersionParts(v string) []int {
	trimmed := normalizeVersion(v)
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
