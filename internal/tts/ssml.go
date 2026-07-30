package tts

import (
	"fmt"
	"regexp"
	"strings"
)

// SSML is the in-memory representation of a speech synthesis document. It is
// a flat list of paragraphs, each containing sentences. Pronunciation hints
// are applied at the sentence level as <phoneme> tags around matching terms.
type SSML struct {
	Paragraphs []SSMLParagraph
}

// SSMLParagraph is one paragraph of speech.
type SSMLParagraph struct {
	Sentences []SSMLSentence
}

// SSMLSentence is one sentence, which may contain inline pronunciation hints.
type SSMLSentence struct {
	// Text is the raw sentence text (no SSML tags).
	Text string

	// Hints are pronunciation hints for terms found within this sentence,
	// with their character offset in Text.
	Hints []SSMLHint
}

// SSMLHint marks a span of text that should use a specific phonetic
// pronunciation.
type SSMLHint struct {
	// Start is the byte offset in the sentence Text where the term begins.
	Start int

	// End is the byte offset in the sentence Text where the term ends.
	End int

	// Term is the matched term text.
	Term string

	// Phonemes is the phonetic representation.
	Phonemes string

	// Alphabet is the phonetic alphabet (e.g. "ipa").
	Alphabet string
}

var (
	// sentenceEnd matches sentence-ending punctuation followed by space or
	// end-of-string. Supports Cyrillic punctuation (?!«» and ellipsis).
	sentenceEnd = regexp.MustCompile(`([.!?…]+)\s+`)

	// paragraphSplit splits on double newlines (blank line between paragraphs).
	paragraphSplit = regexp.MustCompile(`\n\s*\n`)

	// whitespaceRun collapses runs of whitespace into single spaces.
	whitespaceRun = regexp.MustCompile(`[ \t\r\f\v]+`)
)

// ParseTranslation converts a Russian translation text into an SSML structure.
// The text is split into paragraphs (double-newline separated) and sentences
// (by .!?… punctuation). Pronunciation hints are applied by scanning each
// sentence for terms in the Pronunciation store.
func ParseTranslation(text string, pron *Pronunciation) *SSML {
	text = strings.TrimSpace(text)
	if text == "" {
		return &SSML{}
	}
	if pron == nil {
		pron = &Pronunciation{}
	}

	paraTexts := paragraphSplit.Split(text, -1)
	paragraphs := make([]SSMLParagraph, 0, len(paraTexts))

	for _, paraText := range paraTexts {
		paraText = normalizeWhitespace(paraText)
		if paraText == "" {
			continue
		}
		sentences := splitSentences(paraText)
		ssmlSentences := make([]SSMLSentence, 0, len(sentences))
		for _, sent := range sentences {
			sent = strings.TrimSpace(sent)
			if sent == "" {
				continue
			}
			hints := findHints(sent, pron)
			ssmlSentences = append(ssmlSentences, SSMLSentence{
				Text:  sent,
				Hints: hints,
			})
		}
		if len(ssmlSentences) > 0 {
			paragraphs = append(paragraphs, SSMLParagraph{Sentences: ssmlSentences})
		}
	}

	return &SSML{Paragraphs: paragraphs}
}

// normalizeWhitespace trims and collapses internal whitespace runs to single
// spaces within a paragraph.
func normalizeWhitespace(s string) string {
	s = strings.TrimSpace(s)
	s = whitespaceRun.ReplaceAllString(s, " ")
	return s
}

// splitSentences breaks a paragraph into sentences by terminal punctuation.
// The punctuation is preserved as part of each sentence.
func splitSentences(para string) []string {
	indices := sentenceEnd.FindAllStringSubmatchIndex(para, -1)
	if len(indices) == 0 {
		return []string{para}
	}

	var sentences []string
	prev := 0
	for _, idx := range indices {
		// idx[2] is the start of the punctuation group, idx[3] is its end.
		punctEnd := idx[3]
		sentences = append(sentences, para[prev:punctEnd])
		prev = idx[1] // after the whitespace following punctuation
	}
	if prev < len(para) {
		tail := strings.TrimSpace(para[prev:])
		if tail != "" {
			sentences = append(sentences, tail)
		}
	}
	return sentences
}

