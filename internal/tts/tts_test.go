package tts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	RegisterEngines()
	os.Exit(m.Run())
}

func TestNoopEngine_Synthesize(t *testing.T) {
	t.Parallel()
	engine, err := NewNoopEngine(EngineConfig{Engine: engineNoop})
	if err != nil {
		t.Fatalf("NewNoopEngine: %v", err)
	}
	if engine.Name() != engineNoop {
		t.Errorf("name: got %q, want %q", engine.Name(), engineNoop)
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "test.wav")

	if err = engine.Synthesize(context.Background(), "Небольшой текст для теста.", outPath); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if info.Size() < 44 { // WAV header is 44 bytes minimum
		t.Errorf("output file too small: %d bytes (WAV header alone is 44)", info.Size())
	}

	// Verify it's a valid WAV by checking the RIFF header.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data[:4]) != wavRIFF {
		t.Errorf("not a WAV file: missing RIFF header, got %q", data[:4])
	}
	if string(data[8:12]) != wavWAVE {
		t.Errorf("not a WAV file: missing WAVE, got %q", data[8:12])
	}
}

func TestNewEngine_Noop(t *testing.T) {
	t.Parallel()
	engine, err := NewEngine(EngineConfig{Engine: engineNoop})
	if err != nil {
		t.Fatalf("NewEngine noop: %v", err)
	}
	if engine.Name() != engineNoop {
		t.Errorf("engine name: got %q", engine.Name())
	}
}

func TestNewEngine_Unknown(t *testing.T) {
	t.Parallel()
	_, err := NewEngine(EngineConfig{Engine: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown engine")
	}
	if !contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention engine name: %v", err)
	}
}

func TestAvailableEngines(t *testing.T) {
	t.Parallel()
	engines := AvailableEngines()
	if !contains(engines, engineNoop) {
		t.Errorf("noop should be in available engines: %s", engines)
	}
	if !contains(engines, engineSileroHTTP) {
		t.Errorf("silero-http should be in available engines: %s", engines)
	}
}

// --- Stress tests ---

func TestLoadStress_AbsentFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	s, err := LoadStress(tmpDir)
	if err != nil {
		t.Fatalf("LoadStress on absent file: %v", err)
	}
	if s == nil || len(s.Entries) != 0 {
		t.Errorf("expected empty stress, got %+v", s)
	}
}

func TestStress_Lookup(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
		},
	}
	entry, ok := s.Lookup("Кедров") // case-insensitive
	if !ok {
		t.Fatal("case-insensitive lookup failed")
	}
	if entry.Stressed != "к+едров" {
		t.Errorf("lookup stressed: got %q", entry.Stressed)
	}

	_, ok = s.Lookup("nonexistent")
	if ok {
		t.Error("lookup of nonexistent term should return false")
	}
}

func TestStress_SaveAndLoad(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "Бета", Stressed: "Б+ета"},
			{Term: "Альфа", Stressed: "+Альфа"},
		},
	}
	if err := s.Save(tmpDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadStress(tmpDir)
	if err != nil {
		t.Fatalf("LoadStress: %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded.Entries))
	}
	// Should be sorted by term length (longest first).
	if loaded.Entries[0].Term != "Альфа" {
		t.Errorf("expected longest term first: got %q", loaded.Entries[0].Term)
	}
}

func TestStress_Apply(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
			{Term: "Ларри Стил", Stressed: "Л+арри Ст+ил"},
		},
	}
	text := "кедров было много. Ларри Стил пришёл."
	result := s.Apply(text)

	if !contains(result, "к+едров") {
		t.Errorf("stress not applied: %s", result)
	}
	if !contains(result, "Л+арри Ст+ил") {
		t.Errorf("multi-word stress not applied: %s", result)
	}
}

func TestStress_Apply_WordBoundary(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "Орден", Stressed: "+Орден"},
		},
	}
	// "Орденский" should NOT match "Орден" because of word boundary.
	text := "Орденский собор был красив."
	result := s.Apply(text)
	if contains(result, "+Орден") {
		t.Errorf("should not replace inside a longer word: %s", result)
	}
}

