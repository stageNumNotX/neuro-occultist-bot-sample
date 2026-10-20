package textutil

import (
	"strings"
	"unicode"
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
