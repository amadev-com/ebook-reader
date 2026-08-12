package translation //nolint:testpackage // needs access to unexported type constants

import (
	"path/filepath"
	"testing"
)

func TestGlossarySaveAndLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "The Order", Target: "Орден", Type: typeOrganization, FirstSeenChapter: 1},
			{Source: "Dark Lord", Target: "Темный Лорд", Type: "title"},
		},
	}
	if err := g.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadGlossary(dir)
	if err != nil {
		t.Fatalf("LoadGlossary: %v", err)
	}
	if len(loaded.Terms) != 2 {
		t.Fatalf("loaded terms = %d, want 2", len(loaded.Terms))
	}
	// Save sorts by source; "Dark Lord" < "The Order".
	if loaded.Terms[0].Source != "Dark Lord" {
		t.Errorf("first term = %q, want Dark Lord", loaded.Terms[0].Source)
	}
	if loaded.Terms[1].Source != "The Order" {
		t.Errorf("second term = %q, want The Order", loaded.Terms[1].Source)
	}
}

func TestLoadGlossaryMissingFile(t *testing.T) {
	t.Parallel()
	g, err := LoadGlossary(t.TempDir())
	if err != nil {
		t.Fatalf("LoadGlossary on missing file: %v", err)
	}
	if len(g.Terms) != 0 {
		t.Errorf("terms = %d, want 0 for missing file", len(g.Terms))
	}
}

func TestGlossaryFind(t *testing.T) {
	t.Parallel()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "The Order", Target: "Орден", Type: typeOrganization},
		},
	}
	// Case-insensitive find.
	term, ok := g.Find("the order")
	if !ok {
		t.Fatal("Find(the order) = false, want true")
	}
	if term.Target != "Орден" {
		t.Errorf("Target = %q, want Орден", term.Target)
	}
	_, ok = g.Find("nonexistent")
	if ok {
		t.Error("Find(nonexistent) = true, want false")
	}
}

func TestGlossaryMerge(t *testing.T) {
	t.Parallel()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "The Order", Target: "Орден", Type: typeOrganization},
		},
	}
	added := g.Merge([]GlossaryTerm{
		{Source: "The Order", Target: "Порядок", Type: typeOrganization}, // duplicate, ignored
		{Source: "Quinn", Target: "Куинн", Type: typeCharacter},          // new
		{Source: "", Target: "empty", Type: "term"},                      // empty source, skipped
		{Source: "empty target", Target: "", Type: "term"},               // empty target, skipped
	})
	if added != 1 {
		t.Errorf("Merge added = %d, want 1", added)
	}
	if len(g.Terms) != 2 {
		t.Errorf("terms after merge = %d, want 2", len(g.Terms))
	}
	// Existing term should NOT be overwritten.
	term, _ := g.Find("The Order")
	if term.Target != "Орден" {
		t.Errorf("existing term target = %q, want Орден (not overwritten)", term.Target)
	}
}

func TestGlossaryPromptBlock(t *testing.T) {
	t.Parallel()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "Quinn", Target: "Куинн", Type: typeCharacter},
			{Source: "The Order", Target: "Орден", Type: typeOrganization},
			{Source: "Dalki", Target: "Далки", Type: "term"},
		},
	}
	block := g.PromptBlock()
	if block == "" {
		t.Fatal("PromptBlock() = empty, want non-empty")
	}
	if !contains(block, "Quinn = Куинн") {
		t.Errorf("PromptBlock missing character entry: %s", block)
	}
	if !contains(block, "[character]") {
		t.Errorf("PromptBlock missing [character] header")
	}
	if !contains(block, "[organization]") {
		t.Errorf("PromptBlock missing [organization] header")
	}
	if !contains(block, "[term]") {
		t.Errorf("PromptBlock missing [term] header")
	}
}

func TestGlossaryPromptBlockEmpty(t *testing.T) {
	t.Parallel()
	g := &Glossary{}
	if g.PromptBlock() != "" {
		t.Errorf("empty glossary PromptBlock = %q, want empty", g.PromptBlock())
	}
}

func TestCharactersSaveAndLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := &Characters{
		Characters: []Character{
			{Name: "Quinn", Translation: "Куинн", Role: "protagonist"},
			{Name: "Mona", Translation: "Мона", Role: "supporting"},
		},
	}
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadCharacters(dir)
	if err != nil {
		t.Fatalf("LoadCharacters: %v", err)
	}
	if len(loaded.Characters) != 2 {
		t.Fatalf("loaded = %d, want 2", len(loaded.Characters))
	}
	// Sorted by name: Mona < Quinn.
	if loaded.Characters[0].Name != "Mona" {
		t.Errorf("first = %q, want Mona", loaded.Characters[0].Name)
	}
}

func TestLoadCharactersMissingFile(t *testing.T) {
	t.Parallel()
	c, err := LoadCharacters(t.TempDir())
	if err != nil {
		t.Fatalf("LoadCharacters on missing: %v", err)
	}
	if len(c.Characters) != 0 {
		t.Errorf("characters = %d, want 0", len(c.Characters))
	}
}

func TestGlossarySort(t *testing.T) {
	t.Parallel()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "zebra"},
			{Source: "Apple"},
			{Source: "banana"},
		},
	}
	g.Sort()
	// Case-insensitive sort: Apple < banana < zebra.
	if g.Terms[0].Source != "Apple" || g.Terms[1].Source != "banana" || g.Terms[2].Source != "zebra" {
		t.Errorf("sort order = %v, %v, %v", g.Terms[0].Source, g.Terms[1].Source, g.Terms[2].Source)
	}
}

func TestGlossaryWithCharacters(t *testing.T) {
	t.Parallel()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "The Order", Target: "Орден", Type: typeOrganization},
			{Source: "Dalki", Target: "Далки", Type: "term"},
			// Stale character entry in glossary — should be dropped in favor of characters.json.
			{Source: "Quinn", Target: "OLD", Type: typeCharacter},
		},
	}
	chars := &Characters{
		Characters: []Character{
			{Name: "Quinn", Translation: "Куинн"},
			{Name: "Mona", Translation: "Мона"},
		},
	}
	merged := g.WithCharacters(chars)

	// Original glossary is not modified.
	if len(g.Terms) != 3 {
		t.Errorf("original glossary modified: %d terms, want 3", len(g.Terms))
	}

	// Merged has 4 terms: 2 characters + 2 non-character terms.
	if len(merged.Terms) != 4 {
		t.Fatalf("merged terms = %d, want 4", len(merged.Terms))
	}

	// Character from characters.json wins over stale glossary entry.
	term, ok := merged.Find("Quinn")
	if !ok {
		t.Fatal("Find(Quinn) not found in merged")
	}
	if term.Target != "Куинн" {
		t.Errorf("Quinn target = %q, want Куинн (from characters.json, not glossary)", term.Target)
	}

	// Non-character terms preserved.
	if _, ok = merged.Find("The Order"); !ok {
		t.Error("The Order missing from merged")
	}
	if _, ok = merged.Find("Dalki"); !ok {
		t.Error("Dalki missing from merged")
	}
}

// Ensure filepath is used (for potential path joins in future tests).
var _ = filepath.Join

func TestCharactersFind(t *testing.T) {
	t.Parallel()
	c := &Characters{
		Characters: []Character{
			{Name: "Quinn", Translation: "Куинн"},
			{Name: "Mona", Translation: "Мона"},
		},
	}
	if _, ok := c.Find("quinn"); !ok {
		t.Error("Find should be case-insensitive")
	}
	if _, ok := c.Find("Nonexistent"); ok {
		t.Error("Find should return false for nonexistent")
	}
}

func TestCharactersMerge_NewCharacters(t *testing.T) {
	t.Parallel()
	c := &Characters{
		Characters: []Character{
			{Name: "Quinn", Translation: "Куинн", Role: "protagonist"},
		},
	}
	added := c.Merge([]Character{
		{Name: "Mona", Translation: "Мона"},
		{Name: "Jack", Translation: "Джек"},
	})
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if len(c.Characters) != 3 {
		t.Errorf("total characters = %d, want 3", len(c.Characters))
	}
}

