package tts

import (
	"fmt"
	"sort"
	"strings"

	"ebook-reader/internal/project"
)

// Stress is the persistent stress marks store. It maps Russian terms to their
// stressed forms using the Silero convention: a '+' before the stressed vowel
// (e.g., "кедров" → "к+едров"). The store is used by the ssml command to
// apply stress marks to chapter text before wrapping it in SSML tags.
type Stress struct {
	Entries []StressEntry `json:"entries"`
}

// StressEntry is one term with its stressed form for Silero TTS.
type StressEntry struct {
	// Term is the Russian text as it appears in the translation (e.g.
	// "кедров" or "договор").
	Term string `json:"term"`

	// Stressed is the stressed form with '+' before the stressed vowel
	// (e.g., "к+едров" or "догов+ор").
	Stressed string `json:"stressed"`

	// Approved indicates that the user has manually confirmed this stressed
	// form during conflict resolution. Approved entries are not re-asked on
	// future runs — if a new chapter produces a different stressed form for
	// the same term, the approved form is kept silently.
	Approved bool `json:"approved,omitempty"`

	// Chapters lists the chapter IDs where this term's stress was extracted
	// or where it appears. Used by the chapters command to show per-chapter
	// stress counts and by Apply to filter relevant entries.
	Chapters []int `json:"chapters,omitempty"`
}

// StressConflict represents a term where different chapters produced
// different stressed forms.
type StressConflict struct {
	Term     string   `json:"term"`
	Variants []string `json:"variants"`
}

// LoadStress reads the global ai/stress.json from the project's AI directory.
// Returns an empty store if the file does not exist (stress marks are optional).
func LoadStress(aiDir string) (*Stress, error) {
	path := aiDir + "/stress.json"
	if !project.Exists(path) {
		return &Stress{}, nil
	}
	var s Stress
	if err := project.LoadJSON(path, &s); err != nil {
		return nil, fmt.Errorf("load stress: %w", err)
	}
	return &s, nil
}

// Save writes the global stress vocabulary to ai/stress.json, sorted by term
// length (longest first) so that multi-word terms are matched before their
// sub-terms during text replacement.
func (s *Stress) Save(aiDir string) error {
	s.Sort()
	path := aiDir + "/stress.json"
	return project.SaveJSON(path, s)
}

// Sort orders entries by term length (longest first) so that multi-word terms
// are matched before their sub-terms during text replacement.
func (s *Stress) Sort() {
	sort.SliceStable(s.Entries, func(i, j int) bool {
		return len(s.Entries[i].Term) > len(s.Entries[j].Term)
	})
}

// Lookup finds a stress entry by term (case-insensitive). Returns the entry
// and true if found.
func (s *Stress) Lookup(term string) (StressEntry, bool) {
	lower := strings.ToLower(term)
	for _, e := range s.Entries {
		if strings.ToLower(e.Term) == lower {
			return e, true
		}
	}
	return StressEntry{}, false
}

// Apply replaces all occurrences of each term in the text with its stressed
// version. Matching is case-insensitive and word-boundary aware. Longer terms
// are replaced first (entries should be sorted by Sort, which puts longest
// first) to avoid partial matches on multi-word terms.
func (s *Stress) Apply(text string) string {
	if len(s.Entries) == 0 {
		return text
	}
	for _, e := range s.Entries {
		if e.Term == "" || e.Stressed == "" || e.Term == e.Stressed {
			continue
		}
		text = replaceWordIgnoreCase(text, e.Term, e.Stressed)
	}
	return text
}

