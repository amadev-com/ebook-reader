package tts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoopEngine_Synthesize(t *testing.T) {
	engine, err := NewNoopEngine(EngineConfig{Engine: "noop"})
	if err != nil {
		t.Fatalf("NewNoopEngine: %v", err)
	}
	if engine.Name() != "noop" {
		t.Errorf("name: got %q, want %q", engine.Name(), "noop")
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "test.wav")

	err = engine.Synthesize(context.Background(), "Небольшой текст для теста.", outPath)
	if err != nil {
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
	if string(data[:4]) != "RIFF" {
		t.Errorf("not a WAV file: missing RIFF header, got %q", data[:4])
	}
	if string(data[8:12]) != "WAVE" {
		t.Errorf("not a WAV file: missing WAVE, got %q", data[8:12])
	}
}

func TestNewEngine_Noop(t *testing.T) {
	engine, err := NewEngine(EngineConfig{Engine: "noop"})
	if err != nil {
		t.Fatalf("NewEngine noop: %v", err)
	}
	if engine.Name() != "noop" {
		t.Errorf("engine name: got %q", engine.Name())
	}
}

func TestNewEngine_Unknown(t *testing.T) {
	_, err := NewEngine(EngineConfig{Engine: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown engine")
	}
	if !contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention engine name: %v", err)
	}
}

func TestAvailableEngines(t *testing.T) {
	engines := AvailableEngines()
	if !contains(engines, "noop") {
		t.Errorf("noop should be in available engines: %s", engines)
	}
	if !contains(engines, "silero-http") {
		t.Errorf("silero-http should be in available engines: %s", engines)
	}
}

// --- Stress tests ---

func TestLoadStress_AbsentFile(t *testing.T) {
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
		text := word + " было много."
		result := s.Apply(text)
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
	s := &Stress{}
	text := "Привет мир."
	result := s.Apply(text)
	if result != text {
		t.Errorf("empty stress should not change text: got %q", result)
	}
}

func TestStress_Apply_MultipleOccurrences(t *testing.T) {
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
	s := &Stress{
		Entries: []StressEntry{
			{Term: "договор", Stressed: "догов+ор"},
		},
	}
	// Update existing.
	s.ResolveConflict("договор", "д+оговор")
	if entry, _ := s.Lookup("договор"); entry.Stressed != "д+оговор" {
		t.Errorf("resolve failed: got %q", entry.Stressed)
	}
	// Remove by empty stressed.
	s.ResolveConflict("договор", "")
	if _, ok := s.Lookup("договор"); ok {
		t.Error("entry should be removed")
	}
	// Add new.
	s.ResolveConflict("новый", "н+овый")
	if entry, _ := s.Lookup("новый"); entry.Stressed != "н+овый" {
		t.Errorf("add failed: got %q", entry.Stressed)
	}
}

// --- SSML generation tests ---

func TestGenerateSSML_Simple(t *testing.T) {
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
	text := "Первый абзац.\n\nВторой абзац."
	result := GenerateSSML(text)

	pCount := strings.Count(result, "<p>")
	if pCount != 2 {
		t.Errorf("expected 2 paragraphs, got %d: %s", pCount, result)
	}
}

func TestGenerateSSML_PreservesStressMarks(t *testing.T) {
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
	result := GenerateSSML("")
	if result != "<speak></speak>" {
		t.Errorf("empty text should produce empty SSML: got %q", result)
	}
}

func TestGenerateSSML_EscapesXML(t *testing.T) {
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
