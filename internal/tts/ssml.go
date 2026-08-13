package tts

import "strings"

// GenerateSSML takes plain Russian text (with stress marks already applied)
// and wraps it in SSML tags for Silero TTS. The text is split into paragraphs
// (by blank lines or single newlines) and sentences (by . ! ? …), then wrapped
// in <speak>, <p>, and <s> tags. Stress marks (+ before vowels) are preserved
// as-is — they are part of the text that Silero interprets natively.
// Malformed stress marks (+ not before a vowel) are stripped to prevent
// Silero SSML parser crashes.
func GenerateSSML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "<speak></speak>"
	}

	// Sanitize stress marks: remove any + that is not immediately before a
	// vowel. Malformed stress marks (e.g. "М+С", "фамил+ьяр") cause Silero's
	// SSML parser to crash with "'NoneType' object has no attribute 'keys'".
	text = sanitizeStressMarks(text)

	paragraphs := splitParagraphs(text)

	var b strings.Builder
	b.WriteString("<speak>\n")
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		sentences := splitSentences(para)
		b.WriteString("<p>")
		for _, sent := range sentences {
			sent = strings.TrimSpace(sent)
			if sent == "" {
				continue
			}
			b.WriteString("<s>")
			b.WriteString(escapeXML(sent))
			b.WriteString("</s>")
		}
		b.WriteString("</p>\n")
	}
	b.WriteString("</speak>")
	return b.String()
}

// splitParagraphs splits text into paragraphs by blank lines (preferred) or
// single newlines. If the text contains no newlines, the entire text is one
// paragraph.
func splitParagraphs(text string) []string {
	// Try blank-line splitting first.
	if strings.Contains(text, "\n\n") {
		parts := strings.Split(text, "\n\n")
		var result []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	}
	// Fall back to single newline splitting.
	if strings.Contains(text, "\n") {
		parts := strings.Split(text, "\n")
		var result []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	}
	return []string{text}
}

// splitSentences splits a paragraph into sentences by sentence-ending
// splitSentences separates a paragraph into sentences at terminal punctuation,
// retaining the punctuation and following closing quotes or brackets. Any
// remaining text is returned as a final sentence.
func splitSentences(para string) []string {
	runes := []rune(para)
	n := len(runes)
	var sentences []string
	start := 0

	for i := range n {
		r := runes[i]
		if r == '.' || r == '!' || r == '?' || r == '…' {
			// Include the punctuation and any trailing quotes/brackets.
			end := i + 1
			// Include closing quotes after punctuation.
			for end < n {
				next := runes[end]
				if next == '»' || next == '"' || next == '“' || next == '„' ||
					next == ')' || next == ']' || next == '…' {
					end++
				} else {
					break
				}
			}
			sentence := strings.TrimSpace(string(runes[start:end]))
			if sentence != "" {
				sentences = append(sentences, sentence)
			}
			start = end
		}
	}

	// Remaining text without terminal punctuation.
	if start < n {
		remaining := strings.TrimSpace(string(runes[start:]))
		if remaining != "" {
			sentences = append(sentences, remaining)
		}
	}

	if len(sentences) == 0 {
		return []string{para}
	}
	return sentences
}

// sanitizeStressMarks removes any stress mark (+) that is not immediately
// before a Cyrillic vowel. Silero expects '+' before the stressed vowel (e.g.
// "к+едров"), and malformed marks like "М+С" or "фамил+ьяр" crash its SSML
// parser. The + is simply removed, leaving the plain text.
func sanitizeStressMarks(text string) string {
	if !strings.Contains(text, "+") {
		return text
	}
	runes := []rune(text)
	var b strings.Builder
	b.Grow(len(text))
	for i, r := range runes {
		if r == '+' {
			// Keep the + only if the next rune is a Cyrillic vowel.
			if i+1 < len(runes) && isCyrillicVowel(runes[i+1]) {
				b.WriteRune(r)
			}
			// Otherwise skip the + (strip it).
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isCyrillicVowel reports whether r is a Cyrillic vowel (upper or lower case).
func isCyrillicVowel(r rune) bool {
	switch r {
	case 'а', 'е', 'ё', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я',
		'А', 'Е', 'Ё', 'И', 'О', 'У', 'Ы', 'Э', 'Ю', 'Я':
		return true
	}
	return false
}

// HasValidStressMark reports whether s contains at least one '+' that is
// immediately before a Cyrillic vowel. This is used to validate AI-produced
// stress entries before merging them into the vocabulary.
func HasValidStressMark(s string) bool {
	runes := []rune(s)
	for i, r := range runes {
		if r == '+' && i+1 < len(runes) && isCyrillicVowel(runes[i+1]) {
			return true
		}
	}
	return false
}

// escapeXML escapes the five special XML characters, replaces Latin letters
// with their Cyrillic visual equivalents (Silero's Russian SSML parser crashes
// escapeXML escapes XML-sensitive characters and converts mapped Latin characters to their Cyrillic equivalents, preserving all other characters.
func escapeXML(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		default:
			b.WriteRune(latinToCyrillic(r))
		}
	}
	return b.String()
}

// latinToCyrillicMap maps Latin letters to their Cyrillic visual equivalents.
// Silero's Russian TTS model cannot handle Latin characters in SSML — it
// crashes with "'NoneType' object has no attribute 'keys'" and returns silence.
// Some entries are phonetic rather than visual (noted in comments).
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var latinToCyrillicMap = map[rune]rune{
	'A': 'А',
	'B': 'В',
	'C': 'С',
	'D': 'Д',
	'E': 'Е',
	'H': 'Н',
	'K': 'К',
	'M': 'М',
	'O': 'О',
	'P': 'Р',
	'R': 'Р', // phonetic: Latin R → Cyrillic Р (both are R sound)
	'S': 'С',
	'T': 'Т',
	'V': 'В', // phonetic: Latin V → Cyrillic В (both are V sound)
	'X': 'Х',
	'Y': 'У',
	'Z': 'З', // phonetic: Latin Z → Cyrillic З (both are Z sound)
	'a': 'а',
	'b': 'в',
	'c': 'с',
	'd': 'д',
	'e': 'е',
	'h': 'н',
	'k': 'к',
	'm': 'м',
	'o': 'о',
	'p': 'р',
	'r': 'р', // phonetic: Latin r → Cyrillic р
	's': 'с',
	't': 'т',
	'v': 'в',
	'x': 'х',
	'y': 'у',
	'z': 'з', // phonetic: Latin z → Cyrillic з
}

// latinToCyrillic replaces Latin letters that have Cyrillic visual
// equivalents. Silero's Russian TTS model cannot handle Latin characters in
// SSML — it crashes with "'NoneType' object has no attribute 'keys'" and
// returns silence. This maps Latin letters to their Cyrillic look-alikes
// latinToCyrillic converts a mapped Latin rune to its Cyrillic equivalent and leaves other runes unchanged.
func latinToCyrillic(r rune) rune {
	if cyr, ok := latinToCyrillicMap[r]; ok {
		return cyr
	}
	return r
}
