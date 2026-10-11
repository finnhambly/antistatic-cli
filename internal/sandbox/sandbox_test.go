package sandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func testLimits() *Limits {
	return &Limits{
		SchemaVersions: []int{2},
		Manifest: ManifestLimits{
			MaxPartBytes: 2000000, MaxTopBytes: 6000000, MaxParts: 400, MaxItems: 1000,
			MaxCheckpoints: 5000, MaxBars: 300000, MaxBarsPerItem: 3000, MaxBarsPerPart: 6000,
		},
		Fields: map[string]FieldLimit{
			"curriculum.slug":        {Min: intp(2), Max: intp(80), Pattern: "^[a-z0-9][a-z0-9_-]*$"},
			"question.code":          {Min: intp(2), Max: intp(40), Pattern: "^[a-z0-9][a-z0-9_-]*$"},
			"checkpoint.key":         {Max: intp(255)},
			"checkpoint.title":       {Max: intp(255)},
			"item.key":               {Max: intp(255)},
			"item.title":             {Max: intp(255)},
			"market.title":           {Max: intp(255)},
			"bar.key":                {Max: intp(255)},
			"bar.label":              {Max: intp(255)},
			"bar.projection_group":   {Max: intp(255)},
			"disclosure.key":         {Max: intp(255)},
			"disclosure.title":       {Max: intp(255)},
			"disclosure.source_url":  {Max: intp(2000), Pattern: "^https?://"},
			"resolution_request.key": {Max: intp(255)},
		},
		HindsightTerms: []string{"seminal", "would later", `/as of 20\d\d/`},
	}
}

// fixture builds a manifest with n items of bars bars each. Item i has one
// attached disclosure and one resolution request; there is one unattached
// disclosure. Outcomes and notes are "sealed" strings no output may contain.
func fixture(n, bars int) map[string]interface{} {
	var items, disclosures, requests []interface{}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("b%03d", i)
		var keys, thresholds, p0, outcomes []interface{}
		for j := 0; j < bars; j++ {
			keys = append(keys, fmt.Sprintf("%s-y%d", key, 2001+j))
			thresholds = append(thresholds, fmt.Sprintf("%d-12-31", 2001+j))
			p0 = append(p0, json.Number("0.10"))
			outcomes = append(outcomes, j%2 == 0)
		}
		items = append(items, map[string]interface{}{
			"key": key, "title": "Item " + key, "introduced_at": "cp000", "sort_order": json.Number(fmt.Sprint(i)),
			"market": map[string]interface{}{"code": "q-" + key, "title": "When will " + key + " happen?", "type": "date",
				"resolution_criteria": "Resolves <b>yes</b> when it happens."},
			"curves": []interface{}{map[string]interface{}{
				"group": nil, "keys": keys, "thresholds": thresholds, "p0": p0,
				"closes_at": "cp001", "outcomes": outcomes, "resolution": "prepared",
			}},
		})
		disclosures = append(disclosures, map[string]interface{}{
			"key": "d-" + key, "item": key, "checkpoint": "cp001", "kind": "evidence",
			"title": "News on " + key, "body_html": "<p>SEALED-NOTE-" + key + "</p>",
		})
		requests = append(requests, map[string]interface{}{
			"key": "r-" + key, "item": key, "title": "Resolve " + key, "body_html": "<p>Did it?</p>",
		})
	}
	disclosures = append([]interface{}{map[string]interface{}{
		"key": "d-general", "checkpoint": "cp000", "kind": "context", "title": "Background", "body_html": "<p>Context</p>",
	}}, disclosures...)
	return map[string]interface{}{
		"schema_version": json.Number("2"),
		"source":         map[string]interface{}{"commit": "abc123", "dirty": false},
		"curriculum":     map[string]interface{}{"title": "Widgets", "access": "restricted"},
		"checkpoints": []interface{}{
			map[string]interface{}{"key": "cp000", "sequence": json.Number("0"), "occurred_at": "2001-01-01T00:00:00Z", "title": "Start"},
			map[string]interface{}{"key": "cp001", "sequence": json.Number("1"), "occurred_at": "2002-01-01T00:00:00Z", "title": "Next"},
		},
		"disclosures":         disclosures,
		"resolution_requests": requests,
		"resolved_through":    "2001-06-30",
		"items":               items,
	}
}

