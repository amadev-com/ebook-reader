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
//  3. Sentence-ending dots: replace "." at the end of sentences with ",!"
//     to work around an XTTS v2 bug where dots cause unnatural pauses or
//     breaks. Mid-sentence dots (abbreviations, numbers, ellipsis) are
//     preserved.
//
// Respelling (term-specific replacements) is applied separately by
// Respelling.Apply before this normalization.
func NormalizeRussian(text string) string {
	text = deCapitalizeMidSentence(text)
	text = bindSingleLetterPrepositions(text)
	text = replaceSentenceDots(text)
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

// replaceSentenceDots replaces sentence-ending dots with ",!" to work around
// an XTTS v2 bug where dots cause unnatural pauses or breaks in Russian text.
// Only dots that end a sentence are replaced — mid-sentence dots (after
// abbreviations, inside numbers like "3.14", ellipsis "…") are preserved.
//
// A dot is considered sentence-ending if it's followed by:
//   - end of text
//   - whitespace + uppercase letter or quote (start of next sentence)
//   - a newline (end of paragraph)
//
// A dot is NOT replaced if:
//   - preceded by a digit and followed by a digit (decimal number: "3.14")
//   - preceded by a single letter and followed by whitespace + lowercase
//     (abbreviation: "т. е.")
//   - part of an ellipsis ("…" or "...")
func replaceSentenceDots(text string) string {
	runes := []rune(text)
	n := len(runes)
	var b strings.Builder
	for i := 0; i < n; i++ {
		r := runes[i]
		if r != '.' {
			b.WriteRune(r)
			continue
		}

		// Check for ellipsis: "..." (three consecutive dots)
		if i+1 < n && runes[i+1] == '.' {
			// Part of multi-dot sequence (ellipsis) — keep as-is.
			b.WriteRune(r)
			continue
		}
		// Check if this is the last dot of "..." (i.e. preceded by two dots)
		if i >= 2 && runes[i-1] == '.' && runes[i-2] == '.' {
			b.WriteRune(r)
			continue
		}

		// Check for decimal number: digit.digit
		if i > 0 && isDigit(runes[i-1]) && i+1 < n && isDigit(runes[i+1]) {
			b.WriteRune(r)
			continue
		}

		// Check for abbreviation: single letter followed by dot followed by
		// space + lowercase letter (e.g. "т. е.", "пр. и.")
		if i > 0 && isLetter(runes[i-1]) && (i < 2 || !isLetter(runes[i-2])) {
			// Single-letter abbreviation — check what follows the dot.
			if i+1 < n && (runes[i+1] == ' ' || runes[i+1] == '\t') {
				// Look ahead past whitespace for a lowercase letter.
				j := i + 1
				for j < n && (runes[j] == ' ' || runes[j] == '\t') {
					j++
				}
				if j < n && isLetter(runes[j]) && !isUpper(runes[j]) {
					// Abbreviation like "т. е." — keep the dot.
					b.WriteRune(r)
					continue
				}
			}
		}

		// Determine if this dot ends a sentence by looking at what follows.
		if i+1 >= n {
			// End of text — sentence-ending.
			b.WriteString(",!")
			continue
		}

		next := runes[i+1]
		switch next {
		case '\n':
			// End of line — sentence-ending.
			b.WriteString(",!")
		case ' ', '\t':
			// Look ahead past whitespace to see if next sentence starts
			// (uppercase letter, quote, or another paragraph).
			j := i + 1
			for j < n && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			if j >= n {
				// Only whitespace after dot until end of text — sentence-ending.
				b.WriteString(",!")
			} else if isUpper(runes[j]) || isQuote(runes[j]) || runes[j] == '—' {
				// Next sentence starts with uppercase/quote — sentence-ending.
				b.WriteString(",!")
			} else {
				// Lowercase after dot — likely abbreviation or continuation.
				// Keep the dot.
				b.WriteRune(r)
			}
		default:
			// Something other than whitespace/newline follows the dot.
			// Could be a closing quote: ." or ." — treat as sentence-ending.
			if isQuote(next) || next == '»' {
				b.WriteString(",!")
			} else {
				// Non-whitespace, non-quote — keep the dot (e.g. "3.14").
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// isDigit reports whether r is an ASCII digit.
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isLetter reports whether r is a Cyrillic or Latin letter.
func isLetter(r rune) bool {
	return isCyrillicUpper(r) || isCyrillicLower(r) || isLatinUpper(r) || isLatinLower(r)
}

// isCyrillicLower reports whether r is a lowercase Cyrillic letter.
func isCyrillicLower(r rune) bool {
	// Cyrillic lowercase: а-я (0x0430-0x044F), ё (0x0451)
	return (r >= 0x0430 && r <= 0x044F) || r == 0x0451
}

// isLatinUpper reports whether r is an uppercase Latin letter.
func isLatinUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

// isLatinLower reports whether r is a lowercase Latin letter.
func isLatinLower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

// isUpper reports whether r is any uppercase letter.
func isUpper(r rune) bool {
	return isCyrillicUpper(r) || isLatinUpper(r)
}

// isQuote reports whether r is a quotation mark.
func isQuote(r rune) bool {
	switch r {
	case '"', '\u201C', '\u201D', '\'', '«', '»', '„', '‟':
		return true
	}
	return false
}
