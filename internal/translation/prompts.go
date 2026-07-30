package translation

import (
	"fmt"
	"strings"
)

// ChapterInfo is the metadata about a chapter passed to prompt builders.
type ChapterInfo struct {
	ID    int
	Title string
}

// --- Glossary extraction prompts (bookai analyze) ---

// GlossaryExtractionSystem is the system prompt for the terminology extraction
// call. It instructs the model to return a JSON object with characters,
// places, organizations, titles, and invented terms. The model receives full
// chapter texts in batches and must extract all meaningful terms from each
// batch.
const GlossaryExtractionSystem = `You are a literary translation assistant specializing in English-to-Russian book translation. Your task is to analyze chapters from a book and extract a terminology glossary that will ensure consistent translation across all chapters.

You will receive the FULL TEXT of several chapters. Read each chapter carefully and extract ALL meaningful terms, characters, places, and invented words — not just those at the beginning.

Extract:
- characters: named people (protagonists, antagonists, supporting). Include their role if obvious.
- places: named locations (cities, countries, fictional places).
- organizations: named groups, factions, orders, companies.
- titles: titles, ranks, honorifics used in the book.
- terms: invented words, magic systems, technologies, or genre-specific jargon.

For each entry, provide:
- source: the English term as it appears in the text
- target: the recommended Russian translation
- type: one of "character", "place", "organization", "title", "term"

Return a JSON object with this exact shape:
{
  "characters": [{"name": "...", "translation": "...", "role": "...", "description": "..."}],
  "terms": [{"source": "...", "target": "...", "type": "..."}]
}

Guidelines:
- Only extract terms that appear meaningful or recurring. Skip common words.
- For character names, use transliteration unless the character has an established Russian name.
- Be consistent: if "The Order" appears, translate it the same way everywhere.
- If the book is a web novel or light novel, pay attention to game-like terms (levels, quests, stats, systems).`

// GlossaryExtractionUser builds the user prompt from chapter full texts.
// Chapters are processed in batches to keep each API call within context
// limits while still reading the full text of every chapter. If
// existingGlossary is non-empty, it is included so the model can merge new
// findings with previously extracted terms rather than producing duplicates.
func GlossaryExtractionUser(chapters []ChapterText, existingGlossary string, lockedTerms string) string {
	var b strings.Builder
	b.WriteString("Analyze these chapters and extract the terminology glossary.\n\n")
	if lockedTerms != "" {
		b.WriteString("LOCKED TRANSLATIONS — you MUST use these exact translations for the matching terms. ")
		b.WriteString("Do not change them. You may still extract other fields (role, description, type) ")
		b.WriteString("from context, but the translation/target must match exactly:\n\n")
		b.WriteString(lockedTerms)
		b.WriteString("\n\n")
	}
	if existingGlossary != "" {
		b.WriteString("Glossary extracted from previous chapters (merge with your new findings, do not duplicate):\n\n")
		b.WriteString(existingGlossary)
		b.WriteString("\n\n")
	}
	b.WriteString("Chapters (full text):\n\n")
	for _, ch := range chapters {
		fmt.Fprintf(&b, "## %s\n%s\n\n", ch.Title, ch.Text)
	}
	b.WriteString("\nReturn the merged JSON glossary now (all previous terms + any new ones from these chapters).")
	return b.String()
}

// ChapterText is a chapter's title + full source text, used for batched
// glossary extraction. Each batch sends the full text of N chapters to the
// model so no terms are missed regardless of where they appear.
type ChapterText struct {
	Title string
	Text  string
}

// --- Translation prompts (bookai translate) ---

// System builds the system prompt for a chapter translation. It includes the
// translator persona, the glossary block, and previous chapter context.
func System(glossaryBlock, prevContext string) string {
	var b strings.Builder
	b.WriteString(`You are a professional literary translator translating an English book into Russian. Your translation must be natural, fluent Russian that reads as if originally written in that language — not a word-by-word rendering.

Rules:
- Preserve the author's tone, style, and narrative voice.
- Translate all prose into Russian. Do not leave English text in the output.
- Use the glossary consistently: every glossary term must be translated exactly as specified.
- Keep paragraph breaks exactly as in the source (separated by blank lines).
- Do not add commentary, notes, or explanations. Output ONLY the translated text.
- If you discover a new recurring term not in the glossary, translate it consistently within this chapter. The system will extract new terms separately.`)
	if glossaryBlock != "" {
		b.WriteString("\n\n")
		b.WriteString(glossaryBlock)
	}
	if prevContext != "" {
		b.WriteString("\n\n")
		b.WriteString(prevContext)
	}
	return b.String()
}

