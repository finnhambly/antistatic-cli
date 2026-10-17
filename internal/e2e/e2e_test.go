package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// js_vectors.json is made by the site's JS (antistatic_exchange
// test/fixtures/e2e/js_vectors.json): Go must open it and remake its
// tokens exactly (their nonces are given). go_vectors.json is made here
// (go test ./internal/e2e -update) and copied to the site's
// test/fixtures/e2e, where the JS opens its tokens and the server checks its
// calls against the field schema, each token's family and field.
var update = flag.Bool("update", false, "rewrite testdata/go_vectors.json")

const fam = "f0123456789abcdef"

type jsVectors struct {
	UserID string            `json:"user_id"`
	Family string            `json:"family"`
	Raw    map[string]string `json:"raw"`
	Setup  struct {
		Setup
		Phrase      string            `json:"phrase"`
		Raw         map[string]string `json:"raw"`
		Fingerprint string            `json:"fingerprint"`
	} `json:"setup"`
	Tokens []struct {
		Family string `json:"family"`
		Kind   string `json:"kind"`
		Text   string `json:"text"`
		Nonce  string `json:"nonce"`
		Token  string `json:"token"`
	} `json:"tokens"`
	Errors []struct {
		Tool  string          `json:"tool"`
		Args  json.RawMessage `json:"args"`
		Error *string         `json:"error"`
	} `json:"errors"`
	DecryptText []struct {
		Family string `json:"family"`
		In     string `json:"in"`
		Out    string `json:"out"`
	} `json:"decrypt_text"`
	DecryptResult []struct{ In, Out string } `json:"decrypt_result"`
}

func loadJS(t *testing.T) jsVectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "js_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v jsVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func rawOf(t *testing.T, m map[string]string) Raw {
	t.Helper()
	enc, err := FromB64(m["enc"])
	if err != nil {
		t.Fatal(err)
	}
	iv, err := FromB64(m["iv"])
	if err != nil {
		t.Fatal(err)
	}
	return Raw{Enc: enc, IV: iv}
}