// MergeChapter merges other into s, tagging all entries with the given
// chapter ID. If a term already exists in s, the chapter ID is added to
// its Chapters list (if not already present). Conflict handling is the
// same as Merge — approved entries silently keep their form.
func (s *Stress) MergeChapter(other *Stress, chapterID int) []StressConflict {
	// Tag all entries from other with the chapter ID.
	for i := range other.Entries {
		other.Entries[i].Chapters = addChapterID(other.Entries[i].Chapters, chapterID)
	}
	conflicts := s.Merge(other)
	// For existing entries that were matched (not newly added), ensure the
	// chapter ID is recorded. Merge only adds new entries — it doesn't
	// update Chapters on existing ones. We do a second pass.
	for i := range other.Entries {
		for j := range s.Entries {
			if strings.EqualFold(s.Entries[j].Term, other.Entries[i].Term) {
				s.Entries[j].Chapters = addChapterID(s.Entries[j].Chapters, chapterID)
				break
			}
		}
	}
	return conflicts
}

// addChapterID appends chapterID to s if not already present.
func addChapterID(s []int, chapterID int) []int {
	for _, v := range s {
		if v == chapterID {
			return s
		}
	}
	return append(s, chapterID)
}

// Merge merges other into s. If a term exists in both with the same stressed
// form, it's kept once. If a term exists in both with different stressed
// forms, the entry from s is kept and a StressConflict is returned — UNLESS
// the existing entry is Approved, in which case the conflict is silently
// resolved (the approved form is kept, no conflict reported). New terms
// from other are appended.
func (s *Stress) Merge(other *Stress) []StressConflict {
	var conflicts []StressConflict
	conflictMap := make(map[string]bool) // track terms already in conflicts

	for _, oe := range other.Entries {
		if oe.Term == "" || oe.Stressed == "" {
			continue
		}
		found := false
		for i := range s.Entries {
			if strings.EqualFold(s.Entries[i].Term, oe.Term) {
				found = true
				if !strings.EqualFold(s.Entries[i].Stressed, oe.Stressed) {
					// If the existing entry is approved, silently keep it.
					// Don't report a conflict — the user already decided.
					if s.Entries[i].Approved {
						break
					}
					if !conflictMap[strings.ToLower(oe.Term)] {
						conflicts = append(conflicts, StressConflict{
							Term:     oe.Term,
							Variants: uniqueVariants(s.Entries[i].Stressed, oe.Stressed),
						})
						conflictMap[strings.ToLower(oe.Term)] = true
					}
				}
				break
			}
		}
		if !found {
			s.Entries = append(s.Entries, oe)
		}
	}

	return conflicts
}

// MergeAll merges multiple per-chapter stress stores into a single global
// vocabulary. Returns the merged store and a list of conflicts for terms with
// disagreeing stress marks across chapters.
func MergeAll(chapters []*Stress) (merged *Stress, conflicts []StressConflict) {
	merged = &Stress{}
	for _, ch := range chapters {
		c := merged.Merge(ch)
		conflicts = append(conflicts, c...)
	}
	merged.Sort()
	return merged, conflicts
}

// uniqueVariants returns the unique stressed forms from the given list,
// preserving order.
func uniqueVariants(forms ...string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, f := range forms {
		if f == "" {
			continue
		}
		lower := strings.ToLower(f)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, f)
		}
	}
	return result
}

// ResolveConflict updates the stressed form for a term in the store. If the
// term doesn't exist, it's added. If stressed is empty, the entry is removed.
// The resolved entry is marked as Approved so future runs won't re-ask the
// user for the same term.
func (s *Stress) ResolveConflict(term, stressed string) {
	for i := range s.Entries {
		if strings.EqualFold(s.Entries[i].Term, term) {
			if stressed == "" {
				s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
			} else {
				s.Entries[i].Stressed = stressed
				s.Entries[i].Approved = true
			}
			return
		}
	}
	if stressed != "" {
		s.Entries = append(s.Entries, StressEntry{
			Term:     term,
			Stressed: stressed,
			Approved: true,
		})
	}
}

// CountForChapter returns the number of entries tagged with the given
// chapter ID.
func (s *Stress) CountForChapter(chapterID int) int {
	n := 0
	for _, e := range s.Entries {
		for _, c := range e.Chapters {
			if c == chapterID {
				n++
				break
			}
		}
	}
	return n
}
