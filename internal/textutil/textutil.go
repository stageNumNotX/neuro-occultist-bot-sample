package textutil

import (
	"strings"
	"unicode"
	"unicode/utf16"
)

// SanitizeUntrusted strips control characters, delimiter breakout markers, and
// clamps length so model/image prompts can treat the value as data, not instructions.
func SanitizeUntrusted(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "<untrusted-data>", "")
	s = strings.ReplaceAll(s, "</untrusted-data>", "")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	return Truncate(s, maxRunes)
}

// Truncate shortens s to at most maxRunes runes.
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// UTF16Len returns the number of UTF-16 code units in s (Telegram’s
// character count for captions and messages).
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16RuneLen(r)
	}
	return n
}

// TruncateUTF16 shortens s to at most maxUnits UTF-16 code units.
// Telegram photo captions are capped at 1024.
func TruncateUTF16(s string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	if UTF16Len(s) <= maxUnits {
		return s
	}
	if maxUnits == 1 {
		return "…"
	}
	budget := maxUnits - 1 // room for the ellipsis
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := utf16RuneLen(r)
		if used+w > budget {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteRune('…')
	return b.String()
}

func utf16RuneLen(r rune) int {
	n := utf16.RuneLen(r)
	if n < 1 {
		return 1
	}
	return n
}
