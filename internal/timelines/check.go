package timelines

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"unicode/utf8"
)

// Violation is one problem: where it is and what kind. It never carries the
// field's value.
type Violation struct {
	Path string `json:"path"`
	Code string `json:"code"`
	// Term is the hindsight term a T5 warning matched (from the limits,
	// never from the manifest).
	Term string `json:"term,omitempty"`
}

func (v Violation) String() string {
	if v.Term != "" {
		return fmt.Sprintf("%s: %s %q", v.Path, v.Code, v.Term)
	}
	return v.Path + ": " + v.Code
}

// Counts summarises a manifest.
type Counts struct {
	Checkpoints int `json:"checkpoints"`
	Items       int `json:"items"`
	Bars        int `json:"bars"`
	Parts       int `json:"parts"`
	Bytes       int `json:"bytes"`
	TopBytes    int `json:"top_bytes"`
}

// Report is the result of Check.
type Report struct {
	Counts   Counts      `json:"counts"`
	Errors   []Violation `json:"errors"`
	Warnings []Violation `json:"warnings"`
	Split    *Split      `json:"-"`
}

// OK is true when there are no errors (warnings are fine).
func (r *Report) OK() bool { return len(r.Errors) == 0 }

type checker struct {
	limits   *Limits
	errors   []Violation
	warnings []Violation
	patterns map[string]*regexp.Regexp
}

func (c *checker) fail(path, code string) {
	c.errors = append(c.errors, Violation{Path: path, Code: code})
}

var topLevelKeys = map[string]bool{
	"schema_version": true, "source": true, "curriculum": true, "checkpoints": true,
	"disclosures": true, "resolution_requests": true, "resolved_through": true, "items": true,
}

var curveKeys = map[string]bool{
	"group": true, "keys": true, "thresholds": true, "p0": true, "labels": true, "end_dates": true,
	"closes_at": true, "resolves_at": true, "outcomes": true, "resolution": true, "liquidity_schedule": true,
}