func TestStress_Apply_WordBoundary_CyrillicShortTerm(t *testing.T) {
	t.Parallel()
	// Regression: "ИИ" (2-char acronym) was matching inside words because
	// the byte-level word boundary check treated UTF-8 continuation bytes as
	// non-word characters. The fix uses rune-level boundary detection.
	s := &Stress{
		Entries: []StressEntry{
			{Term: "ИИ", Stressed: "И+И"},
		},
	}

	// Case 1: "ий" in the middle of a word (previous char is a letter).
	// "молний" contains "ий" which matches "ИИ" case-insensitively.
	text := "молний было много."
	result := s.Apply(text)
	if contains(result, "И+И") {
		t.Errorf("ИИ should not match inside 'молний': %s", result)
	}

	// Case 2: "ии" at the END of a word (match ends at word boundary, but
	// starts inside the word). This was the actual bug — the walk-back
	// from the match start didn't go past the current rune start, so the
	// previous character was never checked.
	// "армии", "стратегии", "молнии" all end in "ии" which matches "ИИ".
	for _, word := range []string{"армии", "стратегии", "молнии", "линии"} {
		text = word + " было много."
		result = s.Apply(text)
		if contains(result, "И+И") {
			t.Errorf("ИИ should not match at end of %q: %s", word, result)
		}
	}

	// Case 3: "ИИ" before punctuation (still a word boundary on the right,
	// but must not match if preceded by a letter).
	text2 := "в армии, стратегии и линии."
	result2 := s.Apply(text2)
	if contains(result2, "И+И") {
		t.Errorf("ИИ should not match inside words before punctuation: %s", result2)
	}

	// Case 4: standalone "ИИ" should be replaced.
	text3 := "ИИ развивается быстро."
	result3 := s.Apply(text3)
	if !contains(result3, "И+И") {
		t.Errorf("standalone ИИ should be replaced: %s", result3)
	}

	// Case 5: standalone "ИИ" before punctuation should be replaced.
	text4 := "ИИ, развивайся!"
	result4 := s.Apply(text4)
	if !contains(result4, "И+И") {
		t.Errorf("standalone ИИ before punctuation should be replaced: %s", result4)
	}
}

func TestStress_Apply_CaseInsensitive(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "орден", Stressed: "+орден"},
		},
	}
	text := "ОРДЕН был велик."
	result := s.Apply(text)
	if !contains(result, "+орден") {
		t.Errorf("case-insensitive replacement failed: %s", result)
	}
}

func TestStress_Apply_Empty(t *testing.T) {
	t.Parallel()
	s := &Stress{}
	text := "Привет мир."
	result := s.Apply(text)
	if result != text {
		t.Errorf("empty stress should not change text: got %q", result)
	}
}

func TestStress_Apply_MultipleOccurrences(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "Нейт", Stressed: "Н+ейт"},
		},
	}
	text := "Нейт сказал. Нейт ушёл."
	result := s.Apply(text)
	if strings.Count(result, "Н+ейт") != 2 {
		t.Errorf("expected 2 replacements, got %d in: %s", strings.Count(result, "Н+ейт"), result)
	}
}

func TestStress_Apply_DoesNotLoseTextOnWordBoundaryReject(t *testing.T) {
	t.Parallel()
	// Regression test: when a term match is rejected by atWordBoundary
	// (because it's inside a longer word), the text before the rejected
	// match must NOT be lost.
	s := &Stress{
		Entries: []StressEntry{
			{Term: "зак", Stressed: "з+ак"},
		},
	}
	// "заключалась" contains "зак" but atWordBoundary should reject it.
	text := "Глава 300. Настоящая проблема заключалась в другом. Зак ушёл."
	result := s.Apply(text)

	if len(result) < len(text)-10 {
		t.Errorf("text was lost: original %d bytes, result %d bytes\nresult: %s",
			len(text), len(result), result)
	}
	if !contains(result, "Глава 300") {
		t.Errorf("beginning of text lost: %s", result)
	}
	if !contains(result, "заключалась") {
		t.Errorf("word with rejected match corrupted: %s", result)
	}
	if !contains(result, "з+ак ушёл") {
		t.Errorf("valid match not replaced: %s", result)
	}
}

