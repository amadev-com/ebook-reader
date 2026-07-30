package translation

import (
	"path/filepath"
	"testing"
)

func TestGlossarySaveAndLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &Glossary{
		Terms: []GlossaryTerm{
			{Source: "The Order", Target: "Орден", Type: "organization", FirstSeenChapter: 1},
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
			{Source: "The Order", Target: "Орден", Type: "organization"},
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
			{Source: "The Order", Target: "Орден", Type: "organization"},
		},
	}
	added := g.Merge([]GlossaryTerm{
		{Source: "The Order", Target: "Порядок", Type: "organization"}, // duplicate, ignored
		{Source: "Quinn", Target: "Куинн", Type: "character"},          // new
		{Source: "", Target: "empty", Type: "term"},                    // empty source, skipped
		{Source: "empty target", Target: "", Type: "term"},             // empty target, skipped
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
			{Source: "Quinn", Target: "Куинн", Type: "character"},
			{Source: "The Order", Target: "Орден", Type: "organization"},
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

// Ensure filepath is used (for potential path joins in future tests).
var _ = filepath.Join

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
