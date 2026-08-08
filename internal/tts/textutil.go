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
