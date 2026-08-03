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
}

// --- Respelling tests ---

func TestLoadRespelling_AbsentFile(t *testing.T) {
	tmpDir := t.TempDir()
	resp, err := LoadRespelling(tmpDir)
	if err != nil {
		t.Fatalf("LoadRespelling on absent file: %v", err)
	}
	if resp == nil || len(resp.Entries) != 0 {
		t.Errorf("expected empty respelling, got %+v", resp)
	}
}

func TestRespelling_Lookup(t *testing.T) {
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Куинн", Respelled: "КУинн"},
		},
	}
	entry, ok := resp.Lookup("куинн") // case-insensitive
	if !ok {
		t.Fatal("case-insensitive lookup failed")
	}
	if entry.Respelled != "КУинн" {
		t.Errorf("lookup respelled: got %q", entry.Respelled)
	}

	_, ok = resp.Lookup("nonexistent")
	if ok {
		t.Error("lookup of nonexistent term should return false")
	}
}

func TestRespelling_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Бета", Respelled: "БЭта"},
			{Term: "Альфа", Respelled: "Альфа"},
		},
	}
	if err := resp.Save(tmpDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadRespelling(tmpDir)
	if err != nil {
		t.Fatalf("LoadRespelling: %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded.Entries))
	}
	// Should be sorted by term length (longest first).
	if loaded.Entries[0].Term != "Альфа" {
		t.Errorf("expected longest term first: got %q", loaded.Entries[0].Term)
	}
}

func TestRespelling_Apply(t *testing.T) {
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Куинн", Respelled: "КУинн"},
			{Term: "Ларри Стил", Respelled: "Ларри Стиил"},
		},
	}
	text := "Куинн был убеждён, что этот Ларри Стил и есть."
	result := resp.Apply(text)

	if !contains(result, "КУинн") {
		t.Errorf("respelling not applied: %s", result)
	}
	if !contains(result, "Ларри Стиил") {
		t.Errorf("multi-word respelling not applied: %s", result)
	}
	if contains(result, "Куинн ") {
		t.Errorf("original term should be replaced: %s", result)
	}
}

func TestRespelling_Apply_WordBoundary(t *testing.T) {
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Орден", Respelled: "Ордэн"},
		},
	}
	// "Орденский" should NOT match "Орден" because of word boundary.
	text := "Орденский собор был красив."
	result := resp.Apply(text)
	if contains(result, "Ордэн") {
		t.Errorf("should not replace inside a longer word: %s", result)
	}
}

func TestRespelling_Apply_CaseInsensitive(t *testing.T) {
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "орден", Respelled: "ордэн"},
		},
	}
	text := "ОРДЕН был велик."
	result := resp.Apply(text)
	if !contains(result, "ордэн") {
		t.Errorf("case-insensitive replacement failed: %s", result)
	}
}

func TestRespelling_Apply_GreedyLongerMatchFirst(t *testing.T) {
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Орден", Respelled: "Ордэн"},
			{Term: "Орден Света", Respelled: "Ордэн Свэта"},
		},
	}
	// Sort to ensure longest-first (Save does this, but Apply expects it).
	resp.Sort()

	text := "Орден Света был силён."
	result := resp.Apply(text)
	if contains(result, "Ордэн ") && !contains(result, "Ордэн Свэта") {
		t.Errorf("should match longer term first, got: %s", result)
	}
	if !contains(result, "Ордэн Свэта") {
		t.Errorf("longer match not applied: %s", result)
	}
}

func TestRespelling_Apply_Empty(t *testing.T) {
	resp := &Respelling{}
	text := "Привет мир."
	result := resp.Apply(text)
	if result != text {
		t.Errorf("empty respelling should not change text: got %q", result)
	}
}

func TestRespelling_Apply_MultipleOccurrences(t *testing.T) {
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Нейт", Respelled: "НЭйт"},
		},
	}
	text := "Нейт сказал. Нейт ушёл."
	result := resp.Apply(text)
	if strings.Count(result, "НЭйт") != 2 {
		t.Errorf("expected 2 replacements, got %d in: %s", strings.Count(result, "НЭйт"), result)
	}
}

func TestRespelling_Apply_DoesNotLoseTextOnWordBoundaryReject(t *testing.T) {
	// Regression test: when a term match is rejected by atWordBoundary
	// (because it's inside a longer word), the text before the rejected
	// match must NOT be lost. The bug was that searchStart was advanced
	// past the rejected match, causing all text before it to be dropped.
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "зак", Respelled: "зак"}, // no-op, but still triggers search
			{Term: "Зак", Respelled: "зак"},
		},
	}
	// "заключалась" contains "зак" but atWordBoundary should reject it
	// (next char "л" is a word rune). The text before "заключалась"
	// must be preserved.
	text := "Глава 300. Настоящая проблема заключалась в другом. Зак ушёл."
	result := resp.Apply(text)

	// The entire text should be preserved (minus the "Зак" → "зак" replacement).
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
	if !contains(result, "зак ушёл") {
		t.Errorf("valid match not replaced: %s", result)
	}
}

func TestRespelling_Apply_PreservesTextBeforeRejectedMatches(t *testing.T) {
	// Another regression: multiple rejected matches should not compound
	// text loss. Each rejected match should only skip the search position,
	// not advance the "text written" position.
	resp := &Respelling{
		Entries: []RespellingEntry{
			{Term: "Сэм", Respelled: "СЭм"},
		},
	}
	// "Сэм" appears as a standalone word AND as a prefix of "Сэмми".
	// The "Сэмми" match should be rejected, but text before it must survive.
	text := "Сэм пришёл. Сэмми тоже. Сэм ушёл."
	result := resp.Apply(text)

	if !contains(result, "СЭм пришёл") {
		t.Errorf("first match not replaced: %s", result)
	}
	if !contains(result, "Сэмми") {
		t.Errorf("rejected match corrupted word: %s", result)
	}
	if !contains(result, "СЭм ушёл") {
		t.Errorf("last match not replaced: %s", result)
	}
	// Full text should be preserved.
	if len(result) < len(text)-10 {
		t.Errorf("text was lost: original %d bytes, result %d bytes\nresult: %s",
			len(text), len(result), result)
	}
}

// --- NormalizeRussian tests ---

func TestNormalizeRussian_DeCapitalizeMidSentence(t *testing.T) {
	// ALL-CAPS word in the middle of a sentence should be lowercased.
	text := "Он сказал ПРИВЕТ всем."
	result := NormalizeRussian(text)
	if !contains(result, "привет") {
		t.Errorf("mid-sentence ALL-CAPS should be lowercased: %s", result)
	}
}

func TestNormalizeRussian_KeepSentenceStartCapital(t *testing.T) {
	// Word at start of sentence should keep its capitalization.
	text := "ПРИВЕТ всем. ПОКА друзья."
	result := NormalizeRussian(text)
	// After normalization, sentence-start words keep capitals.
	if !contains(result, "ПРИВЕТ") || !contains(result, "ПОКА") {
		t.Errorf("sentence-start capitals should be kept: %s", result)
	}
}

func TestNormalizeRussian_KeepSingleCharWord(t *testing.T) {
	// Single-character "Я" (I) should not be lowercased.
	text := "Я сказал."
	result := NormalizeRussian(text)
	if !contains(result, "Я ") {
		t.Errorf("single-char 'Я' should keep capital: %s", result)
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
