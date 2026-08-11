package tts

import (
	"testing"
)

func TestSplitSentencesForStress(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "simple sentences",
			text: "Привет мир. До свидания.",
			want: []string{"Привет мир.", "До свидания."},
		},
		{
			name: "with newlines",
			text: "Первая строка.\nВторая строка.",
			want: []string{"Первая строка.", "Вторая строка."},
		},
		{
			name: "empty text",
			text: "",
			want: nil,
		},
		{
			name: "whitespace only",
			text: "   \n  ",
			want: nil,
		},
		{
			name: "no ending punctuation",
			text: "Просто текст без точки",
			want: []string{"Просто текст без точки"},
		},
		{
			name: "exclamation and question",
			text: "Что? Это правда! Нет.",
			want: []string{"Что?", "Это правда!", "Нет."},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitSentencesForStress(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sentences, want %d: %v", len(got), len(tc.want), got)
			}
			for i, s := range got {
				if s != tc.want[i] {
					t.Errorf("sentence %d: got %q, want %q", i, s, tc.want[i])
				}
			}
		})
	}
}

func TestNewStressFromOverrides(t *testing.T) {
	t.Parallel()
	overrides := []StressOverride{
		{Term: "замок", Phonemes: "з+амок"},
		{Term: "мука", Phonemes: "мук+а"},
		{Term: "", Phonemes: "should be skipped"},
		{Term: "empty phonemes", Phonemes: ""},
	}
	s := NewStressFromOverrides(overrides)
	if len(s.Entries) != 2 {
		t.Fatalf("expected 2 entries (empty ones skipped), got %d", len(s.Entries))
	}
	if s.Entries[0].Term != "замок" || s.Entries[0].Stressed != "з+амок" {
		t.Errorf("entry 0: got %q = %q", s.Entries[0].Term, s.Entries[0].Stressed)
	}
}

func TestNewStressFromOverrides_Empty(t *testing.T) {
	t.Parallel()
	s := NewStressFromOverrides(nil)
	if len(s.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(s.Entries))
	}
	// Apply on text should be a no-op.
	if got := s.Apply("test text"); got != "test text" {
		t.Errorf("empty overrides should not modify text, got %q", got)
	}
}
