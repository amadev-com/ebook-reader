package translation

import (
	"strings"
	"testing"
)

func TestGlossaryExtractionUser(t *testing.T) {
	t.Parallel()
	chapters := []ChapterText{
		{Title: "Chapter 1: Beginnings", Text: "It was the best of times..."},
		{Title: "Chapter 2: Endings", Text: "The end is near..."},
	}
	prompt := GlossaryExtractionUser(chapters, "")
	if !strings.Contains(prompt, "Chapter 1: Beginnings") {
		t.Errorf("prompt missing chapter 1 title: %s", prompt)
	}
	if !strings.Contains(prompt, "It was the best of times") {
		t.Errorf("prompt missing chapter 1 text")
	}
	if !strings.Contains(prompt, "Return the merged JSON glossary") {
		t.Errorf("prompt missing instruction")
	}

	// With existing glossary.
	prompt = GlossaryExtractionUser(chapters, `{"characters":[{"name":"Quinn"}],"terms":[]}`)
	if !strings.Contains(prompt, "Glossary extracted from previous chapters") {
		t.Errorf("prompt missing existing glossary section")
	}
	if !strings.Contains(prompt, `"name":"Quinn"`) {
		t.Errorf("prompt missing existing glossary content")
	}
}

func TestSystem(t *testing.T) {
	t.Parallel()
	sys := System("", "")
	if !strings.Contains(sys, "professional literary translator") {
		t.Errorf("system prompt missing persona")
	}
	if !strings.Contains(sys, "glossary consistently") {
		t.Errorf("system prompt missing glossary rule")
	}

	// With glossary block.
	sys = System("[character]\n  Quinn = Куинн\n", "")
	if !strings.Contains(sys, "Quinn = Куинн") {
		t.Errorf("system prompt missing glossary block")
	}

	// With previous context.
	sys = System("", "=== Previous chapter context ===\n[Chapter 1 summary]\n...")
	if !strings.Contains(sys, "Previous chapter context") {
		t.Errorf("system prompt missing previous context")
	}
}

func TestUser(t *testing.T) {
	t.Parallel()
	ch := ChapterInfo{ID: 5, Title: "The Awakening"}
	prompt := User(ch, "The source text here.")
	if !strings.Contains(prompt, "Chapter 5") {
		t.Errorf("user prompt missing chapter id")
	}
	if !strings.Contains(prompt, "The Awakening") {
		t.Errorf("user prompt missing chapter title")
	}
	if !strings.Contains(prompt, "The source text here.") {
		t.Errorf("user prompt missing source text")
	}
}

func TestSummaryUser(t *testing.T) {
	t.Parallel()
	ch := ChapterInfo{ID: 3, Title: "Chapter 3"}
	prompt := SummaryUser(ch, "The chapter text.")
	if !strings.Contains(prompt, "Summarize") {
		t.Errorf("summary prompt missing instruction")
	}
	if !strings.Contains(prompt, "Chapter 3") {
		t.Errorf("summary prompt missing title")
	}
}

func TestNewTermsUser(t *testing.T) {
	t.Parallel()
	ch := ChapterInfo{ID: 7, Title: "Chapter 7"}
	prompt := NewTermsUser(ch, "English source", "Russian translation")
	if !strings.Contains(prompt, "English source") {
		t.Errorf("new terms prompt missing source")
	}
	if !strings.Contains(prompt, "Russian translation") {
		t.Errorf("new terms prompt missing translation")
	}
	if !strings.Contains(prompt, "Extract new glossary terms") {
		t.Errorf("new terms prompt missing instruction")
	}
}

func TestGlossaryExtractionSystem(t *testing.T) {
	t.Parallel()
	if !strings.Contains(GlossaryExtractionSystem, "characters") {
		t.Error("system prompt missing characters")
	}
	if !strings.Contains(GlossaryExtractionSystem, "JSON") {
		t.Error("system prompt missing JSON instruction")
	}
}

func TestSummarySystem(t *testing.T) {
	t.Parallel()
	if !strings.Contains(SummarySystem, "Summarize") {
		t.Error("summary system prompt missing instruction")
	}
	if !strings.Contains(SummarySystem, "English") {
		t.Error("summary system prompt should specify English output")
	}
}

func TestNewTermsSystem(t *testing.T) {
	t.Parallel()
	if !strings.Contains(NewTermsSystem, "JSON") {
		t.Error("new terms system prompt missing JSON instruction")
	}
}
