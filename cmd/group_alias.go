package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var fiscalYearPattern = regexp.MustCompile(`(?i)\b([12]\d{3})\s*/\s*([0-9]{2,4})\b`)

func normalizeGroupKey(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.ReplaceAll(key, " ", "")
	return key
}

func leadingYear(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 4 {
		return 0, false
	}
	year, err := strconv.Atoi(trimmed[:4])
	if err != nil || year < 1000 || year > 9999 {
		return 0, false
	}
	return year, true
}

func groupLabelFromSubmarketLabel(label string) string {
	match := fiscalYearPattern.FindStringSubmatch(label)
	if len(match) != 3 {
		return ""
	}

	start, err := strconv.Atoi(match[1])
	if err != nil {
		return ""
	}

	endRaw := strings.TrimSpace(match[2])
	end, err := strconv.Atoi(endRaw)
	if err != nil {
		return ""
	}
	if len(endRaw) <= 2 {
		century := (start / 100) * 100
		end += century
		if end < start {
			end += 100
		}
	}

	return fmt.Sprintf("%04d/%02d", start, end%100)
}

func groupAliasTokenSet(group string, groupLabel string) map[string]struct{} {
	set := make(map[string]struct{})

	add := func(value string) {
		key := normalizeGroupKey(value)
		if key == "" {
			return
		}
		set[key] = struct{}{}
	}

	group = strings.TrimSpace(group)
	add(group)

	if year, ok := leadingYear(group); ok {
		add(fmt.Sprintf("%04d", year))
	}

	if len(group) >= 10 && strings.Count(group[:10], "-") == 2 {
		add(group[:10])
	}

	groupLabel = strings.TrimSpace(groupLabel)
	add(groupLabel)
	if year, ok := leadingYear(groupLabel); ok {
		add(fmt.Sprintf("%04d", year))
	}

	return set
}

func buildGroupAliasIndex(groups []string, groupLabels map[string]string) map[string]string {
	index := make(map[string]string)
	ambiguous := make(map[string]bool)

	addAlias := func(alias, canonical string) {
		key := normalizeGroupKey(alias)
		if key == "" || canonical == "" {
			return
		}
		if ambiguous[key] {
			return
		}
		if existing, exists := index[key]; exists && existing != canonical {
			delete(index, key)
			ambiguous[key] = true
			return
		}
		index[key] = canonical
	}

	for _, group := range groups {
		groupLabel := ""
		if groupLabels != nil {
			groupLabel = groupLabels[group]
		}
		for alias := range groupAliasTokenSet(group, groupLabel) {
			addAlias(alias, group)
		}
	}

	return index
}

func resolveGroupAlias(aliasIndex map[string]string, input string) (string, bool) {
	if aliasIndex == nil {
		return "", false
	}
	group, ok := aliasIndex[normalizeGroupKey(input)]
	return group, ok
}

func sortedGroupKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
