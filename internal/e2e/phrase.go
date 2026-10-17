package e2e

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// The EFF large word list (7776 words), words only, from
// https://www.eff.org/files/2016/07/18/eff_large_wordlist.txt. Four words
// have hyphens: drop-down, felt-tip, t-shirt, yo-yo.
//
//go:embed eff_large_wordlist.txt
var wordlist string

// WordsSHA256 is the sha256 of the words joined by newlines, with a trailing newline.
const WordsSHA256 = "6d557f0693958fb5e650b68b5bee585eb82cf4da32965505c789e924743bc522"

// PhraseWords is how many words a phrase has; ConfirmWords the ones set-up
// asks to retype (1-based).
const PhraseWords = 6

var ConfirmWords = []int{2, 5}

// Words is the list.
var Words []string

var (
	index      = map[string]bool{}
	hyphenated = map[string]bool{}
	maxParts   = 1
)

func init() {
	sum := sha256.Sum256([]byte(wordlist))
	if hex.EncodeToString(sum[:]) != WordsSHA256 {
		panic("e2e: word list sha256 mismatch")
	}
	Words = strings.Split(strings.TrimSuffix(wordlist, "\n"), "\n")
	for _, w := range Words {
		index[canon(w)] = true
		if strings.Contains(w, "-") {
			hyphenated[w] = true
		}
		if n := len(strings.Split(w, "-")); n > maxParts {
			maxParts = n
		}
	}
}

// Words are compared without hyphens: the list has both "yo-yo" and "yoyo".
func canon(w string) string { return strings.ReplaceAll(w, "-", "") }

// RandomIndex is a uniform index below n: rejection sampling over 32-bit draws.
func RandomIndex(n int, rng io.Reader) (int, error) {
	limit := uint64(0x100000000) / uint64(n) * uint64(n)
	var buf [4]byte
	for {
		if _, err := io.ReadFull(rng, buf[:]); err != nil {
			return 0, err
		}
		v := uint64(binary.LittleEndian.Uint32(buf[:]))
		if v < limit {
			return int(v % uint64(n)), nil
		}
	}
}

// GeneratePhrase draws 6 words uniformly from the list, joined by spaces.
func GeneratePhrase() (string, error) {
	words := make([]string, PhraseWords)
	for i := range words {
		j, err := RandomIndex(len(Words), rand.Reader)
		if err != nil {
			return "", err
		}
		words[i] = Words[j]
	}
	return strings.Join(words, " "), nil
}

func distance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for i := range prev {
		prev[i] = i
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// SuggestWord is the closest list word by edit distance (ties: the one
// sharing the longest start), when close enough to be a typo; else "".
func SuggestWord(word string) string {
	w := []rune(strings.ToLower(word))
	shared := func(x string) int {
		r := []rune(x)
		i := 0
		for i < len(r) && i < len(w) && r[i] == w[i] {
			i++
		}
		return i
	}
	best, bestD := "", -1
	for _, x := range Words {
		d := distance(w, []rune(x))
		if bestD < 0 || d < bestD || (d == bestD && shared(x) > shared(best)) {
			best, bestD = x, d
		}
	}
	if bestD <= max(1, len(w)/3) {
		return best
	}
	return ""
}

var nonLetters = regexp.MustCompile(`[^a-z]+`)

// wordAt is the list word (without hyphens) that atoms[i:i+n] spell, or "".
func wordAt(atoms []string, i, n int) string {
	if n == 1 {
		if index[atoms[i]] {
			return atoms[i]
		}
		return ""
	}
	parts := atoms[i : i+n]
	if hyphenated[strings.Join(parts, "-")] {
		return strings.Join(parts, "")
	}
	return ""
}

// ParsePhrase reads a typed phrase: any case; dashes, commas, dots or
// whitespace between words; hyphenated words with or without the hyphen.
// It returns the words without hyphens joined by single spaces (what the
// key is derived from).
func ParsePhrase(input string) (string, error) {
	var atoms []string
	for _, a := range nonLetters.Split(strings.ToLower(input), -1) {
		if a != "" {
			atoms = append(atoms, a)
		}
	}
	// best[i]: the fewest words that spell atoms[i:] (nil: none).
	best := make([][]string, len(atoms)+1)
	best[len(atoms)] = []string{}
	for i := len(atoms) - 1; i >= 0; i-- {
		for n := 1; n <= maxParts && i+n <= len(atoms); n++ {
			w := wordAt(atoms, i, n)
			rest := best[i+n]
			if w != "" && rest != nil && (best[i] == nil || len(rest)+1 < len(best[i])) {
				best[i] = append([]string{w}, rest...)
			}
		}
	}
	if best[0] == nil {
		covered := func(j int) bool {
			return wordAt(atoms, j, 1) != "" ||
				(j > 0 && wordAt(atoms, j-1, 2) != "") ||
				(j+2 <= len(atoms) && wordAt(atoms, j, 2) != "")
		}
		bad := atoms[0]
		for j := range atoms {
			if !covered(j) {
				bad = atoms[j]
				break
			}
		}
		if s := SuggestWord(bad); s != "" {
			return "", fmt.Errorf("not a word in the list: %s (%s?)", bad, s)
		}
		return "", fmt.Errorf("not a word in the list: %s", bad)
	}
	if len(best[0]) != PhraseWords {
		return "", fmt.Errorf("needs %d words, not %d", PhraseWords, len(best[0]))
	}
	return strings.Join(best[0], " "), nil
}
