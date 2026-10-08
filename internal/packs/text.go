package packs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HiddenMark replaces every run of hidden characters (zero-width, bidi controls, tag characters, other control characters)
// that Normalize removed. Chunk scanning counts the marks (hidden text is an injection technique) and then deletes them,
// so what is stored and served never contains a hidden character.
const HiddenMark = ''

// HashHex returns the hex SHA-256 of the parts joined with a NUL byte.
func HashHex(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EstimateTokens is the chars/4 estimate used everywhere in Drydock (an assumption, not a tokenizer).
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

func isHidden(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r == HiddenMark:
		return true
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069,
		r == 0xFEFF, r == 0x00AD, r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

// Normalize prepares text for hashing, chunking and storage: line endings become \n, tabs stay, trailing spaces go, runs of
// blank lines collapse to one blank line, and hidden characters are removed (each run leaves one HiddenMark so scanning can
// still see that something was hidden). It returns the number of runs removed.
func Normalize(s string) (string, int) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	runs := 0
	inRun := false
	for _, r := range s {
		if isHidden(r) {
			if !inRun {
				b.WriteRune(HiddenMark)
				runs++
				inRun = true
			}
			continue
		}
		inRun = false
		b.WriteRune(r)
	}
	lines := strings.Split(b.String(), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRightFunc(l, unicode.IsSpace)
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n")), runs
}

// StripHidden removes HiddenMark characters and reports how many were present.
func StripHidden(s string) (string, int) {
	n := strings.Count(s, string(HiddenMark))
	if n == 0 {
		return s, 0
	}
	return strings.ReplaceAll(s, string(HiddenMark), ""), n
}

// truncateRunes returns s cut to at most n runes, on a rune boundary.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