func TestCharactersMerge_UpdateExisting(t *testing.T) {
	t.Parallel()
	c := &Characters{
		Characters: []Character{
			{Name: "Quinn", Translation: "", Role: "", Description: ""},
		},
	}
	added := c.Merge([]Character{
		{Name: "Quinn", Translation: "Куинн", Role: "protagonist", Description: "The main character"},
	})
	if added != 0 {
		t.Errorf("added = %d, want 0 (existing was updated)", added)
	}
	if c.Characters[0].Translation != "Куинн" {
		t.Errorf("translation not updated: %q", c.Characters[0].Translation)
	}
	if c.Characters[0].Role != "protagonist" {
		t.Errorf("role not updated: %q", c.Characters[0].Role)
	}
	if c.Characters[0].Description != "The main character" {
		t.Errorf("description not updated: %q", c.Characters[0].Description)
	}
}

func TestCharactersMerge_DoesNotOverwriteExisting(t *testing.T) {
	t.Parallel()
	c := &Characters{
		Characters: []Character{
			{Name: "Quinn", Translation: "Куинн", Role: "protagonist", Description: "Original desc"},
		},
	}
	added := c.Merge([]Character{
		{Name: "Quinn", Translation: "NEW", Role: "supporting", Description: "New desc"},
	})
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
	// Existing values should NOT be overwritten.
	if c.Characters[0].Translation != "Куинн" {
		t.Errorf("translation overwritten: %q", c.Characters[0].Translation)
	}
	if c.Characters[0].Role != "protagonist" {
		t.Errorf("role overwritten: %q", c.Characters[0].Role)
	}
}

func TestCharactersMerge_EmptyName(t *testing.T) {
	t.Parallel()
	c := &Characters{}
	added := c.Merge([]Character{
		{Name: "", Translation: "test"},
	})
	if added != 0 {
		t.Errorf("added = %d, want 0 for empty name", added)
	}
}

func TestPromptBlockForChapter(t *testing.T) {
	t.Parallel()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "Guild", Target: "Гильдия", Type: typeOrganization, Chapters: []int{1, 5, 10}},
			{Source: "Quinn", Target: "Куинн", Type: typeCharacter, Chapters: []int{1, 2, 3}},
			{Source: "Dalki", Target: "Далки", Type: "term", Chapters: []int{5, 6}},
			{Source: "Untagged", Target: "Безметочный", Type: "term"}, // no chapter tags — excluded
		},
	}

	// Chapter 1: Guild + Quinn (terms tagged with chapter 1).
	block := g.PromptBlockForChapter(1)
	if !contains(block, "Guild") {
		t.Error("chapter 1 missing Guild")
	}
	if !contains(block, "Quinn") {
		t.Error("chapter 1 missing Quinn")
	}
	if contains(block, "Dalki") {
		t.Error("chapter 1 should NOT include Dalki")
	}
	if contains(block, "Untagged") {
		t.Error("chapter 1 should NOT include Untagged (no chapter tags)")
	}

	// Chapter 5: Guild + Dalki.
	block5 := g.PromptBlockForChapter(5)
	if !contains(block5, "Guild") {
		t.Error("chapter 5 missing Guild")
	}
	if !contains(block5, "Dalki") {
		t.Error("chapter 5 missing Dalki")
	}
	if contains(block5, "Quinn") {
		t.Error("chapter 5 should NOT include Quinn")
	}
	if contains(block5, "Untagged") {
		t.Error("chapter 5 should NOT include Untagged")
	}

	// Chapter 100: no terms (no chapter-specific matches).
	block100 := g.PromptBlockForChapter(100)
	if contains(block100, "Guild") {
		t.Error("chapter 100 should NOT include Guild")
	}
	if contains(block100, "Quinn") {
		t.Error("chapter 100 should NOT include Quinn")
	}
	if contains(block100, "Untagged") {
		t.Error("chapter 100 should NOT include Untagged")
	}
	if block100 != "" {
		t.Errorf("chapter 100 should produce empty block, got %q", block100)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
