package api

import (
	"fmt"
	"net/url"
	"regexp"
)

var pathParam = regexp.MustCompile(`\{[a-z_]+\}`)

// Path fills a contract path template's {name} segments, in order, with the
// escaped args: Path("/markets/{code}/forecast", code). Templates are the
// contract's paths without the /api/v1 prefix, and call sites pass them as
// literals so contract_test.go can check each one against contract/v1.json.
func Path(template string, args ...interface{}) string {
	i := 0
	out := pathParam.ReplaceAllStringFunc(template, func(string) string {
		if i >= len(args) {
			panic(fmt.Sprintf("api.Path(%q): too few arguments", template))
		}
		s := url.PathEscape(fmt.Sprint(args[i]))
		i++
		return s
	})
	if i != len(args) {
		panic(fmt.Sprintf("api.Path(%q): too many arguments", template))
	}
	return out
}
