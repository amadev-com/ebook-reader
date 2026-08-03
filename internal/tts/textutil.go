package tts

import "strings"

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
// inside "Орденский".
func atWordBoundary(s string, start, end int) bool {
	if start > 0 {
		prev := s[start-1]
		if isWordRune(prev) {
			return false
		}
	}
	if end < len(s) {
		next := s[end]
		if isWordRune(next) {
			return false
		}
	}
	return true
}

// isWordRune reports whether r is a letter or digit (word constituent).
func isWordRune(r byte) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r >= 0xC0 // Cyrillic and other non-ASCII letters (UTF-8 continuation handled by byte range)
}
