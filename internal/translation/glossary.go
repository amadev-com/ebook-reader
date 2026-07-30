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
	if len(g.Terms) == 0 {
		return ""
	}
	// Group by type, preserving a stable type order.
	typeOrder := []string{"character", "place", "organization", "title", "term"}
	byType := make(map[string][]GlossaryTerm)
	for _, t := range g.Terms {
		byType[t.Type] = append(byType[t.Type], t)
	}
	var b strings.Builder
	b.WriteString("=== Glossary (use these translations consistently) ===\n")
	for _, ty := range typeOrder {
		terms := byType[ty]
		if len(terms) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n[%s]\n", ty)
		for _, t := range terms {
			fmt.Fprintf(&b, "  %s = %s\n", t.Source, t.Target)
		}
	}
	// Any types not in typeOrder.
	for ty, terms := range byType {
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
		for _, t := range terms {
			fmt.Fprintf(&b, "  %s = %s\n", t.Source, t.Target)
		}
	}
	return b.String()
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
