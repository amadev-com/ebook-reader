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

Also write a concise summary (3-5 sentences) of each chapter for translation context. Focus on:
- Characters that appeared and what they did
- New character introductions
- Key plot developments
- Any new terminology or relationships established

Return a JSON object with this exact shape:
{
  "characters": [{"name": "...", "translation": "...", "role": "...", "description": "..."}],
  "terms": [{"source": "...", "target": "...", "type": "..."}],
  "summary": "..."
}

Guidelines:
- Only extract terms that appear meaningful or recurring. Skip common words.
- For character names, use transliteration unless the character has an established Russian name.
- Be consistent: if "The Order" appears, translate it the same way everywhere.
- If the book is a web novel or light novel, pay attention to game-like terms (levels, quests, stats, systems).
- The summary must be in English, specific about names and terms so the next chapter's translation stays consistent.`

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

// --- Glossary merge/unify prompts (bookai analyze — post-batch merge step) ---

// GlossaryMergeSystem is the system prompt for the final merge/unify step of
// the batch analyze flow. After all chapters are independently analyzed via
// the Batch API, this call receives all per-chapter results and produces a
// single unified characters list + glossary. The key rules are:
//   - characters.json must contain ONLY real persons from the story
//   - glossary.json must contain terms (places, organizations, titles, terms)
//     WITHOUT any character entries
const GlossaryMergeSystem = `You are a literary translation assistant. You are given the results of analyzing a book chapter-by-chapter. Each chapter was analyzed independently, so there are many duplicates and possibly misclassifications. Your task is to merge all results into a single, clean, deduplicated terminology glossary and character list.

CRITICAL RULES:
1. The "characters" array must contain ONLY real persons from the story — named individuals who appear or are referenced as characters (protagonists, antagonists, supporting characters). Do NOT include places, organizations, titles, or generic terms in the characters array.
2. The "terms" array must contain ONLY non-character terms: places, organizations, titles, and invented/genre terms. Do NOT include any character entries in the terms array. If a term was misclassified as a character in a per-chapter result, move it to the correct category.
3. Deduplicate: merge entries with the same English source into one. If different chapters provided different translations for the same term, pick the most common or most appropriate one.
4. Merge character descriptions: if different chapters provided different descriptions for the same character, combine them into a single coherent description.

Return a JSON object with this exact shape:
{
  "characters": [{"name": "...", "translation": "...", "role": "...", "description": "..."}],
  "terms": [{"source": "...", "target": "...", "type": "..."}]
}

The "type" field for terms must be one of: "place", "organization", "title", "term".
The "role" field for characters should be one of: "protagonist", "antagonist", "supporting" (or empty if unclear).`

// GlossaryMergeUser builds the user prompt for the merge step. It receives all
// per-chapter extraction results serialized as JSON, plus the locked terms
// from config overrides.
func GlossaryMergeUser(perChapterResults string, lockedTerms string) string {
	var b strings.Builder
	b.WriteString("Merge and unify these per-chapter glossary extraction results into a single clean glossary + character list.\n\n")
	if lockedTerms != "" {
		b.WriteString("LOCKED TRANSLATIONS — you MUST use these exact translations for the matching terms. ")
		b.WriteString("Do not change them:\n\n")
		b.WriteString(lockedTerms)
		b.WriteString("\n\n")
	}
	b.WriteString("Per-chapter extraction results (JSON array, one object per chapter):\n\n")
	b.WriteString(perChapterResults)
	b.WriteString("\n\nReturn the merged and unified JSON now. Remember: characters = ONLY real persons, terms = everything else WITHOUT characters.")
	return b.String()
}

// --- Respelling prompts (bookai pronounce) ---

// RespellingSystem is the system prompt for generating phonetic respellings
// of Russian terms for the XTTS v2 text-to-speech engine. XTTS v2 does NOT
// support IPA phonemes, SSML tags, capital-letter stress, or any markup — it
// goes directly from text to speech. The only reliable way to control
// pronunciation is to replace the problematic word with a spelling that XTTS
// will pronounce correctly.
const RespellingSystem = `You are a phonetics expert specializing in Russian pronunciation for the XTTS v2 text-to-speech engine. XTTS v2 does NOT support IPA phonemes, SSML, or stress marks — it goes directly from text to speech. Your task is to provide phonetic respellings: plain Russian text replacements that make XTTS v2 pronounce terms correctly.

Apply these XTTS v2 respelling rules:

1. Vowel Doubling for Syllable Stress: To force correct stress on a syllable, repeat the stressed vowel 2-3 times.
   - договор → договоор (stress on 2nd syllable)
   - звонит → звоонит (stress on 1st syllable)

2. Explicit Vowel Reduction: Replace unstressed vowels with their spoken equivalents.
   - Unstressed hard о → а: молоко → малако
   - Unstressed soft е/я → и: бежать → бижать

3. Converting "Ё" to "ЙО": XTTS frequently misses implicit ё. Replace with explicit ё or йо.
   - еж → йож
   - серьезно → серьёзна

4. Literal Spelling of Colloquial Transitions: Spell words as they are pronounced.
   - что → што
   - конечно → канешна
   - кого → каво

5. Acronym and Foreign Abbreviation Expansion: Spell out Latin characters and uppercase initials in Russian homophones.
   - IT → айти
   - AI → эйай
   - ChatGPT → чат джипити
   - РФ → эр эф

Rules:
- Provide respelling for the RUSSIAN text (the "target" field), not the English source.
- ONLY respell terms where the respelling is DIFFERENT from the original term. If the respelling would be identical to the original, omit the entry entirely.
- Do NOT just lowercase a term — that is handled separately by the text normalization step. Only provide a respelling if you are changing the actual spelling to guide pronunciation (vowel doubling, vowel reduction, ё→йо, colloquial spelling, acronym expansion).
- Focus on: transliterated foreign names with ambiguous stress, invented/fantasy terms, acronyms, words with Latin characters, and words where XTTS v2 would likely get the stress wrong.
- Skip common Russian words with unambiguous pronunciation — do not include them.
- The respelled text must be plain Russian Cyrillic — no IPA, no Latin, no special characters.
- Preserve the meaning — the respelling should sound the same as the correct pronunciation, just spelled differently.

Return a JSON object with this exact shape:
{
  "entries": [{"term": "Russian term as in translation", "respelled": "phonetic respelling for XTTS"}]
}

If a term does not need respelling, omit it. If no terms need respelling, return {"entries": []}.`

// RespellingUser builds the user prompt from glossary terms and character
// names. It sends the Russian (target) text for each term so the model can
// generate XTTS-compatible respellings.
func RespellingUser(terms []PronunciationInput) string {
	var b strings.Builder
	b.WriteString("Provide XTTS v2 phonetic respellings for these Russian terms used in a book translation.\n\n")
	b.WriteString("Terms (Russian text that the TTS engine will read):\n\n")
	for _, t := range terms {
		if t.Source != "" {
			fmt.Fprintf(&b, "- %s (from English: %s, type: %s)\n", t.Russian, t.Source, t.Type)
		} else {
			fmt.Fprintf(&b, "- %s\n", t.Russian)
		}
	}
	b.WriteString("\nReturn the JSON respellings now.")
	return b.String()
}

// PronunciationInput is one term to generate respelling for.
type PronunciationInput struct {
	Russian string // the Russian text as it appears in translation
	Source  string // the English source (for context, not pronounced)
	Type    string // term type: character, place, organization, title, term
}
