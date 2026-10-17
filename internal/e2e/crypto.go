// Package e2e is encrypted wargames' crypto, format 3, byte-for-byte the
// site's (assets/js/e2e_crypto.js in antistatic_exchange; the vectors in
// testdata are checked on both sides).
//
// The key setup is format 2's, unchanged:
//
//	phrase  6 EFF words, compared without hyphens, joined by spaces
//	master  32 random bytes, made once at setup
//	kek     HMAC-SHA256(PBKDF2-SHA256(phrase, salt 16 bytes, 600000), "antistatic/2/wrap")
//	wrapped "wrap2:" + base64(iv 12 || AES-256-GCM(kek, master; "antistatic/2/<user id>")), 60 bytes
//	raw     Enc, IV = HMAC-SHA256(master, "antistatic/2/enc" | "antistatic/2/iv"): what's saved
//	check   "enc2:" + base64(iv 12 || AES-256-GCM(raw.Enc, padded "antistatic"; "antistatic/2/<user id>"))
//
// Content, format 3:
//
//	keys    text = HMAC(raw.Enc, "antistatic/3/text"), mac = HMAC(raw.IV, "antistatic/3/mac")
//	family  "f" + 16 random hex, made when a run starts; its forks share it
//	ctx     family "." kind (the field's schema path without indices or "antistatic_")
//	handle  32 hex of HMAC(mac, "antistatic/3/handle" || 0 || family || 0 || text)
//	token   "enc3:" handle ":" ctx ":" base64(nonce 12 || AES-256-GCM(text, padded; aad)),
//	        nonce random, padded = uint32 length || utf-8 text || zeros, to a bucket
//	aad     "antistatic/3/<user id>/" ctx "/" handle
package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	Format      = 3
	Iterations  = 600000
	CheckText   = "antistatic"
	MinBucket   = 32
	step        = 65536
	TokenPrefix = "enc3:"
	CheckPrefix = "enc2:"
	WrapPrefix  = "wrap2:"
)

// ErrWrongPhrase is a phrase that doesn't open the account's key.
var ErrWrongPhrase = errors.New("wrong recovery phrase")

// FamilyRe is a run family id.
var FamilyRe = regexp.MustCompile(`^f[0-9a-f]{16}$`)

// A token's handle and context, before its base64.
var headRe = regexp.MustCompile(`^([0-9a-f]{32}):(f[0-9a-f]{16})\.([a-z0-9_]+(?:\.[a-z0-9_]+)*):`)

var indexRe = regexp.MustCompile(`\[\d+\]`)

// KindOf is a field's kind from its schema path: no indices, no "antistatic_".
var keyRe = regexp.MustCompile(`(?s) key( .*)?$`)

func KindOf(path string) string {
	return strings.TrimPrefix(keyRe.ReplaceAllString(indexRe.ReplaceAllString(path, ""), "._key"), "antistatic_")
}

// NewFamily is a new run family id: "f" + 16 random hex.
func NewFamily() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "f" + hex.EncodeToString(b), nil
}

// Bucket is the padded size for n bytes: powers of two from 32 bytes, then 64 KiB steps.
func Bucket(n int) int {
	if n <= MinBucket {
		return MinBucket
	}
	if n <= step {
		b := MinBucket
		for b < n {
			b *= 2
		}
		return b
	}
	return (n + step - 1) / step * step
}

func isBucket(n int) bool { return n >= MinBucket && Bucket(n) == n }

// ToB64 is standard base64 with padding.
func ToB64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// FromB64 accepts canonical base64 only (what ToB64 makes).
func FromB64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || ToB64(b) != s {
		return nil, errors.New("not canonical base64")
	}
	return b, nil
}

