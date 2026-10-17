package e2e

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The field schema of encrypted runs: a copy of the site's
// priv/e2e/private-fields.json (served at /e2e/fields.json; the
// contract-drift workflow compares them). Every field of every tool an
// encrypted run may use is text (encrypted whole), lines (a script,
// encrypted line by line), key (a client-chosen id, encrypted), opaque (a
// client id, hashed), drop (left out), unit (encrypted around the plain X)
// or a plain format; anything else is refused.
//
//go:embed private-fields.json
var SchemaJSON []byte

type schema struct {
	formats map[string]Object
	types   map[string]any
	tools   map[string]any
}

var fieldSchema = mustSchema(SchemaJSON)

func mustSchema(data []byte) *schema {
	v, err := ParseJSON(data)
	if err != nil {
		panic("e2e: private-fields.json: " + err.Error())
	}
	root := v.(Object)
	s := &schema{formats: map[string]Object{}, types: map[string]any{}, tools: map[string]any{}}
	get := func(k string) Object { x, _ := root.Get(k); return x.(Object) }
	for _, m := range get("formats") {
		s.formats[m.Key] = m.Value.(Object)
	}
	for _, m := range get("types") {
		s.types[m.Key] = m.Value
	}
	for _, m := range get("tools") {
		s.tools[m.Key] = m.Value
	}
	return s
}

// HasTool reports whether encrypted runs may use the tool.
func HasTool(tool string) bool {
	_, ok := fieldSchema.tools[tool]
	return ok
}

var cryptoKinds = map[string]bool{"text": true, "lines": true, "key": true, "opaque": true, "drop": true, "unit": true}

// node is a resolved schema node.
type node struct {
	kind    string // text, lines, key, opaque, drop, unit
	format  string
	fType   string
	pattern *regexp.Regexp
	enum    []any
	union   []any
	object  Object
	array   any
	mapKeys any
	mapVals any
	json    any
	sw      string
	cases   Object
}

var patterns = map[string]*regexp.Regexp{}

// RE2 caps repeat counts at 1000; a larger cap reads as unbounded.
var bigRepeat = regexp.MustCompile(`\{(\d+),(\d{4,})\}`)

func compilePattern(p string) *regexp.Regexp {
	if re, ok := patterns[p]; ok {
		return re
	}
	re, err := regexp.Compile(p)
	if err != nil {
		re = regexp.MustCompile(bigRepeat.ReplaceAllString(p, "{$1,}"))
	}
	patterns[p] = re
	return re
}

func (s *schema) resolve(n any) (*node, error) {
	switch t := n.(type) {
	case string:
		if cryptoKinds[t] {
			return &node{kind: t}, nil
		}
		if f, ok := s.formats[t]; ok {
			out := &node{format: t}
			if ty, ok := f.Get("type"); ok {
				out.fType = ty.(string)
			}
			if p, ok := f.Get("pattern"); ok {
				out.pattern = compilePattern(p.(string))
			}
			return out, nil
		}
		if ty, ok := s.types[t]; ok {
			return s.resolve(ty)
		}
		return nil, fmt.Errorf("schema: unknown kind %s", t)
	case []any:
		return &node{union: t}, nil
	case Object:
		if ref, ok := t.Get("ref"); ok {
			return s.resolve(s.types[ref.(string)])
		}
		out := &node{}
		for _, m := range t {
			switch m.Key {
			case "object":
				out.object = m.Value.(Object)
			case "array":
				out.array = m.Value
			case "enum":
				out.enum = m.Value.([]any)
			case "json":
				out.json = m.Value
			case "switch":
				out.sw = m.Value.(string)
			case "cases":
				out.cases = m.Value.(Object)
			case "map":
				mm := m.Value.(Object)
				out.mapKeys, _ = mm.Get("keys")
				out.mapVals, _ = mm.Get("values")
			}
		}
		if out.object == nil && out.array == nil && out.enum == nil && out.json == nil && out.sw == "" && out.mapKeys == nil {
			// {} (a tool with no fields) is an object with none.
			out.object = Object{}
		}
		return out, nil
	}
	return nil, fmt.Errorf("schema: bad node")
}

func isNumber(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(n.String(), 64)
	return f, err == nil && !math.IsInf(f, 0)
}