// --- Stress merge tests ---

func TestStress_Merge_NoConflicts(t *testing.T) {
	t.Parallel()
	global := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
		},
	}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "догов+ор"},
			{Term: "кедров", Stressed: "к+едров"}, // same — no conflict
		},
	}
	conflicts := global.Merge(other)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got %d", len(conflicts))
	}
	if len(global.Entries) != 2 {
		t.Errorf("expected 2 entries after merge, got %d", len(global.Entries))
	}
}

func TestStress_Merge_Conflicts(t *testing.T) {
	t.Parallel()
	global := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "догов+ор"},
		},
	}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "д+оговор"}, // different stress — conflict
		},
	}
	conflicts := global.Merge(other)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Term != "договор" {
		t.Errorf("conflict term: got %q", conflicts[0].Term)
	}
	if len(conflicts[0].Variants) != 2 {
		t.Errorf("expected 2 variants, got %d", len(conflicts[0].Variants))
	}
}

func TestMergeAll(t *testing.T) {
	t.Parallel()
	ch1 := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
			{Term: "договор", Stressed: "догов+ор"},
		},
	}
	ch2 := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"}, // same — no conflict
			{Term: "звонит", Stressed: "зв+онит"},
		},
	}
	ch3 := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "д+оговор"}, // conflict with ch1
		},
	}
	merged, conflicts := MergeAll([]*Stress{ch1, ch2, ch3})
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Term != "договор" {
		t.Errorf("conflict term: got %q", conflicts[0].Term)
	}
	if len(merged.Entries) != 3 {
		t.Errorf("expected 3 merged entries, got %d", len(merged.Entries))
	}
}

func TestStress_ResolveConflict(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "догов+ор"},
		},
	}
	// Update existing.
	s.ResolveConflict("договор", "д+оговор")
	entry, _ := s.Lookup("договор")
	if entry.Stressed != "д+оговор" {
		t.Errorf("resolve failed: got %q", entry.Stressed)
	}
	if !entry.Approved {
		t.Error("resolved entry should be marked Approved")
	}
	// Remove by empty stressed.
	s.ResolveConflict("договор", "")
	if _, ok := s.Lookup("договор"); ok {
		t.Error("entry should be removed")
	}
	// Add new.
	s.ResolveConflict("новый", "н+овый")
	entry, _ = s.Lookup("новый")
	if entry.Stressed != "н+овый" {
		t.Errorf("add failed: got %q", entry.Stressed)
	}
	if !entry.Approved {
		t.Error("new resolved entry should be marked Approved")
	}
}

func TestStress_Merge_ApprovedNoConflict(t *testing.T) {
	t.Parallel()
	global := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "догов+ор", Approved: true},
		},
	}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "д+оговор"}, // different stress, but existing is approved
		},
	}
	conflicts := global.Merge(other)
	if len(conflicts) != 0 {
		t.Errorf("approved entry should not produce conflict, got %d", len(conflicts))
	}
	// Existing approved form should be kept.
	if entry, _ := global.Lookup("договор"); entry.Stressed != "догов+ор" {
		t.Errorf("approved form should be kept, got %q", entry.Stressed)
	}
}

func TestStress_Merge_NotApprovedStillConflicts(t *testing.T) {
	t.Parallel()
	global := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "догов+ор", Approved: false},
		},
	}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "д+оговор"},
		},
	}
	conflicts := global.Merge(other)
	if len(conflicts) != 1 {
		t.Fatalf("non-approved entry should still conflict, got %d", len(conflicts))
	}
}

func TestStress_MergeChapter_TagsNewEntries(t *testing.T) {
	t.Parallel()
	global := &Stress{}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
			{Term: "договор", Stressed: "догов+ор"},
		},
	}
	global.MergeChapter(other, 5)

	if len(global.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(global.Entries))
	}
	for _, e := range global.Entries {
		if len(e.Chapters) != 1 || e.Chapters[0] != 5 {
			t.Errorf("entry %q: expected Chapters=[5], got %v", e.Term, e.Chapters)
		}
	}
}

