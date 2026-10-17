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
// test/fixtures/e2e/js_vectors.json): Go must open and remake it exactly.
// go_vectors.json is made here (go test ./internal/e2e -update) and copied
// to the site's test/fixtures/e2e, where the JS opens it.
var update = flag.Bool("update", false, "rewrite testdata/go_vectors.json")

type jsVectors struct {
	UserID string            `json:"user_id"`
	Raw    map[string]string `json:"raw"`
	Setup  struct {
		Setup
		Phrase      string            `json:"phrase"`
		Raw         map[string]string `json:"raw"`
		Fingerprint string            `json:"fingerprint"`
	} `json:"setup"`
	Tokens []struct{ Text, Token string } `json:"tokens"`
	Opaque []struct{ ID, Opaque string }  `json:"opaque"`
	Calls  []struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
		Out  json.RawMessage `json:"out"`
	} `json:"calls"`
	Errors []struct {
		Tool  string          `json:"tool"`
		Args  json.RawMessage `json:"args"`
		Error *string         `json:"error"`
	} `json:"errors"`
	DecryptText   []struct{ In, Out string } `json:"decrypt_text"`
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
	k42 := mustKeys(t, Raw{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}, "42")
	k43 := mustKeys(t, Raw{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}, "43")
	tok := k42.Encrypt("x")
	b, _ := TokenBytes(tok)
	if len(b) != 28+32 {
		t.Fatal(len(b))
	}
	if got, err := k42.Decrypt(tok); err != nil || got != "x" {
		t.Fatal(got, err)
	}
	if tok != k42.Encrypt("x") || tok == k43.Encrypt("x") {
		t.Fatal("deterministic per account")
	}
	if _, err := k43.Decrypt(tok); err == nil {
		t.Fatal("opened under another account")
	}
	for n, want := range map[int]int{28: 32, 29: 64, 70000: 131072} {
		b, _ := TokenBytes(k42.Encrypt(strings.Repeat("a", n)))
		if len(b)-28 != want {
			t.Errorf("%d bytes padded to %d, want %d", n, len(b)-28, want)
		}
	}
	if _, err := TokenBytes("enc2:" + ToB64(make([]byte, 28+33))); err == nil {
		t.Error("off-bucket size accepted")
	}
	t64 := k42.Encrypt(strings.Repeat("a", 40))
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := t64[len(t64)-2]
	loose := t64[:len(t64)-2] + string(alphabet[strings.IndexByte(alphabet, last)^1]) + "="
	if _, err := TokenBytes(loose); err == nil {
		t.Error("non-canonical base64 accepted")
	}
}

func TestJSVectors(t *testing.T) {
	v := loadJS(t)
	keys := mustKeys(t, rawOf(t, v.Raw), v.UserID)
	if len(v.Tokens) < 5 || len(v.Calls) < 10 || len(v.Errors) < 5 || len(v.DecryptText) < 5 || len(v.DecryptResult) < 3 {
		t.Fatal("vectors missing")
	}

	for _, tc := range v.Tokens {
		if got := keys.Encrypt(tc.Text); got != tc.Token {
			t.Errorf("Encrypt(%.20q) differs from the JS token", tc.Text)
		}
		if got, err := keys.Decrypt(tc.Token); err != nil || got != tc.Text {
			t.Errorf("Decrypt JS token for %.20q: %q, %v", tc.Text, got, err)
		}
	}
	for _, tc := range v.Opaque {
		if got := keys.OpaqueID(tc.ID); got != tc.Opaque {
			t.Errorf("OpaqueID(%q) = %q, want %q", tc.ID, got, tc.Opaque)
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

	for _, tc := range v.Calls {
		args, err := ParseJSON(tc.Args)
		if err != nil {
			t.Fatal(err)
		}
		got, err := EncryptCall(keys, tc.Tool, args)
		if err != nil {
			t.Errorf("%s: %v", tc.Tool, err)
			continue
		}
		wantOut, _ := ParseJSON(tc.Out)
		if !sameJSON(got, wantOut) {
			t.Errorf("%s %s:\n got %s\nwant %s", tc.Tool, tc.Args, EncodeJSON(got, 0), EncodeJSON(wantOut, 0))
		}
	}
	for _, tc := range v.Errors {
		args, _ := ParseJSON(tc.Args)
		_, err := EncryptCall(keys, tc.Tool, args)
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
		if got := keys.DecryptText(tc.In); got != tc.Out {
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

func TestUnits(t *testing.T) {
	keys := mustKeys(t, Raw{bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)}, "42")
	T := `(enc2:[A-Za-z0-9+/]+={0,2})`
	for unit, want := range map[string]string{
		"£Xk": "^" + T + "X" + T + "$", "$": "^" + T + "X$", "%": "^X%$",
		"X% of GDP": "^X%" + T + "$", "pence per litre": "^" + T + "$", "": "^$",
	} {
		if got := encryptUnit(keys, unit); !regexp.MustCompile(want).MatchString(got) {
			t.Errorf("unit %q: %q", unit, got)
		}
	}
	rendered := ""
	for _, p := range SplitTokens(encryptUnit(keys, "£Xk")) {
		if p.Token != "" {
			rendered += p.Token
		} else {
			rendered += strings.Replace(p.Text, "X", "36", 1)
		}
	}
	if got := keys.DecryptText(rendered); got != "£36k" {
		t.Fatal(got)
	}
}

// TestGoVectors writes (with -update) or checks go_vectors.json: a setup and
// tokens made here, for the site's JS to open.
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
			tokens = append(tokens, map[string]string{"text": s, "token": keys.Encrypt(s)})
		}
		out := map[string]any{
			"setup":  map[string]any{"user_id": setup.UserID, "version": setup.Version, "salt": setup.Salt, "wrapped": setup.Wrapped, "check": setup.Check, "phrase": phrase},
			"raw":    map[string]string{"enc": ToB64(raw.Enc), "iv": ToB64(raw.IV)},
			"tokens": tokens,
			"opaque": map[string]string{"6b0f3a52-8c1e": keys.OpaqueID("6b0f3a52-8c1e")},
		}
		data, _ := json.MarshalIndent(out, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Setup struct {
			Setup
			Phrase string `json:"phrase"`
		} `json:"setup"`
		Raw    map[string]string `json:"raw"`
		Tokens []struct{ Text, Token string }
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	raw, err := UnlockRaw(v.Setup.Phrase, v.Setup.Setup, Iterations)
	if err != nil {
		t.Fatal(err)
	}
	keys := mustKeys(t, raw, v.Setup.UserID)
	for _, tc := range v.Tokens {
		if got, err := keys.Decrypt(tc.Token); err != nil || got != tc.Text {
			t.Errorf("token for %.20q: %q %v", tc.Text, got, err)
		}
	}
}
