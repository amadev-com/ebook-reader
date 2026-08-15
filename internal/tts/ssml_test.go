package tts //nolint:testpackage // needs access to unexported ssml helpers

import (
	"slices"
	"testing"
)

func TestFindLatinWords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "pure Cyrillic",
			text: "Привет мир, это русский текст.",
			want: nil,
		},
		{
			name: "single Latin word",
			text: "Затем в него infusedирована ваша способность.",
			want: []string{"infusedирована"},
		},
		{
			name: "multiple Latin words",
			text: "Boneclaw был troublemakerом.",
			want: []string{"Boneclaw", "troublemakerом"},
		},
		{
			name: "acronym included",
			text: "ДНК и GPS — это акронимы.",
			want: []string{"GPS"},
		},
		{
			name: "mixed case Latin word",
			text: "Он сказал deployed.",
			want: []string{"deployed"},
		},
		{
			name: "empty text",
			text: "",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FindLatinWords(tt.text)
			if !slices.Equal(got, tt.want) {
				t.Errorf("FindLatinWords(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestHasLatinLetters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want bool
	}{
		{"Привет мир", false},
		{"infusedирована", true},
		{"", false},
		{"123 !?", false},
		{"DNA", true},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()
			if got := HasLatinLetters(tt.text); got != tt.want {
				t.Errorf("HasLatinLetters(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestTransliterateLatin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "pure Cyrillic unchanged",
			text: "Привет мир",
			want: "Привет мир",
		},
		{
			name: "visual equivalent",
			text: "Boneclaw",
			want: "Вонеслав",
		},
		{
			name: "phonetic equivalent",
			text: "infused",
			want: "инфусед",
		},
		{
			name: "mixed text",
			text: "Затем infusedирована",
			want: "Затем инфуседирована",
		},
		{
			name: "empty string",
			text: "",
			want: "",
		},
		{
			name: "preserves non-letter characters",
			text: "test! 123",
			want: "тест! 123",
		},
		{
			name: "acronym spelled out",
			text: "DNA",
			want: "ДЭ ЭН А ",
		},
		{
			name: "acronym in sentence",
			text: "У него GPS навигатор.",
			want: "У него ГЭ ПЭ ЭС  навигатор.",
		},
		{
			name: "single Latin letter not acronym",
			text: "класс A",
			want: "класс А",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := TransliterateLatin(tt.text)
			if got != tt.want {
				t.Errorf("TransliterateLatin(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestStripSSMLTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ssml string
		want string
	}{
		{
			name: "basic SSML",
			ssml: "<speak><p><s>Привет мир.</s></p></speak>",
			want: "Привет мир.",
		},
		{
			name: "multiple paragraphs",
			ssml: "<speak>\n<p><s>Первый.</s></p>\n<p><s>Второй.</s></p>\n</speak>",
			want: "\nПервый.\nВторой.\n",
		},
		{
			name: "empty tags",
			ssml: emptySSML,
			want: "",
		},
		{
			name: "no tags",
			ssml: "Простой текст.",
			want: "Простой текст.",
		},
		{
			name: "nested stress marks preserved",
			ssml: "<speak><p><s>к+едров</s></p></speak>",
			want: "к+едров",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := StripSSMLTags(tt.ssml)
			if got != tt.want {
				t.Errorf("StripSSMLTags(%q) = %q, want %q", tt.ssml, got, tt.want)
			}
		})
	}
}
