package cmd

// The CLI against the server's API contract, pinned at contract/v1.json (a
// copy of https://antistatic.exchange/api/v1/contract.json; the
// contract-drift workflow reports when production's differs).
//
// 1. Every API call in cmd/*.go names a literal path template, as
//    client.Get("/markets", ...) or client.Get(api.Path("/markets/{code}", code), ...),
//    and each METHOD /api/v1<template> is a route in the contract.
// 2. Each response struct the CLI decodes accepts the route's example, and
//    every field it reads is one the route's response schema describes.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type contractDoc struct {
	raw    map[string]interface{}
	routes map[string]map[string]interface{}
}

func loadContract(t *testing.T) contractDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "contract", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	doc := contractDoc{raw: raw, routes: map[string]map[string]interface{}{}}
	for _, r := range raw["routes"].([]interface{}) {
		route := r.(map[string]interface{})
		doc.routes[route["method"].(string)+" "+route["path"].(string)] = route
	}
	return doc
}

var clientMethods = map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE"}

// apiCalls lists "METHOD /api/v1<template>" for every client call in cmd/*.go.
func apiCalls(t *testing.T) (calls map[string][]string, problems []string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls = map[string][]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			method, isMethod := clientMethods[sel.Sel.Name]
			if !ok || recv.Name != "client" || !isMethod || len(call.Args) == 0 {
				return true
			}
			pos := fset.Position(call.Pos()).String()
			template, ok := literalTemplate(call.Args[0])
			if !ok {
				problems = append(problems, pos+": the path must be a literal or api.Path(\"literal\", ...)")
				return true
			}
			key := method + " /api/v1" + template
			calls[key] = append(calls[key], pos)
			return true
		})
	}
	return calls, problems
}

func literalTemplate(arg ast.Expr) (string, bool) {
	if call, ok := arg.(*ast.CallExpr); ok {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Path" || len(call.Args) == 0 {
			return "", false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "api" {
			return "", false
		}
		arg = call.Args[0]
	}
	lit, ok := arg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func TestEveryAPICallIsInTheContract(t *testing.T) {
	doc := loadContract(t)
	calls, problems := apiCalls(t)
	for _, p := range problems {
		t.Error(p)
	}
	if len(calls) < 30 {
		t.Fatalf("found only %d API calls; is the scan still matching client.Get/Post/...?", len(calls))
	}
	keys := make([]string, 0, len(calls))
	for k := range calls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := doc.routes[k]; !ok {
			t.Errorf("%s (called at %s) is not in contract/v1.json", k, strings.Join(calls[k], ", "))
		}
	}
}

// Response structs the CLI decodes, by route and the key the payload sits
// under ("" for the whole body).
var contractDecodes = []struct {
	route  string
	under  string
	target func() interface{}
}{
	{"GET /api/v1/markets/{code}/forecast", "data", func() interface{} { return &forecastLookupPayload{} }},
	{"GET /api/v1/markets/{code}/forecast", "data", func() interface{} { return &draftForecastPayload{} }},
	{"GET /api/v1/markets/{code}/forecast", "data", func() interface{} { return &multicountForecastEnvelope{} }},
	{"GET /api/v1/markets/{code}/quote", "data", func() interface{} { return &quoteResponse{} }},
	{"GET /api/v1/markets/{code}/points", "data", func() interface{} { return &pointsPayload{} }},
	{"POST /api/v1/market-specs/preview", "data", func() interface{} { return &specPreview{} }},
	{"GET /api/v1/profile/summary", "data", func() interface{} { return &profileSummary{} }},
	{"GET /api/v1/profile/forecast-history", "data", func() interface{} { return &profileHistoryPayload{} }},
	{"GET /api/v1/profile/liquidity-decay", "data", func() interface{} { return &profileLiquidityPayload{} }},
	{"GET /api/v1/markets/{code}/comments", "data", func() interface{} { return &commentsPayload{} }},
	{"GET /api/v1/me/bot", "bot", func() interface{} { return &botInfo{} }},
	{"GET /api/v1/positions", "data", func() interface{} { return &[]positionRow{} }},
	{"GET /api/v1/markets/{code}/positions", "data", func() interface{} { return &[]positionRow{} }},
	{"GET /api/v1/retrocasting/runs", "data", func() interface{} { return &[]retroRun{} }},
	{"POST /api/v1/retrocasting/runs/{id}/advance", "data", func() interface{} { return &retroRun{} }},
}

func TestResponseStructsMatchTheContract(t *testing.T) {
	doc := loadContract(t)
	for _, tc := range contractDecodes {
		target := tc.target()
		name := fmt.Sprintf("%s %T", tc.route, target)
		t.Run(name, func(t *testing.T) {
			route, ok := doc.routes[tc.route]
			if !ok {
				t.Fatalf("%s is not in the contract", tc.route)
			}
			example := route["example"].(map[string]interface{})["response"].(map[string]interface{})
			status := fmt.Sprint(example["status"])
			body := example["body"]
			schema, ok := route["responses"].(map[string]interface{})[status]
			if !ok {
				t.Fatalf("no schema for status %s", status)
			}
			if tc.under != "" {
				body = body.(map[string]interface{})[tc.under]
				schema = doc.property(schema, tc.under)
				if schema == nil {
					t.Fatalf("the response schema has no %q", tc.under)
				}
			}

			raw, _ := json.Marshal(body)
			if err := json.Unmarshal(raw, target); err != nil {
				t.Errorf("the example doesn't decode: %v", err)
			}
			for _, p := range doc.checkFields(schema, reflect.TypeOf(target), tc.under) {
				t.Error(p)
			}
		})
	}
}

// resolve follows a local $ref.
func (d contractDoc) resolve(schema interface{}) map[string]interface{} {
	m, _ := schema.(map[string]interface{})
	for m != nil {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var node interface{} = d.raw
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			node = node.(map[string]interface{})[part]
		}
		m, _ = node.(map[string]interface{})
	}
	return m
}

// properties merges the properties a schema (and its anyOf/oneOf/allOf
// branches) declares; nil when it declares none, so any field is allowed.
func (d contractDoc) properties(schema interface{}) map[string]interface{} {
	m := d.resolve(schema)
	if m == nil {
		return nil
	}
	var out map[string]interface{}
	if props, ok := m["properties"].(map[string]interface{}); ok {
		out = map[string]interface{}{}
		for k, v := range props {
			out[k] = v
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		branches, _ := m[key].([]interface{})
		for _, b := range branches {
			if props := d.properties(b); props != nil {
				if out == nil {
					out = map[string]interface{}{}
				}
				for k, v := range props {
					out[k] = v
				}
			}
		}
	}
	return out
}

func (d contractDoc) property(schema interface{}, name string) interface{} {
	props := d.properties(schema)
	if props == nil {
		return nil
	}
	return props[name]
}

func (d contractDoc) checkFields(schema interface{}, typ reflect.Type, path string) []string {
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	m := d.resolve(schema)
	if m == nil {
		return nil
	}
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		return d.checkFields(m["items"], typ.Elem(), path+"[]")
	case reflect.Map:
		return d.checkFields(m["additionalProperties"], typ.Elem(), path+".*")
	case reflect.Struct:
		props := d.properties(m)
		if props == nil {
			return nil
		}
		var problems []string
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" || !field.IsExported() {
				continue
			}
			sub, ok := props[name]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s.%s (%s.%s) is not in the contract's response schema", path, name, typ.Name(), field.Name))
				continue
			}
			problems = append(problems, d.checkFields(sub, field.Type, path+"."+name)...)
		}
		return problems
	}
	return nil
}
