package sandbox

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// T5 is the hindsight-wording rule: learner-visible text that gives away
// what happened later ("seminal", "would later", "as of 2010").
const T5 = "T5"

type hindsightTerm struct {
	term string
	re   *regexp.Regexp
}

// compileTerms compiles hindsight terms: a plain term matches literally, an
// entry written /.../ is a regular expression; both case-insensitively and
// only on whole words. Terms Go can't compile are skipped.
func compileTerms(terms []string) []hindsightTerm {
	var out []hindsightTerm
	for _, term := range terms {
		pattern := regexp.QuoteMeta(term)
		if len(term) >= 2 && strings.HasPrefix(term, "/") && strings.HasSuffix(term, "/") {
			pattern = term[1 : len(term)-1]
		}
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			continue
		}
		out = append(out, hindsightTerm{term: term, re: re})
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// matchesWholeWord reports whether re matches text at word boundaries: a
// match that starts (ends) with a word character must not be preceded
// (followed) by one.
func matchesWholeWord(re *regexp.Regexp, text string) bool {
	for _, loc := range re.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if start == end {
			continue
		}
		first, _ := utf8.DecodeRuneInString(text[start:])
		last, _ := utf8.DecodeLastRuneInString(text[:end])
		if isWordRune(first) && start > 0 {
			prev, _ := utf8.DecodeLastRuneInString(text[:start])
			if isWordRune(prev) {
				continue
			}
		}
		if isWordRune(last) && end < len(text) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			if isWordRune(next) {
				continue
			}
		}
		return true
	}
	return false
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// visibleText strips HTML tags and decodes entities.
func visibleText(s string) string {
	return html.UnescapeString(htmlTag.ReplaceAllString(s, " "))
}

// Hindsight scans the learner-visible strings for hindsight terms and
// returns one T5 warning per (path, term).
func Hindsight(root map[string]interface{}, terms []string) []Violation {
	compiled := compileTerms(terms)
	if len(compiled) == 0 {
		return nil
	}
	var out []Violation
	scan := func(path string, v interface{}) {
		s, ok := v.(string)
		if !ok || s == "" {
			return
		}
		text := visibleText(s)
		for _, t := range compiled {
			if matchesWholeWord(t.re, text) {
				out = append(out, Violation{Path: path, Code: T5, Term: t.term})
			}
		}
	}
	for i, raw := range asList(root["items"]) {
		item := asObject(raw)
		if item == nil {
			continue
		}
		path := itemPath(i, item)
		scan(path+".title", item["title"])
		if market := asObject(item["market"]); market != nil {
			for _, f := range []string{"title", "resolution_criteria", "background_info_html"} {
				scan(path+".market."+f, market[f])
			}
		}
	}
	for _, list := range []string{"checkpoints", "disclosures", "resolution_requests"} {
		for i, raw := range asList(root[list]) {
			obj := asObject(raw)
			if obj == nil {
				continue
			}
			for _, f := range []string{"title", "body_html"} {
				scan(fmt.Sprintf("%s[%d].%s", list, i, f), obj[f])
			}
		}
	}
	return out
}