// Check checks a manifest against the server's limits locally. slug is the
// --curriculum flag ("" to skip its check).
func Check(m *Manifest, limits *Limits, slug string) (*Report, error) {
	c := &checker{limits: limits, patterns: map[string]*regexp.Regexp{}}
	root := m.Root
	ml := limits.Manifest

	// Top-level shape.
	if v, ok := root["schema_version"]; !ok {
		c.fail("schema_version", "required")
	} else if !schemaVersionAllowed(v, limits.SchemaVersions) {
		c.fail("schema_version", "unsupported_schema_version")
	}
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !topLevelKeys[k] {
			if k == "budget_allocation" {
				c.fail(k, "not_in_schema_version_2")
			} else {
				c.fail(k, "unknown_field")
			}
		}
	}
	if src, ok := root["source"]; ok {
		obj, isObj := src.(map[string]interface{})
		switch {
		case !isObj:
			c.fail("source", "type")
		default:
			if v, ok := obj["commit"]; ok && v != nil {
				if _, s := v.(string); !s {
					c.fail("source.commit", "type")
				}
			}
			if v, ok := obj["dirty"]; ok && v != nil {
				if _, b := v.(bool); !b {
					c.fail("source.dirty", "type")
				}
			}
		}
	}
	if _, ok := root["curriculum"].(map[string]interface{}); !ok {
		c.requiredOrType(root, "curriculum", "curriculum")
	}
	for _, name := range []string{"checkpoints", "items"} {
		if _, ok := root[name].([]interface{}); !ok {
			c.requiredOrType(root, name, name)
		}
	}
	for _, name := range []string{"disclosures", "resolution_requests"} {
		if v, ok := root[name]; ok && v != nil {
			if _, isList := v.([]interface{}); !isList {
				c.fail(name, "type")
			}
		}
	}
	if v, ok := root["resolved_through"]; ok && v != nil {
		if _, s := v.(string); !s {
			c.fail("resolved_through", "type")
		}
	}

	if slug != "" {
		c.field("--curriculum", "curriculum.slug", slug)
	}

	checkpoints := asList(root["checkpoints"])
	for i, raw := range checkpoints {
		path := fmt.Sprintf("checkpoints[%d]", i)
		cp, ok := raw.(map[string]interface{})
		if !ok {
			c.fail(path, "type")
			continue
		}
		c.requiredString(cp, path, "key", "checkpoint.key")
		c.optionalString(cp, path, "title", "checkpoint.title")
	}

	items := asList(root["items"])
	totalBars := 0
	seen := map[string]bool{}
	for i, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			c.fail(fmt.Sprintf("items[%d]", i), "type")
			continue
		}
		path := itemPath(i, item)
		c.requiredString(item, path, "key", "item.key")
		if key, ok := item["key"].(string); ok {
			if seen[key] {
				c.fail(path+".key", "duplicate_key")
			}
			seen[key] = true
		}
		c.optionalString(item, path, "title", "item.title")
		if _, has := item["submarkets"]; has {
			c.fail(path+".submarkets", "not_in_schema_version_2")
		}
		if _, has := item["budget_allocation"]; has {
			c.fail(path+".budget_allocation", "not_in_schema_version_2")
		}
		if market, ok := item["market"].(map[string]interface{}); ok {
			c.optionalString(market, path+".market", "code", "question.code")
			c.optionalString(market, path+".market", "title", "market.title")
		} else {
			c.requiredOrType(item, "market", path+".market")
		}
		bars := c.curves(item, path)
		if ml.MaxBarsPerItem > 0 && bars > ml.MaxBarsPerItem {
			c.fail(path, "too_many_bars")
		}
		totalBars += bars
	}

	for i, raw := range asList(root["disclosures"]) {
		path := fmt.Sprintf("disclosures[%d]", i)
		d, ok := raw.(map[string]interface{})
		if !ok {
			c.fail(path, "type")
			continue
		}
		c.requiredString(d, path, "key", "disclosure.key")
		c.optionalString(d, path, "title", "disclosure.title")
		c.optionalString(d, path, "source_url", "disclosure.source_url")
	}
	for i, raw := range asList(root["resolution_requests"]) {
		path := fmt.Sprintf("resolution_requests[%d]", i)
		r, ok := raw.(map[string]interface{})
		if !ok {
			c.fail(path, "type")
			continue
		}
		c.requiredString(r, path, "key", "resolution_request.key")
	}

	// Counts against the manifest limits.
	if ml.MaxItems > 0 && len(items) > ml.MaxItems {
		c.fail("items", "too_many_items")
	}
	if ml.MaxCheckpoints > 0 && len(checkpoints) > ml.MaxCheckpoints {
		c.fail("checkpoints", "too_many_checkpoints")
	}
	if ml.MaxBars > 0 && totalBars > ml.MaxBars {
		c.fail("items", "too_many_bars")
	}

	split, err := SplitManifest(m, ml.MaxPartBytes, ml.MaxBarsPerPart)
	if err != nil {
		return nil, err
	}
	c.errors = append(c.errors, split.Orphans...)
	c.errors = append(c.errors, split.Oversized...)
	if ml.MaxParts > 0 && len(split.Parts) > ml.MaxParts {
		c.fail("items", "too_many_parts")
	}
	if ml.MaxTopBytes > 0 && len(split.Top) > ml.MaxTopBytes {
		c.fail("top", "top_too_large")
	}

	c.warnings = append(c.warnings, Hindsight(root, limits.HindsightTerms)...)

	return &Report{
		Counts: Counts{
			Checkpoints: len(checkpoints),
			Items:       len(items),
			Bars:        totalBars,
			Parts:       len(split.Parts),
			Bytes:       m.Bytes,
			TopBytes:    len(split.Top),
		},
		Errors:   nonNil(c.errors),
		Warnings: nonNil(c.warnings),
		Split:    split,
	}, nil
}

func nonNil(v []Violation) []Violation {
	if v == nil {
		return []Violation{}
	}
	return v
}

func schemaVersionAllowed(v interface{}, allowed []int) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	n, err := num.Int64()
	if err != nil {
		return false
	}
	for _, a := range allowed {
		if int64(a) == n {
			return true
		}
	}
	return false
}

func (c *checker) requiredOrType(obj map[string]interface{}, key, path string) {
	if v, ok := obj[key]; !ok || v == nil {
		c.fail(path, "required")
	} else {
		c.fail(path, "type")
	}
}

