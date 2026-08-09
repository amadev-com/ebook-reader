package translation

import (
	"fmt"
	"sort"
	"strings"

	"ebook-reader/internal/project"
)

// Glossary is the persistent terminology store (ai/glossary.json). It maps
// source-language terms to their canonical target-language translations so
// that every chapter uses the same rendering of recurring names, places,
// organizations, and invented terms.
type Glossary struct {
	Terms []GlossaryTerm `json:"terms"`
}

// GlossaryTerm is one entry in the glossary.
type GlossaryTerm struct {
	Source           string `json:"source"` // English term
	Target           string `json:"target"` // Russian translation
	Type             string `json:"type"`   // character|place|organization|term|title
	FirstSeenChapter int    `json:"first_seen_chapter,omitempty"`
	Chapters         []int  `json:"chapters,omitempty"` // chapter IDs where this term was encountered
}

// LoadGlossary reads ai/glossary.json from the project's AI directory. If the
// file does not exist, an empty glossary is returned (the analyze stage will
// create it).
func LoadGlossary(aiDir string) (*Glossary, error) {
	path := aiDir + "/glossary.json"
	if !project.Exists(path) {
		return &Glossary{}, nil
	}
	var g Glossary
	if err := project.LoadJSON(path, &g); err != nil {
		return nil, fmt.Errorf("load glossary: %w", err)
	}
	return &g, nil
}

// Save writes the glossary to ai/glossary.json with terms sorted by source
// for stable diffs.
func (g *Glossary) Save(aiDir string) error {
	g.Sort()
	return project.SaveJSON(aiDir+"/glossary.json", g)
}

// Sort orders terms by source (case-insensitive) for stable output.
func (g *Glossary) Sort() {
	sort.SliceStable(g.Terms, func(i, j int) bool {
		return strings.ToLower(g.Terms[i].Source) < strings.ToLower(g.Terms[j].Source)
	})
}

// Find looks up a term by its source text (case-insensitive). Returns the
// term and true if found.
func (g *Glossary) Find(source string) (GlossaryTerm, bool) {
	lower := strings.ToLower(source)
	for _, t := range g.Terms {
		if strings.ToLower(t.Source) == lower {
			return t, true
		}
	}
	return GlossaryTerm{}, false
}

// WithCharacters returns a new Glossary that includes character entries
// (converted to terms with type "character") merged with the existing terms.
// Characters are stored separately in characters.json to avoid duplication;
// this helper merges them at runtime for consumers that need the complete
// term list (translate, verify-glossary, pronounce). The original glossary
// is not modified.
func (g *Glossary) WithCharacters(chars *Characters) *Glossary {
	merged := &Glossary{Terms: make([]GlossaryTerm, 0, len(g.Terms)+len(chars.Characters))}
	// Add character terms first.
	for _, c := range chars.Characters {
		if c.Name == "" || c.Translation == "" {
			continue
		}
		merged.Terms = append(merged.Terms, GlossaryTerm{
			Source: c.Name,
			Target: c.Translation,
			Type:   "character",
		})
	}
	// Add non-character terms, skipping any that duplicate a character entry.
	for _, t := range g.Terms {
		if t.Type == "character" {
			continue // characters come from chars, not glossary
		}
		if _, exists := merged.Find(t.Source); exists {
			continue
		}
		merged.Terms = append(merged.Terms, t)
	}
	return merged
}

// Merge adds or updates terms from newTerms. If a term with the same source
// (case-insensitive) already exists, its target is updated only if the
// existing target is empty. Returns the number of terms actually added.
func (g *Glossary) Merge(newTerms []GlossaryTerm) int {
	added := 0
	for _, nt := range newTerms {
		if nt.Source == "" || nt.Target == "" {
			continue
		}
		if existing, ok := g.Find(nt.Source); ok {
			// Update type if the new one is more specific and existing is empty.
			_ = existing
			continue
		}
		g.Terms = append(g.Terms, nt)
		added++
	}
	return added
}

// PromptBlock returns the glossary formatted as a text block for inclusion in
// a translation prompt. Terms are grouped by type. Returns an empty string if
// the glossary is empty.
func (g *Glossary) PromptBlock() string {
	return g.promptBlockForTerms(g.Terms)
}

