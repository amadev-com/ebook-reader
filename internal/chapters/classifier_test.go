package chapters

import "testing"

func TestClassifyTitleKeep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		rule  string
	}{
		{"Chapter 1", "chapter-num"},
		{"Chapter 42", "chapter-num"},
		{"Chapter IV", "chapter-num"}, // roman numeral
		{"chapter 1: Beginnings", "chapter-num"},
		{"Chapter One", "chapter-any"},
		{"Chapter 1: Just an old Book", "chapter-num"},
		{"Part 1", "part"},
		{"Part II", "part"},
		{"Book 1", "book-num"},
		{"Volume 3", "volume"},
		{"Prologue", "prologue"},
		{"Epilogue", "epilogue"},
		{"Preface", "preface"},
		{"Introduction", "introduction"},
		{"Foreword", "foreword"},
		{"Afterword", "afterword"},
		{"Appendix A", "appendix"},
	}
	for _, c := range cases {
		r := ClassifyTitle(c.title)
		if r.Decision != DecisionKeep {
			t.Errorf("ClassifyTitle(%q) = %s, want Keep", c.title, r.Decision)
		}
		if r.Rule != c.rule {
			t.Errorf("ClassifyTitle(%q).Rule = %q, want %q", c.title, r.Rule, c.rule)
		}
	}
}

func TestClassifyTitleSkip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		rule  string
	}{
		{"Cover", "cover"},
		{"Copyright", "copyright"},
		{"Copyright Page", "copyright"},
		{"Title Page", "title-page"},
		{"Contents", "contents"},
		{"Table of Contents", "contents"},
		{"TOC", "toc"},
		{"Acknowledgements", "acknowledgements"},
		{"Acknowledgments", "acknowledgements"},
		{"About the Author", "about-author"},
		{"Dedication", "dedication"},
		{"Also by the Author", "also-by"},
		{"Colophon", "colophon"},
		{"Preview", "preview"},
		{"Excerpt", "excerpt"},
		{"Discussion Questions", "discussion"},
		{"Praise for the Book", "praise"},
		{"Information", "information"},
		{"Notes", "notes"},
		{"References", "references"},
		{"Index", "index"},
		{"Glossary", "glossary"},
		{"Map", "map"},
		{"List of Illustrations", "illustrations"},
		{"List of Figures", "figures"},
	}
	for _, c := range cases {
		r := ClassifyTitle(c.title)
		if r.Decision != DecisionSkip {
			t.Errorf("ClassifyTitle(%q) = %s, want Skip", c.title, r.Decision)
		}
		if r.Rule != c.rule {
			t.Errorf("ClassifyTitle(%q).Rule = %q, want %q", c.title, r.Rule, c.rule)
		}
	}
}

func TestClassifyTitleAmbiguous(t *testing.T) {
	t.Parallel()
	cases := []string{
		"The Boy Who Lived",
		"A New Beginning",
		"Unexpected Visitors",
		"Something Strange",
		"",
	}
	for _, title := range cases {
		// Empty title is a special case (skip), so skip it here.
		if title == "" {
			continue
		}
		r := ClassifyTitle(title)
		if r.Decision != DecisionAmbiguous {
			t.Errorf("ClassifyTitle(%q) = %s, want Ambiguous", title, r.Decision)
		}
	}
}

func TestClassifyTitleEmptyIsSkip(t *testing.T) {
	t.Parallel()
	r := ClassifyTitle("")
	if r.Decision != DecisionSkip {
		t.Errorf("ClassifyTitle(\"\") = %s, want Skip", r.Decision)
	}
	if r.Rule != "empty-title" {
		t.Errorf("ClassifyTitle(\"\").Rule = %q, want empty-title", r.Rule)
	}
}

func TestDecisionString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    Decision
		want string
	}{
		{DecisionKeep, "KEEP"},
		{DecisionSkip, "SKIP"},
		{DecisionAmbiguous, "AMBIGUOUS"},
		{DecisionUnknown, "UNKNOWN"},
	}
	for _, c := range cases {
		if got := c.d.String(); got != c.want {
			t.Errorf("Decision(%d).String() = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestIsKeepAndIsSkip(t *testing.T) {
	t.Parallel()
	if !IsKeep("Chapter 1") {
		t.Error("IsKeep(Chapter 1) = false, want true")
	}
	if IsKeep("Cover") {
		t.Error("IsKeep(Cover) = true, want false")
	}
	if !IsSkip("Cover") {
		t.Error("IsSkip(Cover) = false, want true")
	}
	if IsSkip("Chapter 1") {
		t.Error("IsSkip(Chapter 1) = true, want false")
	}
}