func (c *checker) requiredString(obj map[string]interface{}, base, key, limit string) {
	v, ok := obj[key]
	if !ok || v == nil {
		c.fail(base+"."+key, "required")
		return
	}
	c.stringValue(base+"."+key, limit, v)
}

func (c *checker) optionalString(obj map[string]interface{}, base, key, limit string) {
	if v, ok := obj[key]; ok && v != nil {
		c.stringValue(base+"."+key, limit, v)
	}
}

func (c *checker) stringValue(path, limit string, v interface{}) {
	s, ok := v.(string)
	if !ok {
		c.fail(path, "type")
		return
	}
	c.field(path, limit, s)
}

// field checks s against the named field limit.
func (c *checker) field(path, limit, s string) {
	fl, ok := c.limits.Fields[limit]
	if !ok {
		return
	}
	n := utf8.RuneCountInString(s)
	if fl.Min != nil && n < *fl.Min {
		c.fail(path, "too_short")
	}
	if fl.Max != nil && n > *fl.Max {
		c.fail(path, "too_long")
	}
	if fl.Pattern != "" {
		re, seen := c.patterns[fl.Pattern]
		if !seen {
			re, _ = regexp.Compile(fl.Pattern) // a pattern Go can't compile is skipped
			c.patterns[fl.Pattern] = re
		}
		if re != nil && !re.MatchString(s) {
			c.fail(path, "pattern")
		}
	}
}

// curves checks an item's curves and returns its bar count.
func (c *checker) curves(item map[string]interface{}, path string) int {
	raw, ok := item["curves"]
	if !ok || raw == nil {
		c.fail(path+".curves", "required")
		return 0
	}
	curves, ok := raw.([]interface{})
	if !ok {
		c.fail(path+".curves", "type")
		return 0
	}
	bars := 0
	for ci, cr := range curves {
		cpath := fmt.Sprintf("%s.curves[%d]", path, ci)
		curve, ok := cr.(map[string]interface{})
		if !ok {
			c.fail(cpath, "type")
			continue
		}
		names := make([]string, 0, len(curve))
		for k := range curve {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if !curveKeys[k] {
				c.fail(cpath+"."+k, "unknown_field")
			}
		}

		keysRaw, ok := curve["keys"]
		if !ok || keysRaw == nil {
			c.fail(cpath+".keys", "required")
			continue
		}
		keys, ok := keysRaw.([]interface{})
		if !ok {
			c.fail(cpath+".keys", "type")
			continue
		}
		n := len(keys)
		if n == 0 {
			c.fail(cpath+".keys", "empty")
		}
		bars += n
		for j, k := range keys {
			c.stringValue(fmt.Sprintf("%s.keys[%d]", cpath, j), "bar.key", k)
		}
		if g, ok := curve["group"]; ok && g != nil {
			c.stringValue(cpath+".group", "bar.projection_group", g)
		}

		// Always arrays of n.
		for _, name := range []string{"thresholds", "p0"} {
			v, ok := curve[name]
			if !ok || v == nil {
				c.fail(cpath+"."+name, "required")
				continue
			}
			list, isList := v.([]interface{})
			if !isList {
				c.fail(cpath+"."+name, "type")
			} else if len(list) != n {
				c.fail(cpath+"."+name, "length_mismatch")
			}
		}
		// Scalar or an array of n.
		if v, ok := curve["closes_at"]; !ok || v == nil {
			c.fail(cpath+".closes_at", "required")
		}
		for _, name := range []string{"labels", "end_dates", "closes_at", "resolves_at", "outcomes", "resolution", "liquidity_schedule"} {
			v, ok := curve[name]
			if !ok {
				continue
			}
			if list, isList := v.([]interface{}); isList {
				if len(list) != n {
					c.fail(cpath+"."+name, "length_mismatch")
					continue
				}
				if name == "labels" {
					for j, l := range list {
						if l != nil {
							c.stringValue(fmt.Sprintf("%s.labels[%d]", cpath, j), "bar.label", l)
						}
					}
				}
			} else if name == "labels" && v != nil {
				c.stringValue(cpath+".labels", "bar.label", v)
			}
		}
	}
	return bars
}