func TestStress_MergeChapter_TagsExistingEntries(t *testing.T) {
	t.Parallel()
	global := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров", Chapters: []int{1}},
		},
	}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
		},
	}
	global.MergeChapter(other, 3)

	if len(global.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(global.Entries))
	}
	e := global.Entries[0]
	if len(e.Chapters) != 2 {
		t.Fatalf("expected 2 chapter IDs, got %d", len(e.Chapters))
	}
	// Should contain both 1 and 3.
	has1, has3 := false, false
	for _, c := range e.Chapters {
		if c == 1 {
			has1 = true
		}
		if c == 3 {
			has3 = true
		}
	}
	if !has1 || !has3 {
		t.Errorf("expected Chapters to contain 1 and 3, got %v", e.Chapters)
	}
}

func TestStress_MergeChapter_NoDuplicateChapterID(t *testing.T) {
	t.Parallel()
	global := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров", Chapters: []int{5}},
		},
	}
	other := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров"},
		},
	}
	global.MergeChapter(other, 5)

	if len(global.Entries[0].Chapters) != 1 {
		t.Errorf("expected no duplicate chapter ID, got %v", global.Entries[0].Chapters)
	}
}

func TestStress_CountForChapter(t *testing.T) {
	t.Parallel()
	s := &Stress{
		Entries: []StressEntry{
			{Term: "кедров", Stressed: "к+едров", Chapters: []int{1, 3}},
			{Term: "договор", Stressed: "догов+ор", Chapters: []int{3}},
			{Term: "мир", Stressed: "м+ир", Chapters: []int{1, 2}},
		},
	}
	if s.CountForChapter(1) != 2 {
		t.Errorf("chapter 1: expected 2, got %d", s.CountForChapter(1))
	}
	if s.CountForChapter(2) != 1 {
		t.Errorf("chapter 2: expected 1, got %d", s.CountForChapter(2))
	}
	if s.CountForChapter(3) != 2 {
		t.Errorf("chapter 3: expected 2, got %d", s.CountForChapter(3))
	}
	if s.CountForChapter(99) != 0 {
		t.Errorf("chapter 99: expected 0, got %d", s.CountForChapter(99))
	}
}

