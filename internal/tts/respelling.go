package tts

import (
	"fmt"
	"sort"
	"strings"

	"ebook-reader/internal/project"
)

// Respelling is the persistent respelling store (ai/respelling.json). It maps
// Russian terms (typically names and borrowed words) to phonetic respellings
// that XTTS v2 will pronounce correctly. Unlike IPA phonemes, respellings are
// plain text replacements — the term is swapped for its respelled version
// before the text is sent to the TTS engine.
type Respelling struct {
	Entries []RespellingEntry `json:"entries"`
}

// RespellingEntry is one term with its phonetic respelling for XTTS v2.
type RespellingEntry struct {
	// Term is the Russian text as it appears in the translation (e.g.
	// "Куинн" or "Орден").
	Term string `json:"term"`

	// Respelled is the phonetic respelling that XTTS v2 will pronounce
	// correctly. This is plain Russian text with XTTS-specific tricks:
	// vowel doubling for stress, ё→йо, de-capitalization, etc.
	// Example: "Куинн" → "КУинн" (stress on first syllable).
	Respelled string `json:"respelled"`
}

// LoadRespelling reads ai/respelling.json from the project's AI directory.
// Returns an empty store if the file does not exist (respelling is optional).
func LoadRespelling(aiDir string) (*Respelling, error) {
	path := aiDir + "/respelling.json"
	if !project.Exists(path) {
		return &Respelling{}, nil
	}
	var r Respelling
	if err := project.LoadJSON(path, &r); err != nil {
		return nil, fmt.Errorf("load respelling: %w", err)
	}
	return &r, nil
}

// LoadChapterRespelling reads ai/respelling_NNN.json for a specific chapter.
// Returns an empty store if the file does not exist.
func LoadChapterRespelling(aiDir string, chapterID int) (*Respelling, error) {
	path := fmt.Sprintf("%s/respelling_%03d.json", aiDir, chapterID)
	if !project.Exists(path) {
		return &Respelling{}, nil
	}
	var r Respelling
	if err := project.LoadJSON(path, &r); err != nil {
		return nil, fmt.Errorf("load chapter respelling: %w", err)
	}
	return &r, nil
}

// SaveChapterRespelling writes respelling entries to ai/respelling_NNN.json
// for a specific chapter, sorted by term length (longest first).
func (r *Respelling) SaveChapterRespelling(aiDir string, chapterID int) error {
	r.Sort()
	path := fmt.Sprintf("%s/respelling_%03d.json", aiDir, chapterID)
	return project.SaveJSON(path, r)
}

// Sort orders entries by term length (longest first) so that multi-word terms
// are matched before their sub-terms during text replacement.
func (r *Respelling) Sort() {
	sort.SliceStable(r.Entries, func(i, j int) bool {
		return len(r.Entries[i].Term) > len(r.Entries[j].Term)
	})
}

// Lookup finds a respelling entry by term (case-insensitive). Returns the
// entry and true if found.
func (r *Respelling) Lookup(term string) (RespellingEntry, bool) {
	lower := strings.ToLower(term)
	for _, e := range r.Entries {
		if strings.ToLower(e.Term) == lower {
			return e, true
		}
	}
	return RespellingEntry{}, false
}

// Apply replaces all occurrences of each term in the text with its respelled
// version. Matching is case-insensitive and word-boundary aware. Longer terms
// are replaced first (entries should be sorted by Sort, which puts longest
// first) to avoid partial matches on multi-word terms.
func (r *Respelling) Apply(text string) string {
	if len(r.Entries) == 0 {
		return text
	}
	for _, e := range r.Entries {
		if e.Term == "" || e.Respelled == "" || e.Term == e.Respelled {
			continue
		}
		text = replaceWordIgnoreCase(text, e.Term, e.Respelled)
	}
	return text
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