func formatFits(n *node, v any) bool {
	switch n.fType {
	case "integer":
		f, ok := isNumber(v)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := isNumber(v)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	s, ok := v.(string)
	return ok && n.pattern != nil && n.pattern.MatchString(s)
}

func jsonEqual(a, b any) bool { return EncodeJSON(a, 0) == EncodeJSON(b, 0) }

func (n *node) fits(v any) bool {
	switch {
	case n.kind != "":
		_, ok := v.(string)
		return ok
	case n.format != "":
		return formatFits(n, v)
	case n.enum != nil:
		for _, e := range n.enum {
			if jsonEqual(e, v) {
				return true
			}
		}
		return false
	case n.object != nil || n.sw != "" || n.mapKeys != nil:
		_, ok := v.(Object)
		return ok
	case n.array != nil:
		_, ok := v.([]any)
		return ok
	case n.json != nil:
		s, ok := v.(string)
		return ok && jsonStart.MatchString(s)
	}
	return false
}

var jsonStart = regexp.MustCompile(`^\s*[\[{]`)

func (s *schema) describe(n *node) string {
	switch {
	case n.kind != "":
		if n.kind == "key" || n.kind == "opaque" {
			return "a key"
		}
		return "text"
	case n.format != "":
		if l, ok := formatLabels[n.format]; ok {
			return l
		}
		return n.format
	case n.enum != nil:
		parts := make([]string, len(n.enum))
		for i, e := range n.enum {
			if str, ok := e.(string); ok {
				parts[i] = str
			} else {
				parts[i] = EncodeJSON(e, 0)
			}
		}
		return "one of " + strings.Join(parts, ", ")
	case n.array != nil:
		return "a list"
	case n.union != nil:
		parts := []string{}
		for _, u := range n.union {
			r, err := s.resolve(u)
			if err == nil {
				parts = append(parts, s.describe(r))
			}
		}
		return strings.Join(parts, " or ")
	}
	return "an object"
}

var formatLabels = map[string]string{
	"int":          "a whole number",
	"number":       "a number",
	"bool":         "true or false",
	"date":         "a date (2026, 2026-10 or 2026-10-31)",
	"period":       "a period (2026-Q4, Q4 2026 or Oct 2026)",
	"run_question": "a run question code (p12-3)",
	"char":         "a character id (c2)",
}

const notAField = "not a field encrypted runs take"

// shown is a plain value for an error message (text isn't echoed).
func shown(v any) string {
	switch t := v.(type) {
	case json.Number, bool, nil:
		return EncodeJSON(t, 0) + " "
	case string:
		if len([]rune(t)) <= 40 {
			return EncodeJSON(t, 0) + " "
		}
	}
	return ""
}

// leafFunc encrypts a text, lines, key, opaque or unit field.
type leafFunc func(kind, value, path string) (string, error)

func (s *schema) walk(n *node, value any, path string, leaf leafFunc, siblings Object) (any, error) {
	if n.union != nil {
		var first error
		for _, u := range n.union {
			opt, err := s.resolve(u)
			if err != nil {
				return nil, err
			}
			if !opt.fits(value) {
				continue
			}
			out, err := s.walk(opt, value, path, leaf, siblings)
			if err == nil {
				return out, nil
			}
			// An option of the wrong shape (a field it doesn't have) gives
			// way to one whose error is about the value.
			if first == nil || (strings.Contains(first.Error(), notAField) && !strings.Contains(err.Error(), notAField)) {
				first = err
			}
		}
		if first != nil {
			return nil, first
		}
		return nil, fmt.Errorf("%s: %sisn't %s", path, shown(value), s.describe(n))
	}
	if !n.fits(value) {
		return nil, fmt.Errorf("%s: %sisn't %s", path, shown(value), s.describe(n))
	}
	switch {
	case n.kind != "":
		return leaf(n.kind, value.(string), path)
	case n.format != "" || n.enum != nil:
		return value, nil
	case n.array != nil:
		item, err := s.resolve(n.array)
		if err != nil {
			return nil, err
		}
		arr := value.([]any)
		out := make([]any, 0, len(arr))
		for i, v := range arr {
			w, err := s.walk(item, v, fmt.Sprintf("%s[%d]", path, i), leaf, nil)
			if err != nil {
				return nil, err
			}
			out = append(out, w)
		}
		return out, nil
	case n.json != nil:
		inner, err := ParseJSON([]byte(value.(string)))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		jn, err := s.resolve(n.json)
		if err != nil {
			return nil, err
		}
		w, err := s.walk(jn, inner, path, leaf, nil)
		if err != nil {
			return nil, err
		}
		return EncodeJSON(w, 0), nil
	case n.sw != "":
		var tag any
		if siblings != nil {
			tag, _ = siblings.Get(n.sw)
		}
		tagS, _ := tag.(string)
		c, ok := n.cases.Get(tagS)
		if !ok || tag == nil {
			return nil, fmt.Errorf("%s: no %s %s", path, n.sw, quoteTag(tag))
		}
		cn, err := s.resolve(c)
		if err != nil {
			return nil, err
		}
		return s.walk(cn, value, path, leaf, nil)
	case n.mapKeys != nil:
		kn, err := s.resolve(n.mapKeys)
		if err != nil {
			return nil, err
		}
		vn, err := s.resolve(n.mapVals)
		if err != nil {
			return nil, err
		}
		out := Object{}
		for _, m := range value.(Object) {
			k, err := s.walk(kn, m.Key, fmt.Sprintf("%s key %s", path, EncodeJSON(m.Key, 0)), leaf, nil)
			if err != nil {
				return nil, err
			}
			v, err := s.walk(vn, m.Value, path+"."+m.Key, leaf, nil)
			if err != nil {
				return nil, err
			}
			out = out.set(k.(string), v)
		}
		return out, nil
	}
	obj := value.(Object)
	out := Object{}
	for _, m := range obj {
		field, ok := n.object.Get(m.Key)
		if !ok {
			return nil, fmt.Errorf("%s.%s: %s", path, m.Key, notAField)
		}
		fn, err := s.resolve(field)
		if err != nil {
			return nil, err
		}
		if fn.kind == "drop" {
			continue
		}
		w, err := s.walk(fn, m.Value, path+"."+m.Key, leaf, obj)
		if err != nil {
			return nil, err
		}
		out = out.set(m.Key, w)
	}
	return out, nil
}

func quoteTag(tag any) string {
	if tag == nil {
		return "undefined"
	}
	return EncodeJSON(tag, 0)
}

// Lines a script's parser reads: its section headings and close stay plain.
var plainLine = regexp.MustCompile(`^\s*(\*\*(Results|Elsewhere|Markets|Questions)\*\*|What do you do\?)\s*$`)

// Markdown markers before a line's text: headings, bullets, numbers, quotes.
var markers = regexp.MustCompile(`^(\s*(?:(?:#{1,6}|[-*+]|\d+[.)])\s+|>\s*)*)`)

var boldLine = regexp.MustCompile(`^(\s*)\*\*(.+?)\*\*(\s*)$`)

// sealFunc encrypts one piece of a field's text.
type sealFunc func(text string) (string, error)

func encryptLine(seal sealFunc, line string) (string, error) {
	if strings.TrimSpace(line) == "" || plainLine.MatchString(line) {
		return line, nil
	}
	if m := boldLine.FindStringSubmatch(line); m != nil {
		t, err := seal(m[2])
		return m[1] + "**" + t + "**" + m[3], err
	}
	prefix := markers.FindStringSubmatch(line)[1]
	rest := line[len(prefix):]
	if strings.TrimSpace(rest) == "" {
		return line, nil
	}
	t, err := seal(rest)
	return prefix + t, err
}

var suffixUnits = map[string]bool{"‰": true, "pp": true, "p.p.": true, "bp": true, "bps": true, "°": true, "°C": true, "°F": true}
var currencyUnit = regexp.MustCompile(`^([A-Z]{0,2}[$£€¥₩₹₽₺₪₫฿₦₱])\s?([a-z]{1,8})?$`)

// UnitTemplate is where the number goes in a unit, as the server reads it:
// "£Xk" stays, "seats" is "X seats", "%" is "X%", "$" is "$X", none is "X".
func UnitTemplate(unit string) string {
	u := strings.TrimSpace(unit)
	if u == "" || u == "date" {
		return "X"
	}
	if strings.Contains(u, "X") {
		return u
	}
	if strings.HasPrefix(u, "%") || suffixUnits[u] {
		return "X" + u
	}
	if m := currencyUnit.FindStringSubmatch(u); m != nil {
		return m[1] + "X" + m[2]
	}
	return "X " + u
}

// encryptUnit encrypts a unit's text around the plain X (and a % right after it).
func encryptUnit(seal sealFunc, unit string) (string, error) {
	u := strings.TrimSpace(unit)
	if u == "" || u == "date" {
		return u, nil
	}
	t := UnitTemplate(u)
	if strings.HasPrefix(t, "X ") {
		return seal(strings.TrimSpace(t[2:]))
	}
	at := strings.Index(t, "X")
	before, after := t[:at], t[at+1:]
	pct := ""
	if strings.HasPrefix(after, "%") {
		pct, after = "%", after[1:]
	}
	out := ""
	if before != "" {
		b, err := seal(before)
		if err != nil {
			return "", err
		}
		out += b
	}
	out += "X" + pct
	if after != "" {
		a, err := seal(after)
		if err != nil {
			return "", err
		}
		out += a
	}
	return out, nil
}

// Shape is the arguments a tool takes, from the schema, two levels deep:
// {run_id: a whole number, spec: {question: when|how_much, ...}}.
func Shape(tool string) string {
	t, ok := fieldSchema.tools[tool]
	if !ok {
		return ""
	}
	n, err := fieldSchema.resolve(toolRoot(t))
	if err != nil {
		return ""
	}
	return fieldSchema.shape(n, 0)
}

func toolRoot(t any) any {
	if o, ok := t.(Object); ok {
		_, hasRef := o.Get("ref")
		_, hasObj := o.Get("object")
		if !hasRef && !hasObj {
			return Object{{"object", o}}
		}
	}
	return t
}

func (s *schema) shape(n *node, depth int) string {
	short := map[string]string{"int": "int", "number": "number", "bool": "bool", "null": "null"}
	switch {
	case n.kind != "":
		if n.kind == "lines" {
			return "text"
		}
		return n.kind
	case n.format != "":
		if v, ok := short[n.format]; ok {
			return v
		}
		return n.format
	case n.enum != nil:
		parts := make([]string, len(n.enum))
		for i, e := range n.enum {
			if str, ok := e.(string); ok {
				parts[i] = str
			} else {
				parts[i] = EncodeJSON(e, 0)
			}
		}
		return strings.Join(parts, "|")
	case n.union != nil:
		parts := []string{}
		for _, u := range n.union {
			if r, err := s.resolve(u); err == nil {
				parts = append(parts, s.shape(r, depth))
			}
		}
		return strings.Join(parts, " or ")
	case n.array != nil:
		if depth >= 2 {
			return "[…]"
		}
		r, err := s.resolve(n.array)
		if err != nil {
			return "[…]"
		}
		return "[" + s.shape(r, depth+1) + "]"
	case n.sw != "":
		return "{…by " + n.sw + "}"
	case n.mapKeys != nil:
		if depth >= 2 {
			return "{…}"
		}
		r, err := s.resolve(n.mapVals)
		if err != nil {
			return "{…}"
		}
		return "{<key>: " + s.shape(r, depth+1) + "}"
	}
	if depth >= 2 {
		return "{…}"
	}
	parts := []string{}
	for _, m := range n.object {
		r, err := s.resolve(m.Value)
		if err != nil || r.kind == "drop" {
			continue
		}
		parts = append(parts, m.Key+": "+s.shape(r, depth+1))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// ErrNoFamily is text to encrypt for a call that names no encrypted run.
var ErrNoFamily = errors.New("not an encrypted run's call: its text can't be encrypted")

// EncryptCall encrypts a call's arguments by the schema, in the run family:
// text and key fields encrypted (each token bound to its field), opaque ids
// hashed, dropped fields left out. A tool or field encrypted runs don't take
// is an error.
func EncryptCall(k *Keys, family, tool string, args any) (any, error) {
	return fieldSchema.encryptCall(k, family, tool, args)
}

func (s *schema) encryptCall(k *Keys, family, tool string, args any) (any, error) {
	t, ok := s.tools[tool]
	if !ok {
		return nil, fmt.Errorf("%s isn't available for encrypted runs", tool)
	}
	n, err := s.resolve(toolRoot(t))
	if err != nil {
		return nil, err
	}
	// market_code is read as code, as the server does.
	if obj, ok := args.(Object); ok && n.object != nil {
		if _, hasCode := n.object.Get("code"); hasCode {
			mc, hasMC := obj.Get("market_code")
			_, argCode := obj.Get("code")
			if hasMC && !argCode {
				rest := Object{}
				for _, m := range obj {
					if m.Key != "market_code" {
						rest = append(rest, m)
					}
				}
				args = append(rest, Member{"code", mc})
			}
		}
	}
	return s.walk(n, args, tool, func(kind, v, path string) (string, error) {
		seal := func(text string) (string, error) {
			if family == "" {
				return "", ErrNoFamily
			}
			return k.Encrypt(family, KindOf(path), text)
		}
		switch kind {
		case "lines":
			lines := strings.Split(v, "\n")
			for i, l := range lines {
				var err error
				if lines[i], err = encryptLine(seal, l); err != nil {
					return "", err
				}
			}
			return strings.Join(lines, "\n"), nil
		case "opaque":
			return k.OpaqueID(v), nil
		case "unit":
			return encryptUnit(seal, v)
		case "text":
			if v == "" {
				return "", nil
			}
		}
		if v == "" {
			return "", fmt.Errorf("%s: a key can't be empty", tool)
		}
		return seal(v)
	}, nil)
}