func TestHasValidStressMark(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  bool
	}{
		{"к+едров", true},
		{"догов+ор", true},
		{"+Эдвард", true},
		{"кедров", false},      // no + at all
		{"М+С", false},         // + before consonant
		{"фамил+ьяр", false},   // + before soft sign
		{"текст+", false},      // + at end
		{"+текст", false},      // + before consonant at start
		{"к+едров д+ом", true}, // multiple valid marks
		{"", false},
	}
	for _, tc := range tests {
		got := HasValidStressMark(tc.input)
		if got != tc.want {
			t.Errorf("HasValidStressMark(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// --- SSML generation tests ---

func TestGenerateSSML_Simple(t *testing.T) {
	t.Parallel()
	text := "Привет мир. До свидания."
	result := GenerateSSML(text)

	if !strings.HasPrefix(result, "<speak>") {
		t.Errorf("should start with <speak>: %s", result)
	}
	if !strings.HasSuffix(result, "</speak>") {
		t.Errorf("should end with </speak>: %s", result)
	}
	if !contains(result, "<p>") {
		t.Errorf("should contain <p>: %s", result)
	}
	if !contains(result, "<s>Привет мир.</s>") {
		t.Errorf("should contain first sentence: %s", result)
	}
	if !contains(result, "<s>До свидания.</s>") {
		t.Errorf("should contain second sentence: %s", result)
	}
}

func TestGenerateSSML_MultipleParagraphs(t *testing.T) {
	t.Parallel()
	text := "Первый абзац.\n\nВторой абзац."
	result := GenerateSSML(text)

	pCount := strings.Count(result, "<p>")
	if pCount != 2 {
		t.Errorf("expected 2 paragraphs, got %d: %s", pCount, result)
	}
}

func TestGenerateSSML_PreservesStressMarks(t *testing.T) {
	t.Parallel()
	text := "В недрах тундры выдры в г+етрах т+ырят в вёдра ядра к+едров."
	result := GenerateSSML(text)

	if !contains(result, "г+етрах") {
		t.Errorf("stress mark should be preserved: %s", result)
	}
	if !contains(result, "к+едров") {
		t.Errorf("stress mark should be preserved: %s", result)
	}
}

func TestGenerateSSML_Empty(t *testing.T) {
	t.Parallel()
	result := GenerateSSML("")
	if result != "<speak></speak>" {
		t.Errorf("empty text should produce empty SSML: got %q", result)
	}
}

func TestGenerateSSML_EscapesXML(t *testing.T) {
	t.Parallel()
	text := "5 < 10 & 20 > 15."
	result := GenerateSSML(text)

	if !contains(result, "&lt;") {
		t.Errorf("should escape <: %s", result)
	}
	if !contains(result, "&gt;") {
		t.Errorf("should escape >: %s", result)
	}
	if !contains(result, "&amp;") {
		t.Errorf("should escape &: %s", result)
	}
}

func TestGenerateSSML_ExclamationAndQuestion(t *testing.T) {
	t.Parallel()
	text := "Что это? Как интересно!"
	result := GenerateSSML(text)

	if !contains(result, "<s>Что это?</s>") {
		t.Errorf("should split on ?: %s", result)
	}
	if !contains(result, "<s>Как интересно!</s>") {
		t.Errorf("should split on !: %s", result)
	}
}

func TestGenerateSSML_LatinToCyrillic(t *testing.T) {
	t.Parallel()
	// Latin characters in Russian text crash Silero's SSML parser.
	// They should be replaced with Cyrillic look-alikes.
	tests := []struct {
		name  string
		input string
		want  string // substring expected in output
		bad   string // substring that must NOT appear
	}{
		{
			name:  "Latin M in MК",
			input: "Все клетки MК старика.",
			want:  "МК",
			bad:   "MК",
		},
		{
			name:  "Latin A for blood type",
			input: "Употребляю групу крови A, но не знаю.",
			want:  "крови А,",
			bad:   "крови A,",
		},
		{
			name:  "VR game",
			input: "Внутри VR-игры игроки.",
			want:  "ВР-игры",
			bad:   "VR-игры",
		},
		{
			name:  "Latin S in МВS",
			input: "МВS триста девяносто один.",
			want:  "МВС",
			bad:   "МВS",
		},
		{
			name:  "Latin D for class D",
			input: "Он был в классе D.",
			want:  "классе Д.",
			bad:   "классе D.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := GenerateSSML(tc.input)
			if !contains(result, tc.want) {
				t.Errorf("expected %q in output: %s", tc.want, result)
			}
			if contains(result, tc.bad) {
				t.Errorf("Latin %q should be replaced: %s", tc.bad, result)
			}
		})
	}
}

func TestSanitizeStressMarks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid stress preserved",
			input: "к+едров д+ом",
			want:  "к+едров д+ом",
		},
		{
			name:  "stress before consonant stripped",
			input: "М+С школа",
			want:  "МС школа",
		},
		{
			name:  "stress before soft sign stripped",
			input: "фамил+ьяром",
			want:  "фамильяром",
		},
		{
			name:  "stress before consonant in word stripped",
			input: "Деся+той",
			want:  "Десятой",
		},
		{
			name:  "stress at end of text stripped",
			input: "текст+",
			want:  "текст",
		},
		{
			name:  "mixed valid and invalid",
			input: "к+едров М+С д+ом фамил+ьяр",
			want:  "к+едров МС д+ом фамильяр",
		},
		{
			name:  "no stress marks unchanged",
			input: "просто текст",
			want:  "просто текст",
		},
		{
			name:  "uppercase vowel stress preserved",
			input: "К+едров",
			want:  "К+едров",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeStressMarks(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeStressMarks(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// contains is a simple substring check for test readability.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsString(s, substr))
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
