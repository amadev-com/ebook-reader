package tts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseTranslation_BasicStructure(t *testing.T) {
	text := "Привет мир.\n\nЭто второй абзац. В нём два предложения."
	doc := ParseTranslation(text, nil)

	if len(doc.Paragraphs) != 2 {
		t.Fatalf("expected 2 paragraphs, got %d", len(doc.Paragraphs))
	}
	if len(doc.Paragraphs[0].Sentences) != 1 {
		t.Errorf("para 0: expected 1 sentence, got %d", len(doc.Paragraphs[0].Sentences))
	}
	if len(doc.Paragraphs[1].Sentences) != 2 {
		t.Errorf("para 1: expected 2 sentences, got %d", len(doc.Paragraphs[1].Sentences))
	}
	if doc.Paragraphs[0].Sentences[0].Text != "Привет мир." {
		t.Errorf("para 0 sent 0: got %q", doc.Paragraphs[0].Sentences[0].Text)
	}
}

func TestParseTranslation_EmptyInput(t *testing.T) {
	doc := ParseTranslation("", nil)
	if doc == nil || len(doc.Paragraphs) != 0 {
		t.Fatalf("expected empty SSML for empty input, got %+v", doc)
	}
}

func TestParseTranslation_WhitespaceNormalization(t *testing.T) {
	text := "Слово   с   лишними\tпробелами. Конец."
	doc := ParseTranslation(text, nil)
	if len(doc.Paragraphs) != 1 || len(doc.Paragraphs[0].Sentences) != 2 {
		t.Fatalf("expected 1 para 2 sentences, got %+v", doc)
	}
	if doc.Paragraphs[0].Sentences[0].Text != "Слово с лишними пробелами." {
		t.Errorf("whitespace not normalized: %q", doc.Paragraphs[0].Sentences[0].Text)
	}
}

func TestParseTranslation_EllipsisSplit(t *testing.T) {
	text := "Она задумалась… Потом продолжила."
	doc := ParseTranslation(text, nil)
	if len(doc.Paragraphs) != 1 {
		t.Fatalf("expected 1 paragraph, got %d", len(doc.Paragraphs))
	}
	if len(doc.Paragraphs[0].Sentences) != 2 {
		t.Errorf("expected 2 sentences (ellipsis split), got %d", len(doc.Paragraphs[0].Sentences))
	}
}

func TestParseTranslation_PronunciationHints(t *testing.T) {
	pron := &Pronunciation{
		Entries: []PronunciationEntry{
			{Term: "Орден", Phonemes: "ɔrdʲen", Alphabet: "ipa"},
		},
	}
	text := "Орден был велик. Все знали Орден."
	doc := ParseTranslation(text, pron)

	if len(doc.Paragraphs) != 1 || len(doc.Paragraphs[0].Sentences) != 2 {
		t.Fatalf("expected 1 para 2 sentences, got %+v", doc)
	}

	// First sentence should have one hint for "Орден".
	sent0 := doc.Paragraphs[0].Sentences[0]
	if len(sent0.Hints) != 1 {
		t.Fatalf("sentence 0: expected 1 hint, got %d", len(sent0.Hints))
	}
	hint := sent0.Hints[0]
	if hint.Term != "Орден" {
		t.Errorf("hint term: got %q, want %q", hint.Term, "Орден")
	}
	if hint.Phonemes != "ɔrdʲen" {
		t.Errorf("hint phonemes: got %q", hint.Phonemes)
	}
	if hint.Alphabet != "ipa" {
		t.Errorf("hint alphabet: got %q", hint.Alphabet)
	}
}

func TestParseTranslation_WordBoundary(t *testing.T) {
	pron := &Pronunciation{
		Entries: []PronunciationEntry{
			{Term: "Орден", Phonemes: "test", Alphabet: "ipa"},
		},
	}
	// "Орденский" should NOT match "Орден" because of word boundary.
	text := "Орденский собор был красив."
	doc := ParseTranslation(text, pron)
	if len(doc.Paragraphs) != 1 || len(doc.Paragraphs[0].Sentences) != 1 {
		t.Fatalf("unexpected structure")
	}
	if len(doc.Paragraphs[0].Sentences[0].Hints) != 0 {
		t.Errorf("should not match inside a longer word, got %d hints",
			len(doc.Paragraphs[0].Sentences[0].Hints))
	}
}

func TestParseTranslation_GreedyLongerMatchFirst(t *testing.T) {
	pron := &Pronunciation{
		Entries: []PronunciationEntry{
			{Term: "Орден", Phonemes: "short", Alphabet: "ipa"},
			{Term: "Орден Света", Phonemes: "long", Alphabet: "ipa"},
		},
	}
	text := "Орден Света был силён."
	doc := ParseTranslation(text, pron)
	if len(doc.Paragraphs[0].Sentences[0].Hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(doc.Paragraphs[0].Sentences[0].Hints))
	}
	hint := doc.Paragraphs[0].Sentences[0].Hints[0]
	if hint.Phonemes != "long" {
		t.Errorf("expected longer match 'long', got %q", hint.Phonemes)
	}
}

