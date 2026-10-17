package e2e

import (
	"regexp"
	"strings"
)

// Locked stands in for a token that isn't this key's.
const Locked = "🔒"

var sentenceEnd = regexp.MustCompile(`[.!?…][)\]"'’”»]*$`)

// DecryptText decrypts every token in text. The server ends a sentence
// after a token with "."; a plaintext that already ends one keeps its own.
func (k *Keys) DecryptText(text string) string {
	if !strings.Contains(text, TokenPrefix) {
		return text
	}
	plain := map[string]string{}
	var b strings.Builder
	ended := false
	for _, p := range SplitTokens(text) {
		if p.Token == "" {
			t := p.Text
			if ended && strings.HasPrefix(t, ".") {
				t = t[1:]
			}
			b.WriteString(t)
			ended = false
			continue
		}
		pt, ok := plain[p.Token]
		if !ok {
			var err error
			if pt, err = k.Decrypt(p.Token); err != nil {
				pt = Locked
			}
			plain[p.Token] = pt
		}
		b.WriteString(pt)
		ended = sentenceEnd.MatchString(pt)
	}
	return b.String()
}

// DecryptDeep decrypts every string (and object key) in a JSON value.
func (k *Keys) DecryptDeep(v any) any {
	switch t := v.(type) {
	case string:
		return k.DecryptText(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = k.DecryptDeep(x)
		}
		return out
	case Object:
		out := Object{}
		for _, m := range t {
			out = out.set(k.DecryptText(m.Key), k.DecryptDeep(m.Value))
		}
		return out
	}
	return v
}

var jsonLine = regexp.MustCompile(`(?m)^[\[{]`)

// DecryptResult decrypts a tool result's text. Text that is (or ends in)
// JSON is parsed, decrypted field by field and re-serialised, so plaintext
// with quotes or newlines keeps it valid JSON.
func (k *Keys) DecryptResult(text string) string {
	if loc := jsonLine.FindStringIndex(text); loc != nil {
		at := loc[0]
		if parsed, err := ParseJSON([]byte(text[at:])); err == nil {
			indent := 2
			if !strings.Contains(text[at:], "\n") {
				indent = 0
			}
			return k.DecryptText(text[:at]) + EncodeJSON(k.DecryptDeep(parsed), indent)
		}
	}
	return k.DecryptText(text)
}