func mustManifest(t *testing.T, root map[string]interface{}) *Manifest {
	t.Helper()
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCanonical(t *testing.T) {
	v, err := Decode([]byte(`{"z": 1.10, "a": [1e5, 12345678901234567890, -0.0], "h": "<b>&amp;</b>", "é": "ü"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[1e5,12345678901234567890,-0.0],"h":"<b>&amp;</b>","z":1.10,"é":"ü"}`
	if string(got) != want {
		t.Errorf("Canonical = %s\nwant        %s", got, want)
	}
}

func TestDecodeErrorsDoNotQuoteInput(t *testing.T) {
	_, err := Parse([]byte(`{"items": [SECRET-ANSWER]}`))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func partsOf(t *testing.T, s *Split) (items, disclosures, requests []string) {
	t.Helper()
	for _, p := range s.Parts {
		if SHA256Hex(p.Body) != p.SHA256 || bytes.HasSuffix(p.Body, []byte("\n")) {
			t.Fatalf("part hash or encoding is off")
		}
		var body struct {
			Items, Disclosures, ResolutionRequests []map[string]interface{}
		}
		if err := json.Unmarshal(p.Body, &struct {
			Items              *[]map[string]interface{} `json:"items"`
			Disclosures        *[]map[string]interface{} `json:"disclosures"`
			ResolutionRequests *[]map[string]interface{} `json:"resolution_requests"`
		}{&body.Items, &body.Disclosures, &body.ResolutionRequests}); err != nil {
			t.Fatal(err)
		}
		if body.Items == nil || body.Disclosures == nil || body.ResolutionRequests == nil {
			t.Fatalf("part lacks a list: %s", p.Body[:60])
		}
		inPart := map[string]bool{}
		for _, it := range body.Items {
			items = append(items, it["key"].(string))
			inPart[it["key"].(string)] = true
		}
		for _, d := range body.Disclosures {
			if !inPart[d["item"].(string)] {
				t.Errorf("disclosure %v is not with its item", d["key"])
			}
			disclosures = append(disclosures, d["key"].(string))
		}
		for _, r := range body.ResolutionRequests {
			if !inPart[r["item"].(string)] {
				t.Errorf("request %v is not with its item", r["key"])
			}
			requests = append(requests, r["key"].(string))
		}
	}
	return
}

func TestSplitIsDeterministicAndOrderPreserving(t *testing.T) {
	root := fixture(25, 7)
	m1 := mustManifest(t, root)
	// The same manifest written with different key order and spacing.
	compact, _ := json.Marshal(root)
	m2, err := Parse(compact)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := SplitManifest(m1, 6000, 30)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := SplitManifest(m2, 6000, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1.Parts) < 3 {
		t.Fatalf("expected several parts, got %d", len(s1.Parts))
	}
	if len(s1.Parts) != len(s2.Parts) || !bytes.Equal(s1.Top, s2.Top) {
		t.Fatal("splits differ")
	}
	for i := range s1.Parts {
		if !bytes.Equal(s1.Parts[i].Body, s2.Parts[i].Body) {
			t.Fatalf("part %d differs", i)
		}
	}

	items, disclosures, requests := partsOf(t, s1)
	for i, key := range items {
		if key != fmt.Sprintf("b%03d", i) {
			t.Fatalf("items out of order at %d: %s", i, key)
		}
		if disclosures[i] != "d-"+key || requests[i] != "r-"+key {
			t.Fatalf("attached entries out of order at %d", i)
		}
	}
	if len(items) != 25 || len(disclosures) != 25 || len(requests) != 25 {
		t.Fatalf("counts: %d items, %d disclosures, %d requests", len(items), len(disclosures), len(requests))
	}

	var top map[string]interface{}
	json.Unmarshal(s1.Top, &top)
	if d := top["disclosures"].([]interface{}); len(d) != 1 || d[0].(map[string]interface{})["key"] != "d-general" {
		t.Errorf("top disclosures = %v", d)
	}
	if r := top["resolution_requests"].([]interface{}); len(r) != 0 {
		t.Errorf("top resolution_requests = %v", r)
	}
	if top["resolved_through"] != "2001-06-30" || top["items"] != nil {
		t.Errorf("top = %v", top)
	}
}

func TestSplitCaps(t *testing.T) {
	m := mustManifest(t, fixture(20, 10))
	s, _ := SplitManifest(m, 0, 30)
	for i, p := range s.Parts {
		if p.Bars > 30 || (i < len(s.Parts)-1 && p.Items != 3) {
			t.Errorf("part: %d items, %d bars", p.Items, p.Bars)
		}
	}
	if len(s.Parts) != 7 {
		t.Errorf("parts = %d, want 7", len(s.Parts))
	}

	full, _ := SplitManifest(m, 0, 0)
	if len(full.Parts) != 1 {
		t.Fatalf("no caps: %d parts", len(full.Parts))
	}
	limit := len(full.Parts[0].Body) / 4
	s, _ = SplitManifest(m, limit, 0)
	if len(s.Parts) < 4 {
		t.Errorf("byte cap gave %d parts", len(s.Parts))
	}
	for i, p := range s.Parts {
		if len(p.Body) > limit {
			t.Errorf("part %d is %d bytes > %d", i, len(p.Body), limit)
		}
	}
	// The exact size bookkeeping: a cap of exactly one part's size keeps it whole.
	exact, _ := SplitManifest(m, len(full.Parts[0].Body), 0)
	if len(exact.Parts) != 1 {
		t.Errorf("cap = body size gave %d parts", len(exact.Parts))
	}
	under, _ := SplitManifest(m, len(full.Parts[0].Body)-1, 0)
	if len(under.Parts) != 2 {
		t.Errorf("cap = body size - 1 gave %d parts", len(under.Parts))
	}

	// One item alone over a cap is reported.
	s, _ = SplitManifest(m, 0, 5)
	if len(s.Oversized) != 20 || s.Oversized[0] != (Violation{Path: "items[b000]", Code: "part_too_many_bars"}) {
		t.Errorf("oversized = %v", s.Oversized)
	}
	s, _ = SplitManifest(m, 100, 0)
	if len(s.Oversized) != 20 || s.Oversized[0].Code != "part_too_large" {
		t.Errorf("oversized = %v", s.Oversized)
	}
}

func render(r *Report) string {
	var b strings.Builder
	for _, v := range append(r.Errors, r.Warnings...) {
		b.WriteString(v.String() + "\n")
	}
	return b.String()
}

func TestCheckReportsPathsAndCodesOnly(t *testing.T) {
	root := fixture(3, 4)
	items := root["items"].([]interface{})
	secret := "SECRET-ANSWER-" + strings.Repeat("é", 300) // 314 code points, > 255
	item0 := items[0].(map[string]interface{})
	item0["title"] = secret
	item0["market"].(map[string]interface{})["code"] = "Bad Code SECRET"
	curve := item0["curves"].([]interface{})[0].(map[string]interface{})
	curve["labels"] = []interface{}{"a", "b", "SECRET-LABEL"} // 3 of 4
	curve["p0"] = []interface{}{json.Number("0.1")}
	item1 := items[1].(map[string]interface{})
	delete(item1["curves"].([]interface{})[0].(map[string]interface{}), "closes_at")
	item1["title"] = "A seminal SECRET result"
	item2 := items[2].(map[string]interface{})
	item2["market"].(map[string]interface{})["background_info_html"] = "<p>As of 2010 the SECRET was known.</p>"
	item2["submarkets"] = []interface{}{}
	// A label of exactly 255 code points (multibyte) is fine.
	item2["curves"].([]interface{})[0].(map[string]interface{})["labels"] = strings.Repeat("ü", 255)
	root["disclosures"] = append(root["disclosures"].([]interface{}), map[string]interface{}{
		"key": "d-x", "item": "nope", "title": "t", "source_url": "ftp://SECRET",
	})
	root["schema_version"] = json.Number("1")

	m := mustManifest(t, root)
	report, err := Check(m, testLimits(), "Bad_Slug")
	if err != nil {
		t.Fatal(err)
	}
	out := render(report)
	for _, want := range []string{
		"schema_version: unsupported_schema_version",
		"--curriculum: pattern",
		"items[b000].title: too_long",
		"items[b000].market.code: pattern",
		"items[b000].curves[0].labels: length_mismatch",
		"items[b000].curves[0].p0: length_mismatch",
		"items[b001].curves[0].closes_at: required",
		"items[b002].submarkets: not_in_schema_version_2",
		"disclosures[4].item: unknown_item",
		"disclosures[4].source_url: pattern",
		`items[b001].title: T5 "seminal"`,
		`items[b002].market.background_info_html: T5 "/as of 20\\d\\d/"`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SECRET") || strings.Contains(out, "é") {
		t.Errorf("the report leaks a value:\n%s", out)
	}
	if strings.Contains(out, "items[b002].curves[0].labels") {
		t.Errorf("a 255-code-point label was flagged:\n%s", out)
	}
	if report.OK() || len(report.Warnings) != 2 {
		t.Errorf("errors %d, warnings %d", len(report.Errors), len(report.Warnings))
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "SECRET") {
		t.Errorf("the JSON report leaks a value")
	}
}

func TestCheckCountsAndCleanManifest(t *testing.T) {
	m := mustManifest(t, fixture(5, 4))
	report, err := Check(m, testLimits(), "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() || len(report.Warnings) != 0 {
		t.Fatalf("unexpected problems:\n%s", render(report))
	}
	c := report.Counts
	if c.Checkpoints != 2 || c.Items != 5 || c.Bars != 20 || c.Parts != 1 || c.Bytes != m.Bytes || c.TopBytes == 0 {
		t.Errorf("counts = %+v", c)
	}

	limits := testLimits()
	limits.Manifest.MaxItems = 4
	limits.Manifest.MaxBarsPerItem = 3
	limits.Manifest.MaxBars = 10
	limits.Manifest.MaxTopBytes = 10
	limits.Manifest.MaxBarsPerPart = 4
	limits.Manifest.MaxParts = 2
	report, _ = Check(m, limits, "")
	out := render(report)
	for _, want := range []string{"items: too_many_items", "items[b000]: too_many_bars", "items: too_many_bars", "top: top_too_large", "items: too_many_parts"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHindsightWholeWords(t *testing.T) {
	root := map[string]interface{}{
		"checkpoints": []interface{}{
			map[string]interface{}{"title": "Seminally speaking", "body_html": "<p>It <i>would later</i> matter</p>"},
			map[string]interface{}{"title": "SEMINAL!", "body_html": "as of 2019"},
			map[string]interface{}{"title": "as of 20199", "body_html": "would laterally"},
		},
	}
	got := Hindsight(root, []string{"seminal", "would later", `/as of 20\d\d/`})
	var lines []string
	for _, v := range got {
		lines = append(lines, v.String())
	}
	want := []string{
		`checkpoints[0].body_html: T5 "would later"`,
		`checkpoints[1].title: T5 "seminal"`,
		`checkpoints[1].body_html: T5 "/as of 20\\d\\d/"`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
