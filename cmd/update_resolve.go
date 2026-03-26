package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type updateLookupPoint struct {
	ID              int    `json:"id"`
	Label           string `json:"label"`
	Group           string `json:"group"`
	ProjectionGroup string `json:"projection_group"`
}

func resolveUpdateLabelsInBody(code string, body map[string]interface{}) error {
	raw, ok := body["updates"]
	if !ok {
		return nil
	}

	updates, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("updates must be an array")
	}

	needsLookup := false
	for _, item := range updates {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasID := entry["submarket_id"]; hasID {
			continue
		}
		if updateLabel(entry) != "" {
			needsLookup = true
			break
		}
	}

	if !needsLookup {
		return nil
	}

	points, err := fetchUpdateLookupPoints(code)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return fmt.Errorf("could not resolve labels: forecast returned no submarkets")
	}

	byLabel := make(map[string][]updateLookupPoint)
	for _, point := range points {
		key := normalizeLookupKey(point.Label)
		if key == "" {
			continue
		}
		byLabel[key] = append(byLabel[key], point)
	}

	for _, item := range updates {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasID := entry["submarket_id"]; hasID {
			continue
		}

		label := updateLabel(entry)
		if label == "" {
			continue
		}

		candidates := byLabel[normalizeLookupKey(label)]
		if len(candidates) == 0 {
			return fmt.Errorf("no submarket matched label %q", label)
		}

		groupHint := updateGroupHint(entry)
		if groupHint != "" {
			filtered := make([]updateLookupPoint, 0, len(candidates))
			for _, candidate := range candidates {
				group := candidate.ProjectionGroup
				if group == "" {
					group = candidate.Group
				}
				if strings.EqualFold(strings.TrimSpace(group), groupHint) {
					filtered = append(filtered, candidate)
				}
			}
			candidates = filtered
		}

		switch len(candidates) {
		case 0:
			return fmt.Errorf("no submarket matched label %q in group %q", label, groupHint)
		case 1:
			entry["submarket_id"] = candidates[0].ID
		default:
			available := uniqueCandidateGroups(candidates)
			return fmt.Errorf(
				"label %q is ambiguous across groups (%s); add group/projection_group in that update",
				label,
				strings.Join(available, ", "),
			)
		}
	}

	body["updates"] = updates
	return nil
}

func fetchUpdateLookupPoints(code string) ([]updateLookupPoint, error) {
	params := url.Values{}
	params.Set("include", "full")
	params.Set("limit", "0")
	params.Set("mode", "full")

	resp, err := client.Get("/markets/"+code+"/forecast", params)
	if err != nil {
		return nil, err
	}

	data, err := resp.Data()
	if err != nil {
		return nil, err
	}

	var payload struct {
		ResponseMode string                         `json:"response_mode"`
		Submarkets   []updateLookupPoint            `json:"submarkets"`
		Forecast     map[string][]updateLookupPoint `json:"forecast"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing forecast payload for label lookup: %w", err)
	}
	if payload.ResponseMode == "summary_index" {
		return nil, fmt.Errorf("forecast summary index cannot resolve labels; retry with full forecast data")
	}

	if len(payload.Submarkets) > 0 {
		return payload.Submarkets, nil
	}

	points := make([]updateLookupPoint, 0)
	for group, groupPoints := range payload.Forecast {
		for _, point := range groupPoints {
			if point.Group == "" {
				point.Group = group
			}
			if point.ProjectionGroup == "" {
				point.ProjectionGroup = point.Group
			}
			points = append(points, point)
		}
	}
	return points, nil
}

func updateLabel(entry map[string]interface{}) string {
	if label, ok := entry["label"].(string); ok {
		return strings.TrimSpace(label)
	}
	if label, ok := entry["submarket_label"].(string); ok {
		return strings.TrimSpace(label)
	}
	return ""
}

func updateGroupHint(entry map[string]interface{}) string {
	if group, ok := entry["group"].(string); ok {
		return strings.TrimSpace(group)
	}
	if group, ok := entry["projection_group"].(string); ok {
		return strings.TrimSpace(group)
	}
	return ""
}

func normalizeLookupKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func uniqueCandidateGroups(points []updateLookupPoint) []string {
	groups := make([]string, 0, len(points))
	seen := make(map[string]bool)
	for _, point := range points {
		group := point.ProjectionGroup
		if group == "" {
			group = point.Group
		}
		if group == "" {
			group = "(none)"
		}
		if seen[group] {
			continue
		}
		seen[group] = true
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}
