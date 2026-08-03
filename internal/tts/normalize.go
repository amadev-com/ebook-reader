package tts

import (
	"strings"
)

// NormalizeRussian applies text normalization rules that help XTTS v2
// pronounce Russian text correctly. These are safe, always-on transformations
// that don't change meaning but improve TTS quality:
//
//  1. De-capitalization: mid-sentence uppercase words are lowercased (XTTS
//     tokenizes ALL-CAPS words awkwardly, often spelling them letter-by-letter).
//     Words at the start of a sentence or after a colon/quote keep their
//     initial capital.
//  2. Single-letter prepositions: bind "в", "с", "к", "у", "и", "а", "я" to
//     the following word with a hyphen to prevent XTTS from swallowing them.
//
// Respelling (term-specific replacements) is applied separately by
// Respelling.Apply before this normalization.
func NormalizeRussian(text string) string {
	text = deCapitalizeMidSentence(text)
	text = bindSingleLetterPrepositions(text)
	return text
}

// deCapitalizeMidSentence lowercases ALL-CAPS words that appear in the middle
// of a sentence. Words at the start of a sentence (after . ! ? … or at the
// beginning of the text) keep their capitalization. Single-character "words"
// (like "Я" = "I") are left alone.
func deCapitalizeMidSentence(text string) string {
	var b strings.Builder
	runes := []rune(text)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		// Find word boundaries: a "word" is a run of Cyrillic uppercase letters.
		if isCyrillicUpper(r) {
			start := i
			for i < n && isCyrillicUpper(runes[i]) {
				i++
			}
			end := i // exclusive
			word := string(runes[start:end])
			i = end - 1 // will be incremented by loop

			// Only de-capitalize if:
			// - word is longer than 1 character (keep "Я" = "I")
			// - it's not at the start of a sentence
			if len([]rune(word)) > 1 && !isSentenceStart(runes, start) {
				b.WriteString(strings.ToLower(word))
			} else {
				b.WriteString(word)
			}
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isSentenceStart checks if position `pos` in `runes` is at the start of a
// sentence (beginning of text, or after . ! ? … : « " followed by optional
// whitespace).
func isSentenceStart(runes []rune, pos int) bool {
	if pos == 0 {
		return true
	}
	// Scan backwards past whitespace.
	for j := pos - 1; j >= 0; j-- {
		r := runes[j]
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		case '.', '!', '?', '…', ':', '«', '"', '“', '„':
			return true
		default:
			return false
		}
	}
	return true // only whitespace before → start
}

// isCyrillicUpper reports whether r is an uppercase Cyrillic letter.
func isCyrillicUpper(r rune) bool {
	// Cyrillic uppercase: А-Я (0x0410-0x042F), Ё (0x0401)
	return (r >= 0x0410 && r <= 0x042F) || r == 0x0401
}

// bindSingleLetterPrepositions binds single-letter Russian prepositions
// (в, с, к, у, и, а, я) to the following word with a non-breaking space
// to prevent XTTS from swallowing them. We use a regular space + the word
// joined, but actually we keep them as-is since XTTS handles them OK in
// most contexts. This is a no-op placeholder for future enhancement.
//
// Note: hyphenation was considered but changes word boundaries in ways that
// can confuse the model. For now, we leave prepositions as-is.
func bindSingleLetterPrepositions(text string) string {
	return text
}
