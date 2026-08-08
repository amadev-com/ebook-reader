package tts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// indexIgnoreCase finds the first occurrence of needle in haystack starting
// at offset, using case-insensitive comparison. Returns the byte offset or -1.
func indexIgnoreCase(haystack, needle string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(haystack) || len(needle) == 0 {
		return -1
	}
	lowerHay := strings.ToLower(haystack[offset:])
	lowerNeedle := strings.ToLower(needle)
	idx := strings.Index(lowerHay, lowerNeedle)
	if idx < 0 {
		return -1
	}
	return offset + idx
}

// atWordBoundary checks that the match at [start, end) in s is bounded by
// non-word characters (or string edges). This prevents matching "Орден"
// inside "Орденский" or "ИИ" inside "молний".
//
// It works at the rune level to correctly handle UTF-8: the byte before a
// Cyrillic character is a UTF-8 continuation byte (0x80-0xBF), not a lead
// byte, so byte-level checks would incorrectly report a word boundary.
func atWordBoundary(s string, start, end int) bool {
	if start > 0 {
		// Decode the rune ending at `start` (the last rune before the match).
		prevStart := start
		// Walk back to find the start of the previous UTF-8 rune.
		for prevStart > 0 && !utf8.RuneStart(s[prevStart]) {
			prevStart--
		}
		if prevStart < start {
			r, _ := utf8.DecodeRuneInString(s[prevStart:start])
			if isWordRuneValue(r) {
				return false
			}
		}
	}
	if end < len(s) {
		// Decode the rune starting at `end` (the first rune after the match).
		r, _ := utf8.DecodeRuneInString(s[end:])
		if isWordRuneValue(r) {
			return false
		}
	}
	return true
}

// isWordRuneValue reports whether r is a letter or digit (word constituent).
func isWordRuneValue(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// replaceWordIgnoreCase replaces all case-insensitive occurrences of old with
// replacement in s, but only at word boundaries (not inside longer words).
func replaceWordIgnoreCase(s, old, replacement string) string {
	if old == "" {
		return s
	}
	var b strings.Builder
	written := 0  // byte position up to which text has been written to builder
	searchAt := 0 // byte position to search from
	for {
		idx := indexIgnoreCase(s, old, searchAt)
		if idx < 0 {
			break
		}
		end := idx + len(old)
		if !atWordBoundary(s, idx, end) {
			// Not at a word boundary — skip this match but don't lose text.
			// Advance search past the match, but keep `written` unchanged so
			// the text before this match is included in the next write.
			searchAt = end
			continue
		}
		b.WriteString(s[written:idx])
		b.WriteString(replacement)
		written = end
		searchAt = end
	}
	if written < len(s) {
		b.WriteString(s[written:])
	}
	return b.String()
}