// PromptBlockForChapter returns the glossary filtered to only terms relevant
// to the given chapter ID. A term is relevant if its Chapters field contains
// the chapter ID, or if its Chapters field is empty (legacy/global entries
// with no chapter tracking). Terms are grouped by type.
func (g *Glossary) PromptBlockForChapter(chapterID int) string {
	var filtered []GlossaryTerm
	for _, t := range g.Terms {
		if len(t.Chapters) == 0 || containsInt(t.Chapters, chapterID) {
			filtered = append(filtered, t)
		}
	}
	return g.promptBlockForTerms(filtered)
}

// promptBlockForTerms formats a subset of terms as a text block.
func (g *Glossary) promptBlockForTerms(terms []GlossaryTerm) string {
	if len(terms) == 0 {
		return ""
	}
	// Group by type, preserving a stable type order.
	typeOrder := []string{"character", "place", "organization", "title", "term"}
	byType := make(map[string][]GlossaryTerm)
	for _, t := range terms {
		byType[t.Type] = append(byType[t.Type], t)
	}
	var b strings.Builder
	b.WriteString("=== Glossary (use these translations consistently) ===\n")
	for _, ty := range typeOrder {
		ts := byType[ty]
		if len(ts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n[%s]\n", ty)
		for _, t := range ts {
			fmt.Fprintf(&b, "  %s = %s\n", t.Source, t.Target)
		}
	}
	// Any types not in typeOrder.
	for ty, ts := range byType {
		known := false
		for _, k := range typeOrder {
			if k == ty {
				known = true
				break
			}
		}
		if known {
			continue
		}
		fmt.Fprintf(&b, "\n[%s]\n", ty)
		for _, t := range ts {
			fmt.Fprintf(&b, "  %s = %s\n", t.Source, t.Target)
		}
	}
	return b.String()
}

// containsInt reports whether s contains v.
func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Characters is the persistent character store (ai/characters.json). It is a
// specialization of the glossary focused on people, with relationship info.
type Characters struct {
	Characters []Character `json:"characters"`
}

// Character is one person in the book.
type Character struct {
	Name        string `json:"name"`
	Translation string `json:"translation"`
	Role        string `json:"role,omitempty"` // protagonist, antagonist, supporting
	Description string `json:"description,omitempty"`
	Chapters    []int  `json:"chapters,omitempty"` // chapter IDs where this character was encountered
}

// LoadCharacters reads ai/characters.json. Returns an empty store if absent.
func LoadCharacters(aiDir string) (*Characters, error) {
	path := aiDir + "/characters.json"
	if !project.Exists(path) {
		return &Characters{}, nil
	}
	var c Characters
	if err := project.LoadJSON(path, &c); err != nil {
		return nil, fmt.Errorf("load characters: %w", err)
	}
	return &c, nil
}

// Save writes characters to ai/characters.json, sorted by name.
func (c *Characters) Save(aiDir string) error {
	sort.SliceStable(c.Characters, func(i, j int) bool {
		return strings.ToLower(c.Characters[i].Name) < strings.ToLower(c.Characters[j].Name)
	})
	return project.SaveJSON(aiDir+"/characters.json", c)
}

// Find looks up a character by name (case-insensitive). Returns the character
// and true if found.
func (c *Characters) Find(name string) (Character, bool) {
	lower := strings.ToLower(name)
	for _, ch := range c.Characters {
		if strings.ToLower(ch.Name) == lower {
			return ch, true
		}
	}
	return Character{}, false
}

// Merge adds or updates characters from newChars. If a character with the same
// name (case-insensitive) already exists, its fields are updated only if the
// new values are more complete (non-empty where existing is empty). New
// characters are appended. Returns the number of characters actually added.
func (c *Characters) Merge(newChars []Character) int {
	added := 0
	for _, nc := range newChars {
		if nc.Name == "" {
			continue
		}
		if i, ok := c.findIndex(nc.Name); ok {
			// Update fields if new is more complete.
			if c.Characters[i].Translation == "" && nc.Translation != "" {
				c.Characters[i].Translation = nc.Translation
			}
			if c.Characters[i].Role == "" && nc.Role != "" {
				c.Characters[i].Role = nc.Role
			}
			if c.Characters[i].Description == "" && nc.Description != "" {
				c.Characters[i].Description = nc.Description
			}
			continue
		}
		c.Characters = append(c.Characters, nc)
		added++
	}
	return added
}

// findIndex returns the index of a character by name (case-insensitive).
func (c *Characters) findIndex(name string) (int, bool) {
	lower := strings.ToLower(name)
	for i, ch := range c.Characters {
		if strings.ToLower(ch.Name) == lower {
			return i, true
		}
	}
	return 0, false
}