func TestParseTranslation_CaseInsensitive(t *testing.T) {
	pron := &Pronunciation{
		Entries: []PronunciationEntry{
			{Term: "орден", Phonemes: "test", Alphabet: "ipa"},
		},
	}
	text := "ОРДЕН был велик."
	doc := ParseTranslation(text, pron)
	if len(doc.Paragraphs[0].Sentences[0].Hints) != 1 {
		t.Errorf("expected case-insensitive match, got %d hints",
			len(doc.Paragraphs[0].Sentences[0].Hints))
	}
}

func TestRender_BasicSSML(t *testing.T) {
	doc := &SSML{
		Paragraphs: []SSMLParagraph{
			{Sentences: []SSMLSentence{
				{Text: "Привет мир."},
			}},
		},
	}
	out := doc.Render()
	expected := "<speak>\n  <p>\n    <s>Привет мир.</s>\n  </p>\n</speak>\n"
	if out != expected {
		t.Errorf("render mismatch:\ngot:  %q\nwant: %q", out, expected)
	}
}

func TestRender_WithPhonemeHint(t *testing.T) {
	// "Орден" is 10 bytes in UTF-8 (5 Cyrillic chars × 2 bytes each).
	ordenLen := len("Орден")
	doc := &SSML{
		Paragraphs: []SSMLParagraph{
			{Sentences: []SSMLSentence{
				{
					Text: "Орден был велик.",
					Hints: []SSMLHint{
						{Start: 0, End: ordenLen, Term: "Орден", Phonemes: "ɔrdʲen", Alphabet: "ipa"},
					},
				},
			}},
		},
	}
	out := doc.Render()
	if out == "" {
		t.Fatal("empty render output")
	}
	if !contains(out, `<phoneme alphabet="ipa" ph="ɔrdʲen">Орден</phoneme>`) {
		t.Errorf("missing phoneme tag in output: %s", out)
	}
	if !contains(out, " был велик.") {
		t.Errorf("missing text after phoneme: %s", out)
	}
}

func TestRender_Empty(t *testing.T) {
	doc := &SSML{}
	out := doc.Render()
	if out != "<speak></speak>\n" {
		t.Errorf("empty render: got %q", out)
	}
}

func TestRender_XMLEscaping(t *testing.T) {
	doc := &SSML{
		Paragraphs: []SSMLParagraph{
			{Sentences: []SSMLSentence{
				{Text: `Он сказал "привет" < & >`},
			}},
		},
	}
	out := doc.Render()
	if !contains(out, "&quot;привет&quot;") {
		t.Errorf("quotes not escaped: %s", out)
	}
	if !contains(out, "&lt;") {
		t.Errorf("< not escaped: %s", out)
	}
	if !contains(out, "&gt;") {
		t.Errorf("> not escaped: %s", out)
	}
	if !contains(out, "&amp;") {
		t.Errorf("& not escaped: %s", out)
	}
}

func TestExtractPlainText(t *testing.T) {
	ssml := `<speak><p><s>Привет <phoneme alphabet="ipa" ph="test">мир</phoneme>.</s></p></speak>`
	text := ExtractPlainText(ssml)
	if text != "Привет мир." {
		t.Errorf("extract plain text: got %q, want %q", text, "Привет мир.")
	}
}

func TestExtractPlainText_NoTags(t *testing.T) {
	text := ExtractPlainText("Просто текст без тегов.")
	if text != "Просто текст без тегов." {
		t.Errorf("got %q", text)
	}
}

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
}

func TestLoadPronunciation_AbsentFile(t *testing.T) {
	tmpDir := t.TempDir()
	pron, err := LoadPronunciation(tmpDir)
	if err != nil {
		t.Fatalf("LoadPronunciation on absent file: %v", err)
	}
	if pron == nil || len(pron.Entries) != 0 {
		t.Errorf("expected empty pronunciation, got %+v", pron)
	}
}

func TestPronunciation_Lookup(t *testing.T) {
	pron := &Pronunciation{
		Entries: []PronunciationEntry{
			{Term: "Орден", Phonemes: "test"},
		},
	}
	entry, ok := pron.Lookup("орден") // case-insensitive
	if !ok {
		t.Fatal("case-insensitive lookup failed")
	}
	if entry.Phonemes != "test" {
		t.Errorf("lookup phonemes: got %q", entry.Phonemes)
	}

	_, ok = pron.Lookup("nonexistent")
	if ok {
		t.Error("lookup of nonexistent term should return false")
	}
}

func TestPronunciation_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	pron := &Pronunciation{
		Entries: []PronunciationEntry{
			{Term: "Бета", Phonemes: "b", Alphabet: "ipa"},
			{Term: "Альфа", Phonemes: "a", Alphabet: "ipa"},
		},
	}
	if err := pron.Save(tmpDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadPronunciation(tmpDir)
	if err != nil {
		t.Fatalf("LoadPronunciation: %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded.Entries))
	}
	// Should be sorted by term.
	if loaded.Entries[0].Term != "Альфа" {
		t.Errorf("expected sorted: first entry should be 'Альфа', got %q", loaded.Entries[0].Term)
	}
}

func TestPhonAlphabet_Default(t *testing.T) {
	entry := PronunciationEntry{Term: "test", Phonemes: "x"}
	if entry.PhonAlphabet() != "ipa" {
		t.Errorf("default alphabet should be 'ipa', got %q", entry.PhonAlphabet())
	}
	entry.Alphabet = "x-sampa"
	if entry.PhonAlphabet() != "x-sampa" {
		t.Errorf("explicit alphabet: got %q", entry.PhonAlphabet())
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
