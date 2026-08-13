package translation

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"ebook-reader/internal/project"
)

// Glossary term type constants used in GlossaryTerm.Type and the prompt
// formatting typeOrder slice.
const (
	typeCharacter    = "character"
	typeOrganization = "organization"
	TypeTerm         = "term"
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

// Save writes characters to ai/characters.json, sorted by name.
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

// glossaryTermForPrompt is a projection of GlossaryTerm that omits internal
// bookkeeping fields (Chapters, FirstSeenChapter) when serializing for AI
// prompts. This saves tokens — the model only needs source/target/type.
type glossaryTermForPrompt struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// MarshalForPrompt serializes the characters as JSON without the internal
// Chapters field. Used for AI merge prompts where chapter tracking is
// meaningless and wastes tokens.
func (g *Glossary) MarshalForPrompt() (string, error) {
	projected := make([]glossaryTermForPrompt, len(g.Terms))
	for i, t := range g.Terms {
		projected[i] = glossaryTermForPrompt{
			Source: t.Source,
			Target: t.Target,
			Type:   t.Type,
		}
	}
	data, err := json.Marshal(projected)
	if err != nil {
		return "", fmt.Errorf("marshal glossary for prompt: %w", err)
	}
	return string(data), nil
}

// Find looks up a character by name (case-insensitive). Returns the character
// and true if found.
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
			Type:   typeCharacter,
		})
	}
	// Add non-character terms, skipping any that duplicate a character entry.
	for _, t := range g.Terms {
		if t.Type == typeCharacter {
			continue // characters come from chars, not glossary
		}
		if _, exists := merged.Find(t.Source); exists {
			continue
		}
		merged.Terms = append(merged.Terms, t)
	}
	return merged
}

// Merge adds or updates characters from newChars. If a character with the same
// name (case-insensitive) already exists, its fields are updated only if the
// new values are more complete (non-empty where existing is empty). New
// characters are appended. Returns the number of characters actually added.
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
// the chapter ID. Terms with empty Chapters are excluded — they were not
// tagged to any chapter and would only add noise/token cost. Terms are
// grouped by type.
func (g *Glossary) PromptBlockForChapter(chapterID int) string {
	var filtered []GlossaryTerm
	for _, t := range g.Terms {
		if containsInt(t.Chapters, chapterID) {
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
	typeOrder := []string{typeCharacter, "place", typeOrganization, "title", TypeTerm}
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
		known := slices.Contains(typeOrder, ty)
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
	return slices.Contains(s, v)
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

// characterForPrompt is a projection of Character that omits the internal
// Chapters field when serializing for AI prompts.
type characterForPrompt struct {
	Name        string `json:"name"`
	Translation string `json:"translation"`
	Role        string `json:"role,omitempty"`
	Description string `json:"description,omitempty"`
}

// MarshalForPrompt serializes the characters as JSON without the internal
// Chapters field. Used for AI merge prompts where chapter tracking is
// meaningless and wastes tokens.
func (c *Characters) MarshalForPrompt() (string, error) {
	projected := make([]characterForPrompt, len(c.Characters))
	for i, ch := range c.Characters {
		projected[i] = characterForPrompt{
			Name:        ch.Name,
			Translation: ch.Translation,
			Role:        ch.Role,
			Description: ch.Description,
		}
	}
	data, err := json.Marshal(projected)
	if err != nil {
		return "", fmt.Errorf("marshal characters for prompt: %w", err)
	}
	return string(data), nil
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
