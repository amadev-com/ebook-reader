package translation

import (
	"fmt"
	"strings"

	"ebook-reader/internal/config"
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
- The summary must be in English, specific about names and terms so the next chapter's translation stays consistent.
- All Russian translations (target fields) must be in Cyrillic only — no Latin characters. Abbreviations and acronyms must be TRANSLATED to their Russian meaning, not transliterated letter-by-letter. For example: "MVS" (My Vampire System) → "Моя Система Вампира", "VR" → "виртуальная реальность", "AI" → "искусственный интеллект", "HP" → "здоровье", "NPC" → "неигровой персонаж". If an abbreviation is a proper name with no Russian equivalent, use transliteration with Cyrillic letters only.`

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
// System builds the translation system prompt with optional glossary terms and previous-chapter context.
// glossaryBlock contains glossary instructions to append to the prompt.
// prevContext contains context from preceding chapters to append to the prompt.
// The returned prompt instructs the translator to produce natural Russian while preserving required terminology and formatting.
func System(glossaryBlock, prevContext string) string {
	var b strings.Builder
	b.WriteString(
		`You are a professional literary translator translating an English book into Russian. Your translation must be natural, fluent Russian that reads as if originally written in that language — not a word-by-word rendering.

Rules:
- Preserve the author's tone, style, and narrative voice.
- Translate all prose into Russian. Do not leave English text in the output.
- Use the glossary consistently: every glossary term must be translated exactly as specified.
- Keep paragraph breaks exactly as in the source (separated by blank lines).
- Do not add commentary, notes, or explanations. Output ONLY the translated text.
- If you discover a new recurring term not in the glossary, translate it consistently within this chapter. The system will extract new terms separately.
- Write ALL numbers as words in Russian, with correct gender and case agreement — including numbers in chapter titles. For example: "Глава 384" → "Глава триста восемьдесят четыре", "5 лет" → "пять лет", "5-го этажа" → "пятого этажа", "300 человек" → "триста человек", "2 раза" → "два раза". Never leave digits in the translated text — the text-to-speech engine cannot read numbers.
- NEVER leave Latin characters in the output. All text must be in Cyrillic. Abbreviations and acronyms must be TRANSLATED to their Russian meaning, not transliterated letter-by-letter. For example: "MVS" → "МВС" is WRONG (transliteration); "VR" → "ВР" is WRONG. Instead translate the meaning: "MVS" (My Vampire System) → "Моя Система Вампира", "VR" (Virtual Reality) → "виртуальная реальность", "AI" → "искусственный интеллект", "HP" → "здоровье", "NPC" → "неигровой персонаж". If an abbreviation is a proper name with no Russian equivalent, use transliteration but with Cyrillic letters only.`,
	)
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

// User builds the user prompt for translating one chapter. The title is not
// included in the prompt header because it is already embedded in the source
// text (chapter source starts with the title line). Including it twice would
// User builds a translation prompt for a chapter, including the chapter number and source text.
func User(ch ChapterInfo, source string) string {
	return fmt.Sprintf(
		"Translate chapter %d. The source text begins with the chapter title — translate it as part of the text.\n\n%s",
		ch.ID,
		source,
	)
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
	return fmt.Sprintf(
		"Source (English):\n\n%s\n\nTranslation (Russian):\n\n%s\n\nExtract new glossary terms as JSON.",
		source,
		translation,
	)
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
5. All Russian translations must be in Cyrillic only — no Latin characters. Abbreviations and acronyms must be TRANSLATED to their Russian meaning, not transliterated letter-by-letter. For example: "MVS" → "Моя Система Вампира", "VR" → "виртуальная реальность", "AI" → "искусственный интеллект". If an abbreviation is a proper name with no Russian equivalent, use transliteration with Cyrillic letters only.

Return a JSON object with this exact shape:
{
  "characters": [{"name": "...", "translation": "...", "role": "...", "description": "..."}],
  "terms": [{"source": "...", "target": "...", "type": "..."}]
}

The "type" field for terms must be one of: "place", "organization", "title", "term".
The "role" field for characters should be one of: "protagonist", "antagonist", "supporting" (or empty if unclear).`

// GlossaryMergeUser builds a prompt that merges per-chapter extraction results with existing vocabulary.
// Locked translations are supplied as mandatory mappings, and existing characters and glossary terms are
// retained when appropriate. It returns the resulting merge prompt.
func GlossaryMergeUser(
	perChapterResults string,
	lockedTerms string,
	existingGlossary string,
	existingCharacters string,
) string {
	var b strings.Builder
	b.WriteString(
		"Merge and unify these per-chapter glossary extraction results into a single clean glossary + character list.\n\n",
	)
	if lockedTerms != "" {
		b.WriteString("LOCKED TRANSLATIONS — you MUST use these exact translations for the matching terms. ")
		b.WriteString("Do not change them:\n\n")
		b.WriteString(lockedTerms)
		b.WriteString("\n\n")
	}
	if existingGlossary != "" || existingCharacters != "" {
		b.WriteString("EXISTING VOCABULARY — merge the new per-chapter results with these existing entries. ")
		b.WriteString(
			"Keep existing entries that are not contradicted by new results, and add new entries from the per-chapter results. ",
		)
		b.WriteString("If a term or character appears in both, prefer the more complete description/translation.\n\n")
		if existingCharacters != "" {
			b.WriteString("Existing characters (JSON):\n")
			b.WriteString(existingCharacters)
			b.WriteString("\n\n")
		}
		if existingGlossary != "" {
			b.WriteString("Existing glossary terms (JSON):\n")
			b.WriteString(existingGlossary)
			b.WriteString("\n\n")
		}
	}
	b.WriteString("Per-chapter extraction results (JSON array, one object per chapter):\n\n")
	b.WriteString(perChapterResults)
	b.WriteString(
		"\n\nReturn the merged and unified JSON now. Remember: characters = ONLY real persons, terms = everything else WITHOUT characters.",
	)
	return b.String()
}

// --- Stress marks prompts (bookai pronounce) ---

// StressSystem is the system prompt for generating stress marks for Russian
// text for the Silero text-to-speech engine. Silero natively supports stress
// marks: a '+' before the stressed vowel (e.g., "к+едров" = stress on "е").
// This is much cleaner than respelling — the word stays intact, only the
// stress position is annotated.
const StressSystem = `You are a Russian phonetics expert. Your task is to scan a chapter of Russian text and identify words with non-obvious or ambiguous stress, then provide the stressed form using the Silero TTS convention: place a '+' before the stressed vowel.

Stress mark convention:
- The '+' goes IMMEDIATELY BEFORE the stressed vowel in the word.
- The '+' MUST be placed before a VOWEL (а, е, ё, и, о, у, ы, э, ю, я) — NEVER before a consonant, soft sign (ь), hard sign (ъ), or any other non-vowel character.
- Use ONLY the ASCII plus character '+' to mark stress. NEVER use Unicode combining stress marks (U+0301, U+0300, etc.) or precomposed stressed letters (like и́, е́, о́, а́, у́, ы́, э́, ю́, я́). The "stressed" field must contain ONLY plain Cyrillic letters and the '+' character — no diacritics, no combining marks, no special Unicode characters.
- The "stressed" field MUST contain exactly one '+' character, placed before the stressed vowel. If the word does not need a stress mark (obvious stress), do not include it at all.
- кедров → к+едров (stress on "е")
- договор → догов+ор (stress on second "о")
- звонит → зв+онит (stress on "о")
- красивее → крас+ивее (stress on "и")

Rules:
- Scan the chapter text and identify words where stress is non-obvious or commonly mispronounced: transliterated foreign names, invented/fantasy terms, words with multiple possible stress positions, and words where wrong stress changes meaning.
- For each problematic word, provide the exact term as it appears in the text and the stressed form with '+' before the stressed vowel.
- ONLY include words where the stressed form is DIFFERENT from the original (i.e., it has a '+' mark). If the word has obvious stress, omit it.
- Skip common Russian words with unambiguous stress (e.g., "мама", "дом", "кот").
- Do NOT change the spelling of the word — only insert '+' before the stressed vowel.
- The "term" field must match the word exactly as it appears in the chapter text (case-sensitive), so it can be found and replaced.
- The "stressed" field must be the same word with a '+' inserted before the stressed vowel.
- NEVER place '+' before a consonant or soft/hard sign. Examples of WRONG output: "М+С", "фамил+ьяр", "Деся+той". These crash the TTS engine.
- WRONG: "stressed": "иску́сственный" (Unicode stress mark). RIGHT: "stressed": "иску́сственный" is WRONG — use "stressed": "искус+ственный" instead.
- WRONG: "stressed": "обстоятельств" (no '+' at all — this is identical to the term and useless). If the word doesn't need a stress mark, don't include it.

Return a JSON object with this exact shape:
{
  "entries": [{"term": "exact word as in text", "stressed": "word with + before stressed vowel"}]
}

If no words need stress marks, return {"entries": []}.`

// StressUser builds the user prompt for a single chapter. It sends the full
// chapter text so the model can scan it for words with non-obvious stress.
// Config overrides are included so the model respects user-specified stress
// StressUser builds a prompt for identifying ambiguous Russian word stress in chapter text,
// including any mandatory pronunciation overrides.
func StressUser(chapterText string, overrides []config.PronunciationOverride) string {
	var b strings.Builder
	b.WriteString(
		"Scan this Russian chapter text and identify words with non-obvious or ambiguous stress. Provide the stressed form with '+' before the stressed vowel for each problematic word.\n\n",
	)

	if len(overrides) > 0 {
		b.WriteString(
			"The following stress marks are mandatory (already defined by the user — include them in your output):\n",
		)
		for _, ov := range overrides {
			fmt.Fprintf(&b, "- %s → %s\n", ov.Term, ov.Phonemes)
		}
		b.WriteString("\n")
	}

	b.WriteString("Chapter text:\n\n")
	b.WriteString(chapterText)
	b.WriteString("\n\nReturn the JSON stress marks now.")
	return b.String()
}

// PronunciationInput is one term to generate respelling for (used by tests).
type PronunciationInput struct {
	Russian string // the Russian text as it appears in translation
	Source  string // the English source (for context, not pronounced)
	Type    string // term type: character, place, organization, title, term
}
