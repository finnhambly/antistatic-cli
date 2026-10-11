package sandbox

import "fmt"

// Part is one upload part: the canonical JSON of
// {"disclosures", "items", "resolution_requests"} for consecutive items.
type Part struct {
	Body   []byte
	SHA256 string
	Items  int
	Bars   int
}

// Split is the result of splitting a manifest.
type Split struct {
	Parts []Part
	// Top is the canonical JSON of the manifest PUT's "top" object.
	Top []byte
	// TopValue is the decoded top object Top encodes.
	TopValue map[string]interface{}
	// Oversized lists items that alone exceed max_part_bytes
	// ("part_too_large") or max_bars_per_part ("part_too_many_bars").
	Oversized []Violation
	// Orphans lists disclosures/resolution requests naming an item the
	// manifest doesn't have, and requests naming no item.
	Orphans []Violation
}

// emptyPartBytes is len(`{"disclosures":[],"items":[],"resolution_requests":[]}`).
const emptyPartBytes = 54

type packed struct {
	item       interface{}
	key        string
	path       string
	itemBytes  int
	disc, reqs []interface{}
	discBytes  int // sum of the encoded disclosures
	reqBytes   int
	bars       int
}

// SplitManifest builds parts deterministically: items are packed in order
// into a part while its body stays within maxBytes and maxBars (0 = no cap).
// Each item's attached disclosures and resolution requests ("item": key)
// travel in its part, in manifest order. An item that alone exceeds a cap
// gets a part of its own and is reported in Oversized.
func SplitManifest(m *Manifest, maxBytes, maxBars int) (*Split, error) {
	root := m.Root
	items := asList(root["items"])
	disclosures := asList(root["disclosures"])
	requests := asList(root["resolution_requests"])

	out := &Split{}
	keys := map[string]int{}
	entries := make([]*packed, len(items))
	for i, raw := range items {
		obj := asObject(raw)
		key, _ := obj["key"].(string)
		b, err := Canonical(raw)
		if err != nil {
			return nil, err
		}
		entries[i] = &packed{item: raw, key: key, path: itemPath(i, obj), itemBytes: len(b), bars: itemBars(obj)}
		if key != "" {
			if _, dup := keys[key]; !dup {
				keys[key] = i
			}
		}
	}

	var unattached []interface{}
	for i, d := range disclosures {
		owner, has := asObject(d)["item"]
		if !has || owner == nil {
			unattached = append(unattached, d)
			continue
		}
		key, _ := owner.(string)
		at, ok := keys[key]
		if !ok {
			out.Orphans = append(out.Orphans, Violation{Path: fmt.Sprintf("disclosures[%d].item", i), Code: "unknown_item"})
			continue
		}
		b, err := Canonical(d)
		if err != nil {
			return nil, err
		}
		entries[at].disc = append(entries[at].disc, d)
		entries[at].discBytes += len(b)
	}
	for i, r := range requests {
		owner, has := asObject(r)["item"]
		if !has || owner == nil {
			out.Orphans = append(out.Orphans, Violation{Path: fmt.Sprintf("resolution_requests[%d].item", i), Code: "required"})
			continue
		}
		key, _ := owner.(string)
		at, ok := keys[key]
		if !ok {
			out.Orphans = append(out.Orphans, Violation{Path: fmt.Sprintf("resolution_requests[%d].item", i), Code: "unknown_item"})
			continue
		}
		b, err := Canonical(r)
		if err != nil {
			return nil, err
		}
		entries[at].reqs = append(entries[at].reqs, r)
		entries[at].reqBytes += len(b)
	}

	// A running part, sized exactly as its canonical encoding will be.
	var cur []*packed
	var nItems, nDisc, nReq, contentBytes, bars int
	size := func(ni, nd, nr, content int) int {
		s := emptyPartBytes + content
		for _, n := range []int{ni, nd, nr} {
			if n > 1 {
				s += n - 1 // commas
			}
		}
		return s
	}
	flush := func() error {
		if len(cur) == 0 {
			return nil
		}
		body := map[string]interface{}{"items": []interface{}{}, "disclosures": []interface{}{}, "resolution_requests": []interface{}{}}
		var its, ds, rs []interface{}
		for _, e := range cur {
			its = append(its, e.item)
			ds = append(ds, e.disc...)
			rs = append(rs, e.reqs...)
		}
		body["items"] = its
		if len(ds) > 0 {
			body["disclosures"] = ds
		}
		if len(rs) > 0 {
			body["resolution_requests"] = rs
		}
		b, err := Canonical(body)
		if err != nil {
			return err
		}
		out.Parts = append(out.Parts, Part{Body: b, SHA256: SHA256Hex(b), Items: len(cur), Bars: bars})
		cur, nItems, nDisc, nReq, contentBytes, bars = nil, 0, 0, 0, 0, 0
		return nil
	}

	for _, e := range entries {
		alone := size(1, len(e.disc), len(e.reqs), e.itemBytes+e.discBytes+e.reqBytes)
		if maxBytes > 0 && alone > maxBytes {
			out.Oversized = append(out.Oversized, Violation{Path: e.path, Code: "part_too_large"})
		}
		if maxBars > 0 && e.bars > maxBars {
			out.Oversized = append(out.Oversized, Violation{Path: e.path, Code: "part_too_many_bars"})
		}
		ni, nd, nr := nItems+1, nDisc+len(e.disc), nReq+len(e.reqs)
		content := contentBytes + e.itemBytes + e.discBytes + e.reqBytes
		if len(cur) > 0 && ((maxBytes > 0 && size(ni, nd, nr, content) > maxBytes) || (maxBars > 0 && bars+e.bars > maxBars)) {
			if err := flush(); err != nil {
				return nil, err
			}
			ni, nd, nr = 1, len(e.disc), len(e.reqs)
			content = e.itemBytes + e.discBytes + e.reqBytes
		}
		cur = append(cur, e)
		nItems, nDisc, nReq, contentBytes = ni, nd, nr, content
		bars += e.bars
	}
	if err := flush(); err != nil {
		return nil, err
	}

	top := map[string]interface{}{
		"curriculum":          root["curriculum"],
		"checkpoints":         root["checkpoints"],
		"disclosures":         []interface{}{},
		"resolution_requests": []interface{}{},
	}
	if unattached != nil {
		top["disclosures"] = unattached
	}
	if top["checkpoints"] == nil {
		top["checkpoints"] = []interface{}{}
	}
	if rt, ok := root["resolved_through"]; ok && rt != nil {
		top["resolved_through"] = rt
	}
	b, err := Canonical(top)
	if err != nil {
		return nil, err
	}
	out.Top, out.TopValue = b, top
	return out, nil
}

// itemPath names an item by its key when it has a usable one, else by index.
func itemPath(i int, item map[string]interface{}) string {
	if key, ok := item["key"].(string); ok && key != "" && len(key) <= 255 && safeKey(key) {
		return "items[" + key + "]"
	}
	return fmt.Sprintf("items[%d]", i)
}

// safeKey keeps a key out of paths when it couldn't be a plain identifier.
func safeKey(key string) bool {
	for _, r := range key {
		if r < 0x21 || r == '[' || r == ']' || r > 0x7e {
			return false
		}
	}
	return true
}
