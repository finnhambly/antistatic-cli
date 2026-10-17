package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// JSON values keep their key order, so what the CLI sends and prints reads
// as it came: nil, bool, json.Number, string, []any or Object.

// Object is a JSON object in its key order.
type Object []Member

// Member is one key and value of an Object.
type Member struct {
	Key   string
	Value any
}

// Get returns a key's value and whether it is there.
func (o Object) Get(key string) (any, bool) {
	for i := len(o) - 1; i >= 0; i-- {
		if o[i].Key == key {
			return o[i].Value, true
		}
	}
	return nil, false
}

// ParseJSON parses one JSON value; anything but whitespace after it is an error.
func ParseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid JSON: more after the value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := Object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("invalid JSON object key")
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				obj = obj.set(key, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("invalid JSON")
	default:
		return t, nil
	}
}

// set replaces a key's value in place (as JSON.parse keeps the last), or appends it.
func (o Object) set(key string, v any) Object {
	for i := range o {
		if o[i].Key == key {
			o[i].Value = v
			return o
		}
	}
	return append(o, Member{key, v})
}

// EncodeJSON writes v as JSON.stringify does: compact with indent 0, else
// indented by that many spaces.
func EncodeJSON(v any, indent int) string {
	var b strings.Builder
	encode(&b, v, indent, 0)
	return b.String()
}

func encode(b *strings.Builder, v any, indent, depth int) {
	newline := func(d int) {
		if indent > 0 {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat(" ", indent*d))
		}
	}
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case json.Number:
		b.WriteString(t.String())
	case float64:
		b.WriteString(strconv.FormatFloat(t, 'f', -1, 64))
	case int:
		b.WriteString(strconv.Itoa(t))
	case string:
		writeString(b, t)
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(depth + 1)
			encode(b, x, indent, depth+1)
		}
		newline(depth)
		b.WriteByte(']')
	case Object:
		if len(t) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(depth + 1)
			writeString(b, m.Key)
			b.WriteByte(':')
			if indent > 0 {
				b.WriteByte(' ')
			}
			encode(b, m.Value, indent, depth+1)
		}
		newline(depth)
		b.WriteByte('}')
	default:
		data, _ := json.Marshal(t)
		b.Write(data)
	}
}

// writeString quotes s as JSON.stringify does (no HTML or U+2028 escapes).
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else if r == utf8.RuneError {
				b.WriteString(`�`)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
