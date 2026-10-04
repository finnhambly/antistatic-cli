package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Account settings over /me/settings. Keys are dotted paths into the nested
// settings object (email.replies). Valid keys and their types come from the
// server's own GET response, so keys the exchange adds later work without a
// CLI release; the server's 422 still has the last word.

const sensitiveSettingsKey = "sensitive_settings_url"

func newSettingsCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "settings",
		Short: "Show your account settings",
		Long: `Show your account settings.

  antistatic settings
  antistatic settings set email.replies=false auto_double_down=true`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			settings, raw, err := fetchSettings()
			if err != nil {
				return err
			}
			printSettings(settings, raw)
			return nil
		},
	}

	command.AddCommand(&cobra.Command{
		Use:   "set key=value...",
		Short: "Change account settings",
		Long: `Change account settings. Keys are as shown by "antistatic settings".

  antistatic settings set email.replies=false
  antistatic settings set auto_double_down=true follow_own_markets=false`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			current, _, err := fetchSettings()
			if err != nil {
				return err
			}
			patch, err := buildSettingsPatch(current, args)
			if err != nil {
				return err
			}
			settings, raw, err := settingsFromResponse(client.Patch("/me/settings", patch))
			if err != nil {
				return err
			}
			printSettings(settings, raw)
			return nil
		},
	})
	return command
}

func fetchSettings() (map[string]interface{}, json.RawMessage, error) {
	return settingsFromResponse(client.Get("/me/settings", nil))
}

// settingsFromResponse accepts {"settings": {...}} with or without a "data"
// envelope.
func settingsFromResponse(resp *api.Response, err error) (map[string]interface{}, json.RawMessage, error) {
	if err != nil {
		return nil, nil, err
	}
	raw := unwrapData(resp)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, fmt.Errorf("parsing settings: %w", err)
	}
	if inner, ok := payload["settings"]; ok {
		raw = inner
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, nil, fmt.Errorf("parsing settings: %w", err)
	}
	return settings, raw, nil
}

// unwrapData returns the "data" field when present, else the whole body.
func unwrapData(resp *api.Response) json.RawMessage {
	if data, err := resp.Data(); err == nil && len(data) > 0 && string(data) != "null" {
		return data
	}
	return resp.Body
}

func printSettings(settings map[string]interface{}, raw json.RawMessage) {
	if jsonOutput || !output.IsTTY() {
		output.JSON(raw)
		return
	}
	leaves := flattenSettings(settings)
	keys := sortedSettingKeys(leaves)
	pairs := make([][2]string, 0, len(keys))
	for _, key := range keys {
		value := fmt.Sprint(leaves[key])
		if leaves[key] == nil {
			value = "none"
		}
		pairs = append(pairs, [2]string{key, value})
	}
	output.KeyValue(pairs)
	if url, ok := settings[sensitiveSettingsKey].(string); ok && url != "" {
		fmt.Printf("\nEmail, password and tokens: %s\n", url)
	}
}

// flattenSettings maps dotted keys to leaf values, leaving out read-only
// fields.
func flattenSettings(settings map[string]interface{}) map[string]interface{} {
	leaves := make(map[string]interface{})
	var walk func(prefix string, node map[string]interface{})
	walk = func(prefix string, node map[string]interface{}) {
		for key, value := range node {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if path == sensitiveSettingsKey {
				continue
			}
			if child, ok := value.(map[string]interface{}); ok {
				walk(path, child)
				continue
			}
			leaves[path] = value
		}
	}
	walk("", settings)
	return leaves
}

func sortedSettingKeys(leaves map[string]interface{}) []string {
	keys := make([]string, 0, len(leaves))
	for key := range leaves {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// buildSettingsPatch turns key=value args into a nested partial object,
// typed after the current value of each key.
func buildSettingsPatch(current map[string]interface{}, args []string) (map[string]interface{}, error) {
	leaves := flattenSettings(current)
	patch := make(map[string]interface{})
	for _, arg := range args {
		key, rawValue, ok := strings.Cut(arg, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("expected key=value, got %q", arg)
		}
		existing, known := leaves[key]
		if !known {
			return nil, fmt.Errorf("unknown setting %q; known: %s", key, strings.Join(sortedSettingKeys(leaves), ", "))
		}
		value, err := parseSettingValue(key, rawValue, existing)
		if err != nil {
			return nil, err
		}
		node := patch
		parts := strings.Split(key, ".")
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part].(map[string]interface{})
			if !ok {
				child = make(map[string]interface{})
				node[part] = child
			}
			node = child
		}
		node[parts[len(parts)-1]] = value
	}
	return patch, nil
}

func parseSettingValue(key, raw string, existing interface{}) (interface{}, error) {
	raw = strings.TrimSpace(raw)
	switch existing.(type) {
	case bool:
		switch raw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("%s must be true or false, got %q", key, raw)
	case float64:
		number, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number, got %q", key, raw)
		}
		return number, nil
	default:
		return raw, nil
	}
}

func init() {
	rootCmd.AddCommand(newSettingsCmd())
}
