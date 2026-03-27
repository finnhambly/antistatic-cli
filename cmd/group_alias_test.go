package cmd

import (
	"reflect"
	"testing"
)

func TestBuildGroupAliasIndex_ResolvesFiscalAliases(t *testing.T) {
	groups := []string{
		"2026-01-31T23:59:59Z",
		"2027-01-31T23:59:59Z",
	}
	groupLabels := map[string]string{
		"2026-01-31T23:59:59Z": "2026/27",
		"2027-01-31T23:59:59Z": "2027/28",
	}

	aliases := buildGroupAliasIndex(groups, groupLabels)

	if got, ok := resolveGroupAlias(aliases, "2026/27"); !ok || got != "2026-01-31T23:59:59Z" {
		t.Fatalf("expected 2026/27 to resolve to first group, got %q ok=%v", got, ok)
	}
	if got, ok := resolveGroupAlias(aliases, "2027"); !ok || got != "2027-01-31T23:59:59Z" {
		t.Fatalf("expected 2027 to resolve to second group, got %q ok=%v", got, ok)
	}
}

func TestSelectDraftPlanGroups_AcceptsAliasInputs(t *testing.T) {
	groups := []string{
		"2026-01-31T23:59:59Z",
		"2027-01-31T23:59:59Z",
		"2028-01-31T23:59:59Z",
	}
	groupLabels := map[string]string{
		"2026-01-31T23:59:59Z": "2026/27",
		"2027-01-31T23:59:59Z": "2027/28",
		"2028-01-31T23:59:59Z": "2028/29",
	}
	aliases := buildGroupAliasIndex(groups, groupLabels)

	selected, err := selectDraftPlanGroups(
		groups,
		"2027",
		"2028/29",
		0,
		aliases,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"2027-01-31T23:59:59Z",
		"2028-01-31T23:59:59Z",
	}
	if !reflect.DeepEqual(selected, want) {
		t.Fatalf("unexpected selected groups: got %v want %v", selected, want)
	}
}
