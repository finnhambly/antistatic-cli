// Package e2e is encrypted wargames' crypto, format 2, byte-for-byte the
// site's (assets/js/e2e_crypto.js in antistatic_exchange; the vectors in
// testdata are checked on both sides).
//
//	phrase  6 EFF words, compared without hyphens, joined by spaces
//	master  32 random bytes, made once at setup
//	kek     HMAC-SHA256(PBKDF2-SHA256(phrase, salt 16 bytes, 600000), "antistatic/2/wrap")
//	wrapped "wrap2:" + base64(iv 12 || AES-256-GCM(kek, master; aad)), 60 bytes
//	enc,ivk HMAC-SHA256(master, "antistatic/2/enc" | "antistatic/2/iv")
//	token   "enc2:" + base64(iv 12 || AES-256-GCM(enc, padded; aad)), with
//	        padded = uint32 length || utf-8 text || zeros, to a bucket,
//	        iv = HMAC-SHA256(ivk, aad || 0 || text)[0..12)
//	aad     "antistatic/2/<user id>"
//	check   the token of "antistatic": the server keeps salt, wrapped and check.
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
	Format      = 2
	Iterations  = 600000
	CheckText   = "antistatic"
	MinBucket   = 32
	step        = 65536
	TokenPrefix = "enc2:"
	WrapPrefix  = "wrap2:"
)

// ErrWrongPhrase is a phrase that doesn't open the account's key.
var ErrWrongPhrase = errors.New("wrong recovery phrase")

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

func aad(userID string) []byte { return []byte(fmt.Sprintf("antistatic/%d/%s", Format, userID)) }

// DeriveKek is the key-encryption key for a parsed phrase.
func DeriveKek(phrase string, salt []byte, iterations int) ([]byte, error) {
	km, err := pbkdf2.Key(sha256.New, phrase, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	return hmacSHA256(km, []byte(fmt.Sprintf("antistatic/%d/wrap", Format))), nil
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
	return WrapPrefix + ToB64(g.Seal(iv, iv, master, aad(userID))), nil
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
	return g.Open(nil, b[:12], b[12:], aad(userID))
}

// Raw is an account's content keys, 32 bytes each.
type Raw struct {
	Enc []byte
	IV  []byte
}

// ContentKeys derives the content keys from a master key.
func ContentKeys(master []byte) Raw {
	return Raw{
		Enc: hmacSHA256(master, []byte(fmt.Sprintf("antistatic/%d/enc", Format))),
		IV:  hmacSHA256(master, []byte(fmt.Sprintf("antistatic/%d/iv", Format))),
	}
}

// Keys are content keys bound to one account.
type Keys struct {
	UserID string
	aead   cipher.AEAD
	iv     []byte
}

// ImportKeys binds raw content keys to an account.
func ImportKeys(raw Raw, userID string) (*Keys, error) {
	if userID == "" {
		return nil, errors.New("keys need the account's user id")
	}
	if len(raw.Enc) != 32 || len(raw.IV) != 32 {
		return nil, errors.New("content keys are 32 bytes each")
	}
	g, err := gcm(raw.Enc)
	if err != nil {
		return nil, err
	}
	return &Keys{UserID: userID, aead: g, iv: append([]byte(nil), raw.IV...)}, nil
}

// Encrypt makes the token of text: the same text, the same token.
func (k *Keys) Encrypt(text string) string {
	data := []byte(text)
	ad := aad(k.UserID)
	padded := make([]byte, Bucket(4+len(data)))
	binary.BigEndian.PutUint32(padded, uint32(len(data)))
	copy(padded[4:], data)
	iv := hmacSHA256(k.iv, ad, []byte{0}, data)[:12]
	return TokenPrefix + ToB64(k.aead.Seal(append([]byte(nil), iv...), iv, padded, ad))
}

// TokenBytes is a well-formed token's bytes.
func TokenBytes(token string) ([]byte, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, errors.New("not an enc2 token")
	}
	b, err := FromB64(token[len(TokenPrefix):])
	if err != nil {
		return nil, err
	}
	if !isBucket(len(b) - 28) {
		return nil, errors.New("not an enc2 token")
	}
	return b, nil
}

// IsToken reports whether s is a well-formed enc2 token.
func IsToken(s string) bool {
	_, err := TokenBytes(s)
	return err == nil
}

// Decrypt opens a token; it fails when the token isn't this key's (or account's).
func (k *Keys) Decrypt(token string) (string, error) {
	b, err := TokenBytes(token)
	if err != nil {
		return "", err
	}
	pt, err := k.aead.Open(nil, b[:12], b[12:], aad(k.UserID))
	if err != nil {
		return "", errors.New("not this key's token")
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

// Verify reports whether the keys open the account's check token.
func (k *Keys) Verify(check string) bool {
	t, err := k.Decrypt(check)
	return err == nil && t == CheckText
}

// OpaqueID makes a client-chosen id (a turn_id) opaque: t + 32 hex of an
// HMAC under the key, the same for the same id. An id already in that form
// (the server's echo of one) is kept.
func (k *Keys) OpaqueID(id string) string {
	if opaqueRe.MatchString(id) {
		return id
	}
	mac := hmacSHA256(k.iv, []byte(fmt.Sprintf("antistatic/%d/opaque\x00%s", Format, id)))
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

// NewSetup makes a phrase, the setup for the server and the content keys.
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
	setup = Setup{UserID: userID, Version: Format, Salt: ToB64(salt), Wrapped: wrapped, Check: keys.Encrypt(CheckText)}
	return
}

// UnlockRaw gives the content keys for a typed phrase and the account's setup.
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

// SplitTokens splits text into text and token pieces. A token is "enc2:"
// and the longest run of base64 after it that is a well-formed token, so a
// token may sit right against other text. A malformed one stays text.
func SplitTokens(text string) []Piece {
	var out []Piece
	rest := text
	for {
		at := strings.Index(rest, TokenPrefix)
		if at < 0 {
			break
		}
		after := rest[at+len(TokenPrefix):]
		run := after[:b64RunLen(after)]
		length := 0
		var lens []int
		for b := MinBucket; ; {
			l := 4 * ((28 + b + 2) / 3)
			if l > len(run) {
				for i := len(lens) - 1; i >= 0; i-- {
					if IsToken(TokenPrefix + run[:lens[i]]) {
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
		end := at + len(TokenPrefix) + length
		if length == 0 {
			end = at + len(TokenPrefix) + len(run)
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
