package tts

import (
	"fmt"
	"sort"
	"strings"

	"ebook-reader/internal/project"
)

// Pronunciation is the persistent pronunciation hint store
// (ai/pronunciation.json). It maps Russian terms (typically names and
// borrowed words) to phonetic representations that TTS engines can use to
// avoid mispronunciation.
type Pronunciation struct {
	Entries []PronunciationEntry `json:"entries"`
}

// PronunciationEntry is one term with its phonetic spelling.
type PronunciationEntry struct {
	// Term is the Russian text as it appears in the translation (e.g.
	// "Орден" or a transliterated name).
	Term string `json:"term"`

	// Phonemes is the phonetic representation. The alphabet depends on the
	// engine: IPA for SSML <phoneme alphabet="ipa">, or engine-specific
	// notation.
	Phonemes string `json:"phonemes"`

	// Alphabet is the phonetic alphabet used (e.g. "ipa", "x-sampa").
	// Defaults to "ipa" if empty.
	Alphabet string `json:"alphabet,omitempty"`
}

// LoadPronunciation reads ai/pronunciation.json from the project's AI
// directory. Returns an empty store if the file does not exist (pronunciation
// hints are optional).
func LoadPronunciation(aiDir string) (*Pronunciation, error) {
	path := aiDir + "/pronunciation.json"
	if !project.Exists(path) {
		return &Pronunciation{}, nil
	}
	var p Pronunciation
	if err := project.LoadJSON(path, &p); err != nil {
		return nil, fmt.Errorf("load pronunciation: %w", err)
	}
	return &p, nil
}

// Save writes pronunciation hints to ai/pronunciation.json, sorted by term.
func (p *Pronunciation) Save(aiDir string) error {
	p.Sort()
	return project.SaveJSON(aiDir+"/pronunciation.json", p)
}

// Sort orders entries by term (case-insensitive) for stable output.
func (p *Pronunciation) Sort() {
	sort.SliceStable(p.Entries, func(i, j int) bool {
		return strings.ToLower(p.Entries[i].Term) < strings.ToLower(p.Entries[j].Term)
	})
}

// Lookup finds a pronunciation entry by term (case-insensitive). Returns the
// entry and true if found.
func (p *Pronunciation) Lookup(term string) (PronunciationEntry, bool) {
	lower := strings.ToLower(term)
	for _, e := range p.Entries {
		if strings.ToLower(e.Term) == lower {
			return e, true
		}
	}
	return PronunciationEntry{}, false
}

// PhonAlphabet returns the phonetic alphabet for the entry, defaulting to
// "ipa" if the Alphabet field is empty.
func (e PronunciationEntry) PhonAlphabet() string {
	if e.Alphabet == "" {
		return "ipa"
	}
	return e.Alphabet
}