// User builds the user prompt for translating one chapter.
func User(ch ChapterInfo, source string) string {
	return fmt.Sprintf("Translate Chapter %d: %s\n\n%s", ch.ID, ch.Title, source)
}

// --- Summary prompts (post-translation memory) ---

// SummarySystem is the system prompt for generating a chapter summary for
// future translation context.
const SummarySystem = `You are a literary translation assistant. Summarize the chapter for future translation context. Keep the summary concise (3-5 sentences) and focus on:
- Characters that appeared and what they did
- New character introductions
- Key plot developments
- Any new terminology or relationships established

Write the summary in English. Be specific about names and terms so the next chapter's translation stays consistent.`

// SummaryUser builds the user prompt for summarizing a translated chapter.
// We summarize from the source (English) text since the summary is for
// translation context, not for the reader.
func SummaryUser(ch ChapterInfo, source string) string {
	return fmt.Sprintf("Summarize this chapter for translation context:\n\n## %s\n\n%s", ch.Title, source)
}

// --- New-term extraction prompts (post-translation glossary update) ---

// NewTermsSystem is the system prompt for extracting new glossary terms from
// a freshly translated chapter.
const NewTermsSystem = `You are a literary translation assistant. Compare the English source and the Russian translation, and extract any new recurring terms that should be added to the glossary.

Focus on:
- Character names that appear for the first time
- New places, organizations, or titles
- Invented terms or genre-specific jargon

Return a JSON object: {"terms": [{"source": "English term", "target": "Russian translation", "type": "character|place|organization|title|term"}]}

Only extract terms that are clearly meaningful and likely to recur. Skip common words and one-off references. If no new terms are found, return {"terms": []}.`

// NewTermsUser builds the user prompt for new-term extraction.
func NewTermsUser(_ ChapterInfo, source, translation string) string {
	return fmt.Sprintf("Source (English):\n\n%s\n\nTranslation (Russian):\n\n%s\n\nExtract new glossary terms as JSON.", source, translation)
}

// --- Pronunciation extraction prompts (bookai pronounce) ---

// PronunciationSystem is the system prompt for generating IPA phonetic
// representations of glossary terms and character names for TTS engines.
const PronunciationSystem = `You are a phonetics expert specializing in Russian pronunciation for text-to-speech systems. Your task is to provide IPA (International Phonetic Alphabet) transcriptions for Russian terms so that TTS engines pronounce them correctly.

Rules:
- Provide IPA transcription for the RUSSIAN text (the "target" field), not the English source.
- Use standard IPA notation: /ˈgorod/ for "город", /kʊˈɪn/ for "Куинн".
- Place primary stress mark ˈ before the stressed syllable.
- For transliterated English names, provide the pronunciation as it would be read by a Russian speaker (not the original English pronunciation).
- For standard Russian words, provide their normal Russian IPA pronunciation.
- Skip terms that are common Russian words with unambiguous pronunciation.
- Focus on names, transliterated foreign words, invented terms, and anything a TTS engine might mispronounce.

Return a JSON object with this exact shape:
{
  "entries": [{"term": "Russian term", "phonemes": "IPA transcription", "alphabet": "ipa"}]
}

If a term does not need pronunciation hints, omit it from the response. If no terms need hints, return {"entries": []}.`

// PronunciationUser builds the user prompt from glossary terms and character
// names. It sends the Russian (target) text for each term so the model can
// provide IPA phonemes for the text the TTS engine will actually read.
func PronunciationUser(terms []PronunciationInput) string {
	var b strings.Builder
	b.WriteString("Provide IPA pronunciation hints for these Russian terms used in a book translation.\n\n")
	b.WriteString("Terms (Russian text that the TTS engine will read):\n\n")
	for _, t := range terms {
		fmt.Fprintf(&b, "- %s\n", t.Russian)
	}
	b.WriteString("\nReturn the JSON pronunciation hints now.")
	return b.String()
}

// PronunciationInput is one term to generate pronunciation hints for.
type PronunciationInput struct {
	Russian string // the Russian text as it appears in translation
	Source  string // the English source (for context, not pronounced)
	Type    string // term type: character, place, organization, title, term
}
