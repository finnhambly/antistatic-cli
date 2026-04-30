package cmd

import (
	"encoding/json"
	"fmt"
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

	updates, err := normalizeUpdatesArray(raw)
	if err != nil {
		return err
	}

	needsLookup := false
	for _, item := range updates {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if hasSubmarketRef(entry) {
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

	groupLabels := make(map[string]string)
	groupSet := make(map[string]struct{})
	for _, point := range points {
		group := point.ProjectionGroup
		if group == "" {
			group = point.Group
		}
		if group == "" {
			continue
		}
		groupSet[group] = struct{}{}
		if groupLabels[group] == "" {
			groupLabels[group] = groupLabelFromSubmarketLabel(point.Label)
		}
	}
	groupAliases := buildGroupAliasIndex(sortedGroupKeys(groupSet), groupLabels)

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
		if hasSubmarketRef(entry) {
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
		resolvedHint := ""
		if groupHint != "" {
			if resolved, ok := resolveGroupAlias(groupAliases, groupHint); ok {
				resolvedHint = resolved
			}
		}
		if groupHint != "" {
			filtered := make([]updateLookupPoint, 0, len(candidates))
			for _, candidate := range candidates {
				if lookupPointMatchesGroupHint(candidate, groupHint, resolvedHint) {
					filtered = append(filtered, candidate)
				}
			}
			candidates = filtered
		}

		switch len(candidates) {
		case 0:
			return fmt.Errorf("no submarket matched label %q in group %q", label, groupHint)
		case 1:
			entry["submarket"] = formatSubmarketRef(candidates[0].ID)
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

func hasSubmarketRef(entry map[string]interface{}) bool {
	if _, ok := entry["submarket"]; ok {
		return true
	}
	if _, ok := entry["submarket_id"]; ok {
		return true
	}
	return false
}

func fetchUpdateLookupPoints(code string) ([]updateLookupPoint, error) {
	data, err := fetchFullForecastData(code)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Submarkets []updateLookupPoint            `json:"submarkets"`
		Forecast   map[string][]updateLookupPoint `json:"forecast"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing forecast payload for label lookup: %w", err)
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
		display := group
		if groupLabel := groupLabelFromSubmarketLabel(point.Label); groupLabel != "" {
			display = fmt.Sprintf("%s (%s)", group, groupLabel)
		}
		groups = append(groups, display)
	}
	sort.Strings(groups)
	return groups
}

func lookupPointMatchesGroupHint(point updateLookupPoint, rawHint, resolvedHint string) bool {
	group := point.ProjectionGroup
	if group == "" {
		group = point.Group
	}
	aliases := groupAliasTokenSet(group, groupLabelFromSubmarketLabel(point.Label))

	if resolvedHint != "" {
		if _, ok := aliases[normalizeGroupKey(resolvedHint)]; ok {
			return true
		}
	}

	_, ok := aliases[normalizeGroupKey(rawHint)]
	return ok
}
