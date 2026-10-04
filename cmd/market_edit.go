package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// market edit exists so owners can fix a market's wording without the web
// form: PATCH /markets/:code takes the same owner-editable fields the form
// does. Resolution criteria and background change only on private markets,
// and the server keeps each change as a new version.

var hexColour = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

func newMarketEditCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "edit <code>",
		Short: "Edit your market's title, unit, items or (private) description",
		Long: `Edit a market you own. Send only what changes.

  antistatic market edit CODE --title "Seats won" --unit MPs
  antistatic market edit CODE --item lab=Labour:#e4003b --item con=:#0087dc
  antistatic market edit CODE --resolution-file criteria.html --background-file bg.html

--item is KEY=LABEL[:COLOUR] for an event or party. Resolution criteria and
background can be edited only on private markets; each edit is kept as a new
version.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := marketEditPayload(cmd)
			if err != nil {
				return err
			}
			if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
				return printPayload(payload)
			}
			if err := requireAuth(); err != nil {
				return err
			}
			resp, err := client.Patch("/markets/"+args[0], payload)
			if err != nil {
				return err
			}
			data, err := resp.Data()
			if err != nil {
				return err
			}
			if jsonOutput || !output.IsTTY() {
				output.JSON(data)
				return nil
			}
			var market struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(data, &market)
			fmt.Printf("Updated %s.\n", market.Code)
			return nil
		},
	}
	command.Flags().String("title", "", "New title")
	command.Flags().String("unit", "", "New unit label")
	command.Flags().StringArray("item", nil, "KEY=LABEL[:COLOUR] (repeatable)")
	command.Flags().String("resolution-file", "", "File with the new resolution criteria (private markets)")
	command.Flags().String("background-file", "", "File with the new background HTML (private markets)")
	command.Flags().Bool("dry-run", false, "Print the request without sending it")
	return command
}

func marketEditPayload(cmd *cobra.Command) (map[string]interface{}, error) {
	payload := map[string]interface{}{}
	for _, flag := range []struct{ name, key string }{{"title", "title"}, {"unit", "unit"}} {
		if cmd.Flags().Changed(flag.name) {
			value, _ := cmd.Flags().GetString(flag.name)
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("--%s can't be empty", flag.name)
			}
			payload[flag.key] = value
		}
	}
	for _, flag := range []struct{ name, key string }{{"resolution-file", "resolution_criteria"}, {"background-file", "background"}} {
		path, _ := cmd.Flags().GetString(flag.name)
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", flag.name, err)
		}
		if strings.TrimSpace(string(raw)) == "" {
			return nil, fmt.Errorf("--%s: %s is empty", flag.name, path)
		}
		payload[flag.key] = string(raw)
	}
	specs, _ := cmd.Flags().GetStringArray("item")
	if len(specs) > 0 {
		items := make([]map[string]interface{}, 0, len(specs))
		for _, spec := range specs {
			item, err := parseItemFlag(spec)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		payload["items"] = items
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("nothing to change: pass --title, --unit, --item, --resolution-file or --background-file")
	}
	return payload, nil
}

// parseItemFlag reads KEY=LABEL[:COLOUR]. The colour is taken only when the
// last ":" part is a hex colour, so labels may contain colons.
func parseItemFlag(spec string) (map[string]interface{}, error) {
	key, rest, ok := strings.Cut(spec, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return nil, fmt.Errorf("--item %q: expected KEY=LABEL[:COLOUR]", spec)
	}
	label, colour := rest, ""
	if i := strings.LastIndex(rest, ":"); i >= 0 && hexColour.MatchString(strings.TrimSpace(rest[i+1:])) {
		label, colour = rest[:i], strings.TrimSpace(rest[i+1:])
		if !strings.HasPrefix(colour, "#") {
			colour = "#" + colour
		}
	}
	item := map[string]interface{}{"key": key}
	if label = strings.TrimSpace(label); label != "" {
		item["label"] = label
	}
	if colour != "" {
		item["color"] = strings.ToLower(colour)
	}
	if len(item) == 1 {
		return nil, fmt.Errorf("--item %q: give a label, a colour or both", spec)
	}
	return item, nil
}