func mustKeys(t *testing.T, raw Raw, user string) *Keys {
	t.Helper()
	k, err := ImportKeys(raw, user)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func testKeys(t *testing.T) *Keys {
	return mustKeys(t, Raw{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}, "42")
}

func enc(t *testing.T, k *Keys, family, kind, text string) string {
	t.Helper()
	tok, err := k.Encrypt(family, kind, text)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestWordList(t *testing.T) {
	if len(Words) != 7776 {
		t.Fatalf("%d words", len(Words))
	}
	sum := sha256.Sum256([]byte(strings.Join(Words, "\n") + "\n"))
	if hex.EncodeToString(sum[:]) != WordsSHA256 {
		t.Fatal("sha256")
	}
	var hy []string
	for _, w := range Words {
		if strings.Contains(w, "-") {
			hy = append(hy, w)
		}
	}
	if strings.Join(hy, ",") != "drop-down,felt-tip,t-shirt,yo-yo" {
		t.Fatal(hy)
	}
}

func TestPhrases(t *testing.T) {
	p, err := GeneratePhrase()
	if err != nil {
		t.Fatal(err)
	}
	words := strings.Split(p, " ")
	if len(words) != 6 {
		t.Fatal(p)
	}
	canon := strings.ReplaceAll(p, "-", "")
	for _, in := range []string{"  " + strings.ToUpper(strings.Join(words, "  ")) + " ", strings.Join(words, ", ")} {
		if got, err := ParsePhrase(in); err != nil || got != canon {
			t.Errorf("ParsePhrase(%q) = %q, %v", in, got, err)
		}
	}
	want := "abacus tshirt dropdown yoyo felttip zoom"
	for _, in := range []string{
		"abacus T-Shirt drop down yo,yo feltTIP zoom",
		"abacus-tshirt-drop-down-yo-yo-felt-tip-zoom",
		"abacus tshirt dropdown yoyo felt-tip zoom",
	} {
		if got, err := ParsePhrase(in); err != nil || got != want {
			t.Errorf("ParsePhrase(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParsePhrase(strings.Join(words[:5], " ")); err == nil || err.Error() != "needs 6 words, not 5" {
		t.Error(err)
	}
	typo := Words[100] + "q"
	if _, err := ParsePhrase(typo + " " + strings.Join(words[1:], " ")); err == nil || err.Error() != "not a word in the list: "+typo+" ("+SuggestWord(typo)+"?)" {
		t.Error(err)
	}
	if SuggestWord("qqqqqqqqqqqq") != "" {
		t.Error("suggested a word for nonsense")
	}
}

func TestRandomIndexRejects(t *testing.T) {
	// 0xffffffff is over the limit for n=3; the next draw, 5, is kept.
	r := bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 5, 0, 0, 0})
	if i, err := RandomIndex(3, r); err != nil || i != 5%3 {
		t.Fatal(i, err)
	}
}

func TestTokens(t *testing.T) {
	k42 := testKeys(t)
	k43 := mustKeys(t, Raw{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}, "43")
	tok := enc(t, k42, fam, "take_turn.entries.text", "x")
	if !regexp.MustCompile(`^enc3:[0-9a-f]{32}:` + fam + `\.take_turn\.entries\.text:`).MatchString(tok) {
		t.Fatal(tok)
	}
	if n, _ := PaddedSize(tok); n != 32 {
		t.Fatal(n)
	}
	got, parts, err := k42.Decrypt(tok)
	if err != nil || got != "x" || parts.Family != fam || parts.Kind != "take_turn.entries.text" {
		t.Fatal(got, parts, err)
	}
	if _, _, err := k43.Decrypt(tok); err == nil {
		t.Fatal("opened under another account")
	}
	for n, want := range map[int]int{28: 32, 29: 64, 70000: 131072} {
		if got, _ := PaddedSize(enc(t, k42, fam, "k", strings.Repeat("a", n))); got != want {
			t.Errorf("%d bytes padded to %d, want %d", n, got, want)
		}
	}
	if _, err := ParseToken("enc3:" + strings.Repeat("0", 32) + ":" + fam + ".k:" + ToB64(make([]byte, 28+33))); err == nil {
		t.Error("off-bucket size accepted")
	}
	t64 := enc(t, k42, fam, "k", strings.Repeat("a", 40))
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := t64[len(t64)-2]
	loose := t64[:len(t64)-2] + string(alphabet[strings.IndexByte(alphabet, last)^1]) + "="
	if _, err := ParseToken(loose); err == nil {
		t.Error("non-canonical base64 accepted")
	}
	for _, bad := range []string{"", "enc2:" + ToB64(make([]byte, 60)), "enc3:" + fam + ".k:" + ToB64(make([]byte, 60)), "enc3:" + strings.Repeat("0", 32) + ":g0123456789abcdef.k:" + ToB64(make([]byte, 60)), "enc3:" + strings.Repeat("0", 32) + ":" + fam + ".K:" + ToB64(make([]byte, 60))} {
		if IsToken(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := k42.Encrypt("f123", "k", "x"); err == nil {
		t.Error("a bad family accepted")
	}
	if _, err := k42.Encrypt(fam, "a key", "x"); err == nil {
		t.Error("a bad kind accepted")
	}
}

// Every token has its own random nonce: equal text gives different tokens
// (the server can't tell them apart by ciphertext), with the same handle.
func TestNoncesAreUnique(t *testing.T) {
	k := testKeys(t)
	seen := map[string]bool{}
	handles := map[string]bool{}
	for i := 0; i < 2000; i++ {
		tok := enc(t, k, fam, "k", "the same text")
		p, _ := ParseToken(tok)
		nonce := hex.EncodeToString(p.bytes[:12])
		if seen[nonce] || seen[tok] {
			t.Fatal("a nonce or token repeated")
		}
		seen[nonce], seen[tok] = true, true
		handles[p.Handle] = true
	}
	if len(handles) != 1 {
		t.Fatal("equal text, different handles in one family")
	}
}

// Handles: the same text, the same handle in one family; another family or
// text, another handle; the account's key makes them.
func TestHandles(t *testing.T) {
	k := testKeys(t)
	other := mustKeys(t, Raw{bytes.Repeat([]byte{9}, 32), bytes.Repeat([]byte{9}, 32)}, "42")
	h := k.Handle(fam, "Call the bank")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(h) || h != k.Handle(fam, "Call the bank") {
		t.Fatal(h)
	}
	for _, d := range []string{k.Handle("f0123456789abcdee", "Call the bank"), k.Handle(fam, "Call the bank."), other.Handle(fam, "Call the bank")} {
		if d == h {
			t.Fatal("handles collide")
		}
	}
	a, _ := ParseToken(enc(t, k, fam, "take_turn.entries.text", "Call the bank"))
	b, _ := ParseToken(enc(t, k, fam, "planner_draw.events.input.action", "Call the bank"))
	if a.Handle != h || b.Handle != h {
		t.Fatal("a token's handle isn't its text's")
	}
}

// A token opens only where it was made: its account, family, field and
// handle. A swapped or edited one reads Unreadable, never other text.
func TestContextBinding(t *testing.T) {
	k := testKeys(t)
	tok := enc(t, k, fam, "take_turn.entries.text", "Call the bank")
	other := enc(t, k, fam, "take_turn.entries.text", "Sell the van")
	p, _ := ParseToken(tok)
	b64 := tok[strings.LastIndex(tok, ":")+1:]
	for name, bad := range map[string]string{
		"family":     "enc3:" + p.Handle + ":f0123456789abcdee.take_turn.entries.text:" + b64,
		"kind":       "enc3:" + p.Handle + ":" + fam + ".planner_notes.notes:" + b64,
		"handle":     "enc3:" + k.Handle(fam, "Sell the van") + ":" + fam + ".take_turn.entries.text:" + b64,
		"ciphertext": other[:strings.LastIndex(other, ":")+1] + b64,
	} {
		if _, _, err := k.Decrypt(bad); err == nil {
			t.Errorf("%s changed: opened", name)
		}
		if got := k.DecryptText("x " + bad + " y"); got != "x "+Unreadable+" y" {
			t.Errorf("%s changed: %q", name, got)
		}
	}
	// Another family's token reads Unreadable where a run's text is expected;
	// any family's where none is (a list of runs).
	theirs := enc(t, k, "f0123456789abcdee", "start_sandbox.title", "Other run")
	if got := k.InFamily(fam).DecryptText(tok + " / " + theirs); got != "Call the bank / "+Unreadable {
		t.Fatal(got)
	}
	if got := k.DecryptText(tok + " / " + theirs); got != "Call the bank / Other run" {
		t.Fatal(got)
	}
	if got := k.DecryptText("a §e:" + p.Handle + "§ b"); got != "a "+Unreadable+" b" {
		t.Fatal(got)
	}
}

func TestKindOf(t *testing.T) {
	for path, want := range map[string]string{
		"antistatic_take_turn.entries[0].text":                      "take_turn.entries.text",
		"antistatic_planner_draw.events[1].input.spec.items[0].key": "planner_draw.events.input.spec.items.key",
		`antistatic_preview_market.spec.anchors key "cuf"`:          "preview_market.spec.anchors._key",
		"antistatic_submit_script.script":                           "submit_script.script",
	} {
		if got := KindOf(path); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestJSVectors(t *testing.T) {
	v := loadJS(t)
	keys := mustKeys(t, rawOf(t, v.Raw), v.UserID)
	if len(v.Tokens) < 8 || len(v.Errors) < 5 || len(v.DecryptText) < 8 || len(v.DecryptResult) < 3 {
		t.Fatal("vectors missing")
	}

	for _, tc := range v.Tokens {
		nonce, err := FromB64(tc.Nonce)
		if err != nil {
			t.Fatal(err)
		}
		fixed := *keys
		fixed.nonce = func() ([]byte, error) { return nonce, nil }
		if got := enc(t, &fixed, tc.Family, tc.Kind, tc.Text); got != tc.Token {
			t.Errorf("Encrypt(%.20q) differs from the JS token", tc.Text)
		}
		got, p, err := keys.Decrypt(tc.Token)
		if err != nil || got != tc.Text || p.Family != tc.Family || p.Kind != tc.Kind {
			t.Errorf("Decrypt JS token for %.20q: %q, %v", tc.Text, got, err)
		}
	}

	// The JS-made setup unlocks with its phrase (in any case) and gives the same keys.
	s := v.Setup
	raw, err := UnlockRaw(strings.ToUpper(strings.ReplaceAll(s.Phrase, " ", "-")), s.Setup, Iterations)
	if err != nil {
		t.Fatal(err)
	}
	want := rawOf(t, s.Raw)
	if !bytes.Equal(raw.Enc, want.Enc) || !bytes.Equal(raw.IV, want.IV) {
		t.Fatal("unlocked keys differ from JS")
	}
	if Fingerprint(s.Check) != s.Fingerprint {
		t.Fatal("fingerprint")
	}
	other := s.Setup
	other.UserID = "43"
	if _, err := UnlockRaw(s.Phrase, other, Iterations); err != ErrWrongPhrase {
		t.Fatal("unlocked under another account:", err)
	}
	old := s.Setup
	old.Version = 2
	if _, err := UnlockRaw(s.Phrase, old, Iterations); err == nil {
		t.Fatal("unlocked a format 2 setup")
	}

	for _, tc := range v.Errors {
		args, _ := ParseJSON(tc.Args)
		_, err := EncryptCall(keys, fam, tc.Tool, args)
		if tc.Error == nil {
			if err != nil {
				t.Errorf("%s: %v", tc.Tool, err)
			}
			continue
		}
		if err == nil || strings.TrimSuffix(err.Error(), ".") != strings.TrimSuffix(*tc.Error, ".") {
			t.Errorf("%s %s: got %v, want %s", tc.Tool, tc.Args, err, *tc.Error)
		}
	}
	for _, tc := range v.DecryptText {
		if got := keys.InFamily(tc.Family).DecryptText(tc.In); got != tc.Out {
			t.Errorf("DecryptText = %q, want %q", got, tc.Out)
		}
	}
	for _, tc := range v.DecryptResult {
		if got := keys.DecryptResult(tc.In); got != tc.Out {
			t.Errorf("DecryptResult = %q, want %q", got, tc.Out)
		}
	}
}

// sameJSON compares two JSON values regardless of object key order.
func sameJSON(a, b any) bool {
	var x, y any
	_ = json.Unmarshal([]byte(EncodeJSON(a, 0)), &x)
	_ = json.Unmarshal([]byte(EncodeJSON(b, 0)), &y)
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

// opened is a sealed call with each token as <kind|text>, to compare calls
// whose nonces differ.
func opened(t *testing.T, k *Keys, v any) any {
	t.Helper()
	open := func(s string) string {
		out := ""
		for _, p := range SplitTokens(s) {
			if p.Token == "" {
				out += p.Text
				continue
			}
			text, tok, err := k.Decrypt(p.Token)
			if err != nil {
				t.Fatalf("%v: %s", err, p.Token)
			}
			out += "<" + tok.Family + "." + tok.Kind + "|" + text + ">"
		}
		return out
	}
	switch x := v.(type) {
	case string:
		return open(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = opened(t, k, e)
		}
		return out
	case Object:
		out := Object{}
		for _, m := range x {
			out = append(out, Member{open(m.Key), opened(t, k, m.Value)})
		}
		return out
	}
	return v
}

func TestCalls(t *testing.T) {
	keys := testKeys(t)
	args, _ := ParseJSON([]byte(`{"run_id": 3, "turn_id": "turn-1", "entries": [{"type": "action", "text": "Call the bank", "settle": {"item": "oven", "market": "p3-1", "outcome": "yes"}}, {"type": "feed", "text": ""}]}`))
	out, err := EncryptCall(keys, fam, "antistatic_take_turn", args)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"run_id":3,"turn_id":"` + keys.OpaqueID("turn-1") + `","entries":[{"type":"action","text":"<` + fam + `.take_turn.entries.text|Call the bank>","settle":{"item":"<` + fam + `.take_turn.entries.settle.item|oven>","market":"p3-1","outcome":"yes"}},{"type":"feed","text":""}]}`
	if got := EncodeJSON(opened(t, keys, out), 0); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// Retried, the same call has new tokens and the same handles.
	again, _ := EncryptCall(keys, fam, "antistatic_take_turn", args)
	if EncodeJSON(again, 0) == EncodeJSON(out, 0) {
		t.Fatal("a retry's ciphertext repeated")
	}
	handles := regexp.MustCompile(`enc3:([0-9a-f]{32}):`)
	if strings.Join(flat(handles.FindAllStringSubmatch(EncodeJSON(again, 0), -1)), ",") != strings.Join(flat(handles.FindAllStringSubmatch(EncodeJSON(out, 0), -1)), ",") {
		t.Fatal("a retry's handles differ")
	}
	// No run family, no text: the call can't be encrypted.
	if _, err := EncryptCall(keys, "", "antistatic_take_turn", args); err != ErrNoFamily {
		t.Fatal(err)
	}
	plain, _ := ParseJSON([]byte(`{"run_id": 3}`))
	if _, err := EncryptCall(keys, "", "antistatic_get_sandbox", plain); err != nil {
		t.Fatal(err)
	}
}

func flat(ms [][]string) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

func TestUnits(t *testing.T) {
	keys := mustKeys(t, Raw{bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)}, "42")
	seal := func(s string) (string, error) { return keys.Encrypt(fam, "preview_market.spec.unit", s) }
	T := `(enc3:[0-9a-f]{32}:` + fam + `\.preview_market\.spec\.unit:[A-Za-z0-9+/]+={0,2})`
	for unit, want := range map[string]string{
		"£Xk": "^" + T + "X" + T + "$", "$": "^" + T + "X$", "%": "^X%$",
		"X% of GDP": "^X%" + T + "$", "pence per litre": "^" + T + "$", "": "^$",
	} {
		if got, err := encryptUnit(seal, unit); err != nil || !regexp.MustCompile(want).MatchString(got) {
			t.Errorf("unit %q: %q %v", unit, got, err)
		}
	}
	sealed, _ := encryptUnit(seal, "£Xk")
	rendered := ""
	for _, p := range SplitTokens(sealed) {
		if p.Token != "" {
			rendered += p.Token
		} else {
			rendered += strings.Replace(p.Text, "X", "36", 1)
		}
	}
	if got := keys.InFamily(fam).DecryptText(rendered); got != "£36k" {
		t.Fatal(got)
	}
}

type goVectors struct {
	Setup struct {
		Setup
		Phrase string `json:"phrase"`
	} `json:"setup"`
	Family string            `json:"family"`
	Raw    map[string]string `json:"raw"`
	Tokens []struct {
		Kind  string `json:"kind"`
		Text  string `json:"text"`
		Token string `json:"token"`
	} `json:"tokens"`
	Opaque map[string]string `json:"opaque"`
	Calls  []struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
		Out  json.RawMessage `json:"out"`
	} `json:"calls"`
}

// TestGoVectors writes (with -update) or checks go_vectors.json: a setup,
// tokens and the calls of testdata/calls.json made here, for the site's JS
// to open and its server to check.
func TestGoVectors(t *testing.T) {
	path := filepath.Join("testdata", "go_vectors.json")
	if *update {
		phrase, setup, raw, err := NewSetup("42", Iterations)
		if err != nil {
			t.Fatal(err)
		}
		keys := mustKeys(t, raw, "42")
		texts := []string{"", "x", "£36k ☕ naïve", "line one\nline \"two\"", strings.Repeat("c", 5000)}
		var tokens []map[string]string
		for _, s := range texts {
			tokens = append(tokens, map[string]string{"kind": "take_turn.entries.text", "text": s, "token": enc(t, keys, fam, "take_turn.entries.text", s)})
		}
		data, err := os.ReadFile(filepath.Join("testdata", "calls.json"))
		if err != nil {
			t.Fatal(err)
		}
		var inputs []struct {
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(data, &inputs); err != nil {
			t.Fatal(err)
		}
		var calls []map[string]any
		for _, in := range inputs {
			args, err := ParseJSON(in.Args)
			if err != nil {
				t.Fatal(err)
			}
			family := fam
			if f, ok := args.(Object).Get("family"); ok {
				family = f.(string)
			}
			out, err := EncryptCall(keys, family, in.Tool, args)
			if err != nil {
				t.Fatalf("%s: %v", in.Tool, err)
			}
			calls = append(calls, map[string]any{"tool": in.Tool, "args": json.RawMessage(EncodeJSON(args, 0)), "out": json.RawMessage(EncodeJSON(out, 0))})
		}
		out := map[string]any{
			"setup":  map[string]any{"user_id": setup.UserID, "version": setup.Version, "salt": setup.Salt, "wrapped": setup.Wrapped, "check": setup.Check, "phrase": phrase},
			"family": fam,
			"raw":    map[string]string{"enc": ToB64(raw.Enc), "iv": ToB64(raw.IV)},
			"tokens": tokens,
			"opaque": map[string]string{"6b0f3a52-8c1e": keys.OpaqueID("6b0f3a52-8c1e"), "turn-1": keys.OpaqueID("turn-1")},
			"calls":  calls,
		}
		data, _ = json.MarshalIndent(out, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v goVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	raw, err := UnlockRaw(v.Setup.Phrase, v.Setup.Setup, Iterations)
	if err != nil {
		t.Fatal(err)
	}
	keys := mustKeys(t, raw, v.Setup.UserID)
	for _, tc := range v.Tokens {
		if got, p, err := keys.Decrypt(tc.Token); err != nil || got != tc.Text || p.Kind != tc.Kind || p.Family != v.Family {
			t.Errorf("token for %.20q: %q %v", tc.Text, got, err)
		}
	}
	for id, want := range v.Opaque {
		if got := keys.OpaqueID(id); got != want {
			t.Errorf("OpaqueID(%q) = %q, want %q", id, got, want)
		}
	}
	if len(v.Calls) < 20 {
		t.Fatal("calls missing")
	}
	for _, c := range v.Calls {
		args, _ := ParseJSON(c.Args)
		family := v.Family
		if f, ok := args.(Object).Get("family"); ok {
			family = f.(string)
		}
		got, err := EncryptCall(keys, family, c.Tool, args)
		if err != nil {
			t.Fatalf("%s: %v", c.Tool, err)
		}
		want, _ := ParseJSON(c.Out)
		if !sameJSON(opened(t, keys, got), opened(t, keys, want)) {
			t.Errorf("%s %s:\n got %s\nwant %s", c.Tool, c.Args, EncodeJSON(opened(t, keys, got), 0), EncodeJSON(opened(t, keys, want), 0))
		}
	}
}

func TestDecryptResultAlignsTheRunGrid(t *testing.T) {
	keys, err := ImportKeys(Raw{Enc: make([]byte, 32), IV: make([]byte, 32)}, "42")
	if err != nil {
		t.Fatal(err)
	}
	a, b := enc(t, keys, fam, "k", "Offers"), enc(t, keys, fam, "k", "A much longer label for the CUF offer that runs on")
	text := "Turn 1.\n\nRun grid (x):\n```\n" + a + "\n  " + b + "\t·▒\n" + a + "\t█·\n\t'27\nclock 2026-11-01\n```"
	got := keys.DecryptResult(text)
	want := "Turn 1.\n\nRun grid (x):\n```\nOffers\n  A much longer label for the… ·▒\nOffers                         █·\n                               '27\nclock 2026-11-01\n```"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestShapeAndValueErrors(t *testing.T) {
	if s := Shape("antistatic_create_market"); !strings.HasPrefix(s, "{run_id: int, spec: {question: when|how_much, title: text") || strings.Contains(s, "code:") {
		t.Fatal(s)
	}
	keys, _ := ImportKeys(Raw{Enc: make([]byte, 32), IV: make([]byte, 32)}, "42")
	args, _ := ParseJSON([]byte(`{"run_id": 3, "spec": {"question": "how_much", "anchors": [{"period": "someday", "quantiles": {"50": 3}}]}}`))
	_, err := EncryptCall(keys, fam, "antistatic_preview_market", args)
	if err == nil || !strings.Contains(err.Error(), `anchors[0].period: "someday" isn't`) || strings.Contains(err.Error(), "not a field") {
		t.Fatal(err)
	}
	for _, p := range []string{`2026`, `"2026"`, `"2026-Q4"`, `"Q4 2026"`, `"Oct 2026"`} {
		args, _ := ParseJSON([]byte(`{"run_id": 3, "spec": {"question": "how_much", "player_choice": false, "persistence": 0.9, "periods": {"cadence": "quarter", "from": "2026-Q4", "to": "2027-Q2"}, "anchors": [{"period": ` + p + `, "quantiles": {"50": 3}}]}}`))
		if _, err := EncryptCall(keys, fam, "antistatic_create_market", args); err != nil {
			t.Fatal(p, err)
		}
	}
	for _, args := range []string{`{"run_id": 3, "code": "p3-1"}`, `{"run_id": 3, "market_code": "p3-1"}`} {
		a, _ := ParseJSON([]byte(args))
		out, err := EncryptCall(keys, fam, "antistatic_set_sandbox_goal", a)
		if err != nil || !strings.Contains(EncodeJSON(out, 0), `"code":"p3-1"`) {
			t.Fatal(args, err)
		}
	}
	item, _ := ParseJSON([]byte(`{"job_id": "pj_abcdefghijklmnop", "events": [{"tool": "add_item", "input": {"market": "p3-1", "key": "cuf", "label": "CUF", "player_choice": true, "anchors": [{"by": "2027-Q2", "p": 0.4}]}}]}`))
	if _, err := EncryptCall(keys, fam, "antistatic_planner_draw", item); err != nil {
		t.Fatal(err)
	}
}

func TestDecryptTextEscapesTheWidgetData(t *testing.T) {
	keys, _ := ImportKeys(Raw{Enc: make([]byte, 32), IV: make([]byte, 32)}, "42")
	a := enc(t, keys, fam, "k", `Say "hi" </script> "`)
	got := keys.DecryptText("Card " + a + ".\n```html\n<script>const D={\"l\":\"" + a + "\"};</script>```\nAfter " + a)
	want := "Card Say \"hi\" </script> \".\n```html\n<script>const D={\"l\":\"Say \\\"hi\\\" <\\/script> \\\"\"};</script>```\nAfter Say \"hi\" </script> \""
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
