package tts

import (
	"slices"
	"strings"
	"unicode"
)

// emptySSML is the SSML output for empty text.
const emptySSML = "<speak></speak>"

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
		return emptySSML
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

// StripSSMLTags removes all XML/SSML tags from text, leaving only the text
// content. Used to check for Latin letters in SSML without false positives
// from tag names like <speak>, <p>, <s>.
func StripSSMLTags(ssml string) string {
	var b strings.Builder
	b.Grow(len(ssml))
	inTag := false
	for _, r := range ssml {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
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
// punctuation (. ! ? …). The punctuation is kept as part of the sentence.
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
// on Latin characters), and preserves stress marks (+) as-is.
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
// so the text is all-Cyrillic before sending to the server.
func latinToCyrillic(r rune) rune {
	if cyr, ok := latinToCyrillicMap[r]; ok {
		return cyr
	}
	return r
}

// latinToCyrillicPhoneticMap maps Latin letters without visual Cyrillic
// equivalents to their phonetic equivalents. Used by TransliterateLatin for
// interactive SSML correction — the user chose to transliterate rather than
// abort, so we need a best-effort conversion for ALL Latin letters.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var latinToCyrillicPhoneticMap = map[rune]string{
	'F': "Ф", 'f': "ф",
	'G': "Г", 'g': "г",
	'I': "И", 'i': "и",
	'J': "ДЖ", 'j': "дж",
	'L': "Л", 'l': "л",
	'N': "Н", 'n': "н",
	'Q': "К", 'q': "к",
	'U': "У", 'u': "у",
	'W': "В", 'w': "в",
}

// latinLetterSpellingMap maps uppercase Latin letters to their Russian
// spelling pronunciation — how the letter is read aloud when spelling an
// acronym (e.g. "DNA" → "ДЭ Н А"). This ensures Silero pronounces acronyms
// correctly instead of trying to read them as visual look-alikes.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var latinLetterSpellingMap = map[rune]string{
	'A': "А", 'B': "БЭ", 'C': "ЦЭ", 'D': "ДЭ",
	'E': "Е", 'F': "ЭФ", 'G': "ГЭ", 'H': "АШ",
	'I': "И", 'J': "ДЖИ", 'K': "КА", 'L': "ЭЛЬ",
	'M': "ЭМ", 'N': "ЭН", 'O': "О", 'P': "ПЭ",
	'Q': "КУ", 'R': "ЭР", 'S': "ЭС", 'T': "ТЭ",
	'U': "У", 'V': "ВЭ", 'W': "ДАБЛЮ", 'X': "ИКС",
	'Y': "УАЙ", 'Z': "ЗЭД",
}

// minAcronymLen is the minimum length for a word to be treated as an acronym
// (all-uppercase Latin words shorter than this are transliterated normally).
const minAcronymLen = 2

// TransliterateLatin replaces ALL Latin letters in text with Cyrillic
// equivalents. All-uppercase Latin words (acronyms like "DNA", "GPS") are
// spelled out letter-by-letter using Russian letter pronunciations so Silero
// reads them correctly. Other Latin words are converted via visual look-alikes
// from latinToCyrillicMap plus phonetic equivalents from
// latinToCyrillicPhoneticMap. This is a best-effort conversion for the SSML
// stage when the user chooses to proceed despite Latin words in the translation.
func TransliterateLatin(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	var current []rune

	flushWord := func() {
		if len(current) == 0 {
			return
		}
		b.WriteString(transliterateWord(string(current)))
		current = current[:0]
	}

	for _, r := range text {
		if unicode.IsLetter(r) {
			current = append(current, r)
		} else {
			flushWord()
			b.WriteRune(r)
		}
	}
	flushWord()
	return b.String()
}

// transliterateWord converts a single word to Cyrillic. All-uppercase Latin
// words (acronyms) are spelled out letter-by-letter; other words use visual
// and phonetic look-alikes.
func transliterateWord(word string) string {
	if isLatinAcronym(word) {
		return spellAcronym(word)
	}
	var b strings.Builder
	for _, r := range word {
		if cyr, ok := latinToCyrillicMap[r]; ok {
			b.WriteRune(cyr)
			continue
		}
		if phon, ok := latinToCyrillicPhoneticMap[r]; ok {
			b.WriteString(phon)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// spellAcronym spells out an all-uppercase Latin word letter-by-letter using
// Russian letter pronunciations, separated by spaces.
func spellAcronym(word string) string {
	var b strings.Builder
	for _, r := range word {
		if spell, ok := latinLetterSpellingMap[r]; ok {
			b.WriteString(spell)
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// isLatinAcronym reports whether word is an all-uppercase Latin word with
// length >= minAcronymLen (e.g. "DNA", "GPS", "HP").
func isLatinAcronym(word string) bool {
	if len(word) < minAcronymLen {
		return false
	}
	for _, r := range word {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// FindLatinWords returns words in text that contain at least one Latin letter.
// Words are defined as maximal runs of letters (Unicode L category). The result
// is deduplicated and sorted.
func FindLatinWords(text string) []string {
	var words []string
	seen := make(map[string]bool)
	var current []rune

	flush := func() {
		if len(current) == 0 {
			return
		}
		word := string(current)
		current = current[:0]
		// Skip words without any Latin letters.
		if !HasLatinLetters(word) {
			return
		}
		if !seen[word] {
			seen[word] = true
			words = append(words, word)
		}
	}

	for _, r := range text {
		if unicode.IsLetter(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()

	slices.Sort(words)
	return words
}

// HasLatinLetters reports whether text contains any Latin letters (A-Z, a-z).
func HasLatinLetters(text string) bool {
	for _, r := range text {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return true
		}
	}
	return false
}

// isCyrillic reports whether r is a Cyrillic letter.
func isCyrillic(r rune) bool {
	return unicode.Is(unicode.Cyrillic, r)
}

// isBadSymbol reports whether r is a letter that is neither Cyrillic nor
// Latin — e.g. CJK, Arabic, Hebrew, Greek. These characters break Silero TTS.
func isBadSymbol(r rune) bool {
	if !unicode.IsLetter(r) {
		return false
	}
	if isCyrillic(r) {
		return false
	}
	if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
		return false
	}
	return true
}

// HasBadSymbols reports whether text contains any letters that are neither
// Cyrillic nor Latin (e.g. CJK, Arabic, Hebrew, Greek).
func HasBadSymbols(text string) bool {
	for _, r := range text {
		if isBadSymbol(r) {
			return true
		}
	}
	return false
}

// FindBadSymbols returns the non-Cyrillic, non-Latin letters found in text,
// deduplicated and sorted. These are characters that break Silero TTS (e.g.
// CJK characters accidentally inserted by the translation model).
func FindBadSymbols(text string) []string {
	seen := make(map[rune]bool)
	var symbols []rune
	for _, r := range text {
		if isBadSymbol(r) && !seen[r] {
			seen[r] = true
			symbols = append(symbols, r)
		}
	}
	slices.Sort(symbols)
	result := make([]string, len(symbols))
	for i, r := range symbols {
		result[i] = string(r)
	}
	return result
}

// StripBadSymbols removes letters that are neither Cyrillic nor Latin from
// text. Non-letter characters (punctuation, digits, whitespace) are preserved.
func StripBadSymbols(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if !isBadSymbol(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