// findHints scans sentence text for pronunciation terms and returns hints
// with their byte offsets. Longer terms are matched first so that
// multi-word terms take priority over their sub-terms.
func findHints(sentence string, pron *Pronunciation) []SSMLHint {
	if len(pron.Entries) == 0 {
		return nil
	}

	// Sort entries by term length descending for greedy matching.
	entries := make([]PronunciationEntry, len(pron.Entries))
	copy(entries, pron.Entries)
	sortByLengthDesc(entries)

	var hints []SSMLHint
	used := make([][2]int, 0, len(entries)) // occupied spans

	for _, entry := range entries {
		if entry.Term == "" || entry.Phonemes == "" {
			continue
		}
		searchStart := 0
		for {
			idx := indexIgnoreCase(sentence, entry.Term, searchStart)
			if idx < 0 {
				break
			}
			end := idx + len(entry.Term)

			// Check word boundaries: the match should not be in the middle
			// of a longer word.
			if !atWordBoundary(sentence, idx, end) {
				searchStart = idx + 1
				continue
			}

			// Check for overlap with already-matched spans.
			if overlapsAny(used, idx, end) {
				searchStart = idx + 1
				continue
			}

			hints = append(hints, SSMLHint{
				Start:    idx,
				End:      end,
				Term:     sentence[idx:end],
				Phonemes: entry.Phonemes,
				Alphabet: entry.PhonAlphabet(),
			})
			used = append(used, [2]int{idx, end})
			searchStart = end
		}
	}

	return hints
}

// sortByLengthDesc sorts pronunciation entries by term length (longest first)
// for greedy matching.
func sortByLengthDesc(entries []PronunciationEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && len(entries[j].Term) > len(entries[j-1].Term); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

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

// overlapsAny checks if [start, end) overlaps any span in used.
func overlapsAny(used [][2]int, start, end int) bool {
	for _, span := range used {
		if start < span[1] && end > span[0] {
			return true
		}
	}
	return false
}

// Render produces a W3C SSML XML string from the SSML structure. Each
// paragraph is wrapped in <p>, each sentence in <s>, and pronunciation hints
// in <phoneme alphabet="..." ph="..."> tags.
func (s *SSML) Render() string {
	if s == nil || len(s.Paragraphs) == 0 {
		return "<speak></speak>\n"
	}

	var b strings.Builder
	b.WriteString("<speak>\n")
	for _, para := range s.Paragraphs {
		b.WriteString("  <p>\n")
		for _, sent := range para.Sentences {
			b.WriteString("    <s>")
			b.WriteString(renderSentence(sent))
			b.WriteString("</s>\n")
		}
		b.WriteString("  </p>\n")
	}
	b.WriteString("</speak>\n")
	return b.String()
}

// renderSentence produces the inner XML for a sentence, inserting <phoneme>
// tags at hint positions. Text is XML-escaped.
func renderSentence(sent SSMLSentence) string {
	if len(sent.Hints) == 0 {
		return escapeXML(sent.Text)
	}

	var b strings.Builder
	prev := 0
	for _, hint := range sent.Hints {
		if hint.Start > prev {
			b.WriteString(escapeXML(sent.Text[prev:hint.Start]))
		}
		fmt.Fprintf(&b, `<phoneme alphabet="%s" ph="%s">`,
			escapeXML(hint.Alphabet), escapeXML(hint.Phonemes))
		b.WriteString(escapeXML(sent.Text[hint.Start:hint.End]))
		b.WriteString("</phoneme>")
		prev = hint.End
	}
	if prev < len(sent.Text) {
		b.WriteString(escapeXML(sent.Text[prev:]))
	}
	return b.String()
}

// ExtractPlainText strips SSML tags and returns the raw text content. Engines
// that do not support SSML natively use this to get plain text.
func ExtractPlainText(ssml string) string {
	// Remove all XML tags, replacing them with nothing (tags are inline
	// markers and should not introduce spaces).
	text := stripTags(ssml)
	// Unescape XML entities.
	text = unescapeXML(text)
	// Collapse whitespace.
	text = normalizeWhitespace(text)
	return strings.TrimSpace(text)
}

// stripTags removes all <...> sequences from s without introducing spaces.
// SSML tags like <phoneme> are inline markers around existing text, so
// removing them should not alter the surrounding text.
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeXML escapes the five XML special characters.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// unescapeXML reverses the five XML special character entities.
func unescapeXML(s string) string {
	s = strings.ReplaceAll(s, "&apos;", "'")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}
