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
// In an html block (the run card's widget, its data JSON in a script), a
// plaintext is escaped as a JSON string's content.
func (k *Keys) DecryptText(text string) string {
	if !strings.Contains(text, TokenPrefix) {
		return text
	}
	if at := strings.Index(text, "```html\n"); at >= 0 {
		body := text[at+8:]
		end := strings.Index(body, "```")
		if end < 0 {
			end = len(body)
		}
		return k.decryptText(text[:at+8], false) + k.decryptText(body[:end], true) + k.DecryptText(body[end:])
	}
	return k.decryptText(text, false)
}

func (k *Keys) decryptText(text string, inScript bool) string {
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
			if inScript {
				q := EncodeJSON(pt, 0)
				pt = strings.ReplaceAll(q[1:len(q)-1], "</", `<\/`)
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
			return alignGrid(k.DecryptText(text[:at])) + EncodeJSON(k.DecryptDeep(parsed), indent)
		}
	}
	return alignGrid(k.DecryptText(text))
}

// The run grid's text, encrypted labels and all, comes with a tab between
// each label and its cells (the server can't know the labels' widths):
// once decrypted, the labels are padded (or cut, at 30) to one width.
const gridWidth = 30

func alignGrid(text string) string {
	if !strings.Contains(text, "\t") {
		return text
	}
	lines := strings.Split(text, "\n")
	inBlock := false
	var block []int
	flush := func() {
		width := 0
		for _, i := range block {
			label := strings.SplitN(lines[i], "\t", 2)[0]
			if n := len([]rune(label)); n > width {
				width = n
			}
		}
		if width > gridWidth {
			width = gridWidth
		}
		for _, i := range block {
			parts := strings.SplitN(lines[i], "\t", 2)
			label := []rune(parts[0])
			if len(label) > width {
				label = append(label[:width-1], '…')
			}
			lines[i] = string(label) + strings.Repeat(" ", width-len(label)) + " " + parts[1]
		}
		block = nil
	}
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "```"):
			if inBlock {
				flush()
			}
			inBlock = !inBlock && i > 0 && strings.HasPrefix(lines[i-1], "Run grid")
		case inBlock && strings.Contains(l, "\t"):
			block = append(block, i)
		}
	}
	return strings.Join(lines, "\n")
}