func hmacSHA256(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// setupAad binds the wrapped key and the check to the account.
func setupAad(userID string) []byte { return []byte("antistatic/2/" + userID) }

func aad(userID, ctx, handle string) []byte {
	return []byte(fmt.Sprintf("antistatic/%d/%s/%s/%s", Format, userID, ctx, handle))
}

// DeriveKek is the key-encryption key for a parsed phrase.
func DeriveKek(phrase string, salt []byte, iterations int) ([]byte, error) {
	km, err := pbkdf2.Key(sha256.New, phrase, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	return hmacSHA256(km, []byte("antistatic/2/wrap")), nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// WrapMaster seals the master key under the kek, for one account.
func WrapMaster(kek, master []byte, userID string) (string, error) {
	g, err := gcm(kek)
	if err != nil {
		return "", err
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	return WrapPrefix + ToB64(g.Seal(iv, iv, master, setupAad(userID))), nil
}

// UnwrapMaster opens a wrapped master key; it fails on a wrong key or account.
func UnwrapMaster(kek []byte, wrapped, userID string) ([]byte, error) {
	if !strings.HasPrefix(wrapped, WrapPrefix) {
		return nil, errors.New("not a wrap2 key")
	}
	b, err := FromB64(wrapped[len(WrapPrefix):])
	if err != nil || len(b) != 60 {
		return nil, errors.New("not a wrap2 key")
	}
	g, err := gcm(kek)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, b[:12], b[12:], setupAad(userID))
}

// Raw is an account's saved keys, 32 bytes each.
type Raw struct {
	Enc []byte
	IV  []byte
}

// ContentKeys derives the raw keys from a master key.
func ContentKeys(master []byte) Raw {
	return Raw{
		Enc: hmacSHA256(master, []byte("antistatic/2/enc")),
		IV:  hmacSHA256(master, []byte("antistatic/2/iv")),
	}
}

// Keys are raw keys bound to one account.
type Keys struct {
	UserID string
	check  cipher.AEAD
	text   cipher.AEAD
	mac    []byte
	// family is the run family results are read in ("": any).
	family string
	// nonce makes a token's nonce; tests fix it.
	nonce func() ([]byte, error)
}

func randomNonce() ([]byte, error) {
	b := make([]byte, 12)
	_, err := rand.Read(b)
	return b, err
}

// ImportKeys binds raw keys to an account.
func ImportKeys(raw Raw, userID string) (*Keys, error) {
	if userID == "" {
		return nil, errors.New("keys need the account's user id")
	}
	if len(raw.Enc) != 32 || len(raw.IV) != 32 {
		return nil, errors.New("keys are 32 bytes each")
	}
	check, err := gcm(raw.Enc)
	if err != nil {
		return nil, err
	}
	text, err := gcm(hmacSHA256(raw.Enc, []byte(fmt.Sprintf("antistatic/%d/text", Format))))
	if err != nil {
		return nil, err
	}
	mac := hmacSHA256(raw.IV, []byte(fmt.Sprintf("antistatic/%d/mac", Format)))
	return &Keys{UserID: userID, check: check, text: text, mac: mac, nonce: randomNonce}, nil
}

func pad(text string) []byte {
	data := []byte(text)
	padded := make([]byte, Bucket(4+len(data)))
	binary.BigEndian.PutUint32(padded, uint32(len(data)))
	copy(padded[4:], data)
	return padded
}

func unpad(pt []byte) (string, error) {
	if len(pt) < 4 {
		return "", errors.New("bad padding")
	}
	n := int(binary.BigEndian.Uint32(pt))
	if n > len(pt)-4 {
		return "", errors.New("bad padding")
	}
	for _, x := range pt[4+n:] {
		if x != 0 {
			return "", errors.New("bad padding")
		}
	}
	text := pt[4 : 4+n]
	if !utf8.Valid(text) {
		return "", errors.New("not utf-8")
	}
	return string(text), nil
}

// Handle is the handle of text in a family: the same text in one family
// has the same handle (the server matches text by it), in another not.
func (k *Keys) Handle(family, text string) string {
	mac := hmacSHA256(k.mac, []byte(fmt.Sprintf("antistatic/%d/handle", Format)), []byte{0}, []byte(family), []byte{0}, []byte(text))
	return hex.EncodeToString(mac[:16])
}

// Encrypt makes a token of text in a family, for the field kind, with a random nonce.
func (k *Keys) Encrypt(family, kind, text string) (string, error) {
	ctx := family + "." + kind
	if !FamilyRe.MatchString(family) || !headRe.MatchString(strings.Repeat("0", 32)+":"+ctx+":") {
		return "", fmt.Errorf("not a context: %s", ctx)
	}
	nonce, err := k.nonce()
	if err != nil {
		return "", err
	}
	handle := k.Handle(family, text)
	return TokenPrefix + handle + ":" + ctx + ":" + ToB64(k.text.Seal(append([]byte(nil), nonce...), nonce, pad(text), aad(k.UserID, ctx, handle))), nil
}

// Token is a well-formed token's parts.
type Token struct {
	Handle string
	Ctx    string
	Family string
	Kind   string
	bytes  []byte
}

// ParseToken checks a token's form and splits it.
func ParseToken(token string) (Token, error) {
	bad := errors.New("not an enc3 token")
	if !strings.HasPrefix(token, TokenPrefix) {
		return Token{}, bad
	}
	rest := token[len(TokenPrefix):]
	m := headRe.FindStringSubmatch(rest)
	if m == nil {
		return Token{}, bad
	}
	b, err := FromB64(rest[len(m[0]):])
	if err != nil || !isBucket(len(b)-28) {
		return Token{}, bad
	}
	return Token{Handle: m[1], Ctx: m[2] + "." + m[3], Family: m[2], Kind: m[3], bytes: b}, nil
}

// IsToken reports whether s is a well-formed enc3 token.
func IsToken(s string) bool {
	_, err := ParseToken(s)
	return err == nil
}

// PaddedSize is the padded size of a token's text.
func PaddedSize(token string) (int, error) {
	t, err := ParseToken(token)
	if err != nil {
		return 0, err
	}
	return len(t.bytes) - 28, nil
}

// Decrypt opens a token. It fails when the token isn't this account's, its
// context or handle were changed, or the handle isn't its text's.
func (k *Keys) Decrypt(token string) (string, Token, error) {
	t, err := ParseToken(token)
	if err != nil {
		return "", t, err
	}
	pt, err := k.text.Open(nil, t.bytes[:12], t.bytes[12:], aad(k.UserID, t.Ctx, t.Handle))
	if err != nil {
		return "", t, errors.New("not this key's token")
	}
	text, err := unpad(pt)
	if err != nil {
		return "", t, err
	}
	if !hmac.Equal([]byte(k.Handle(t.Family, text)), []byte(t.Handle)) {
		return "", t, errors.New("not this text's handle")
	}
	return text, t, nil
}

// sealCheck makes a new setup's check token.
func (k *Keys) sealCheck() (string, error) {
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	return CheckPrefix + ToB64(k.check.Seal(append([]byte(nil), iv...), iv, pad(CheckText), setupAad(k.UserID))), nil
}

// Verify reports whether the keys open the account's check token.
func (k *Keys) Verify(check string) bool {
	if !strings.HasPrefix(check, CheckPrefix) {
		return false
	}
	b, err := FromB64(check[len(CheckPrefix):])
	if err != nil || len(b) != 28+MinBucket {
		return false
	}
	pt, err := k.check.Open(nil, b[:12], b[12:], setupAad(k.UserID))
	if err != nil {
		return false
	}
	text, err := unpad(pt)
	return err == nil && text == CheckText
}

// OpaqueID makes a client-chosen id (a turn_id) opaque: t + 32 hex of an
// HMAC under the key, the same for the same id. An id already in that form
// (the server's echo of one) is kept.
func (k *Keys) OpaqueID(id string) string {
	if opaqueRe.MatchString(id) {
		return id
	}
	mac := hmacSHA256(k.mac, []byte(fmt.Sprintf("antistatic/%d/opaque", Format)), []byte{0}, []byte(id))
	return "t" + hex.EncodeToString(mac[:16])
}

var opaqueRe = regexp.MustCompile(`^t[0-9a-f]{32}$`)

// Fingerprint is 16 hex of sha256 over the check token a key opens.
func Fingerprint(check string) string {
	sum := sha256.Sum256([]byte(check))
	return hex.EncodeToString(sum[:8])
}

// Setup is an account's key setup as the server keeps it.
type Setup struct {
	UserID  string `json:"user_id"`
	Version int    `json:"version"`
	Salt    string `json:"salt"`
	Wrapped string `json:"wrapped"`
	Check   string `json:"check"`
}

// NewSetup makes a phrase, the setup for the server and the raw keys.
func NewSetup(userID string, iterations int) (phrase string, setup Setup, raw Raw, err error) {
	phrase, err = GeneratePhrase()
	if err != nil {
		return
	}
	parsed, err := ParsePhrase(phrase)
	if err != nil {
		return
	}
	salt := make([]byte, 16)
	master := make([]byte, 32)
	if _, err = rand.Read(salt); err != nil {
		return
	}
	if _, err = rand.Read(master); err != nil {
		return
	}
	kek, err := DeriveKek(parsed, salt, iterations)
	if err != nil {
		return
	}
	wrapped, err := WrapMaster(kek, master, userID)
	if err != nil {
		return
	}
	raw = ContentKeys(master)
	keys, err := ImportKeys(raw, userID)
	if err != nil {
		return
	}
	check, err := keys.sealCheck()
	if err != nil {
		return
	}
	setup = Setup{UserID: userID, Version: Format, Salt: ToB64(salt), Wrapped: wrapped, Check: check}
	return
}

// UnlockRaw gives the raw keys for a typed phrase and the account's setup.
func UnlockRaw(input string, setup Setup, iterations int) (Raw, error) {
	if setup.Version != Format {
		return Raw{}, fmt.Errorf("unknown encryption format %d", setup.Version)
	}
	parsed, err := ParsePhrase(input)
	if err != nil {
		return Raw{}, err
	}
	salt, err := FromB64(setup.Salt)
	if err != nil {
		return Raw{}, ErrWrongPhrase
	}
	kek, err := DeriveKek(parsed, salt, iterations)
	if err != nil {
		return Raw{}, err
	}
	master, err := UnwrapMaster(kek, setup.Wrapped, setup.UserID)
	if err != nil {
		return Raw{}, ErrWrongPhrase
	}
	raw := ContentKeys(master)
	keys, err := ImportKeys(raw, setup.UserID)
	if err != nil || !keys.Verify(setup.Check) {
		return Raw{}, ErrWrongPhrase
	}
	return raw, nil
}

// Piece is text or a token in SplitTokens' output.
type Piece struct {
	Text  string
	Token string
}

// SplitTokens splits text into text and token pieces. A token is "enc3:",
// its handle and context, and the longest run of base64 after them that
// makes a well-formed token, so a token may sit right against other text.
// A malformed one stays text.
func SplitTokens(text string) []Piece {
	var out []Piece
	rest := text
	for {
		at := strings.Index(rest, TokenPrefix)
		if at < 0 {
			break
		}
		after := rest[at+len(TokenPrefix):]
		head := headRe.FindString(after)
		run := after[len(head) : len(head)+b64RunLen(after[len(head):])]
		length := 0
		if head != "" {
			var lens []int
			for b := MinBucket; ; {
				l := 4 * ((28 + b + 2) / 3)
				if l > len(run) {
					for i := len(lens) - 1; i >= 0; i-- {
						if IsToken(TokenPrefix + head + run[:lens[i]]) {
							length = lens[i]
							break
						}
					}
					break
				}
				lens = append(lens, l)
				if b < step {
					b *= 2
				} else {
					b += step
				}
			}
		}
		end := at + len(TokenPrefix) + len(head) + length
		if length == 0 {
			end = at + len(TokenPrefix) + len(head) + len(run)
		}
		if at > 0 {
			out = append(out, Piece{Text: rest[:at]})
		}
		if length > 0 {
			out = append(out, Piece{Token: rest[at:end]})
		} else {
			out = append(out, Piece{Text: rest[at:end]})
		}
		rest = rest[end:]
	}
	if rest != "" {
		out = append(out, Piece{Text: rest})
	}
	return out
}

// b64RunLen is the length of /^[A-Za-z0-9+/]*={0,2}/ at the start of s.
func b64RunLen(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
			i++
			continue
		}
		break
	}
	for j := 0; j < 2 && i < len(s) && s[i] == '='; j++ {
		i++
	}
	return i
}
