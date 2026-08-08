package tts

import "strings"

// GenerateSSML takes plain Russian text (with stress marks already applied)
// and wraps it in SSML tags for Silero TTS. The text is split into paragraphs
// (by blank lines or single newlines) and sentences (by . ! ? …), then wrapped
// in <speak>, <p>, and <s> tags. Stress marks (+ before vowels) are preserved
// as-is — they are part of the text that Silero interprets natively.
func GenerateSSML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "<speak></speak>"
	}

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
// punctuation (. ! ? …). The punctuation is kept as part of the sentence.
func splitSentences(para string) []string {
	runes := []rune(para)
	n := len(runes)
	var sentences []string
	start := 0

	for i := 0; i < n; i++ {
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

// escapeXML escapes the five special XML characters. Stress marks (+) are
// NOT escaped — they are valid in SSML text content.
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
			b.WriteRune(r)
		}
	}
	return b.String()
}
