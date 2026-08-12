package chapters

import (
	"testing"

	"ebook-reader/internal/epub"
)

// makeSpineItem is a test helper for building a SpineItem with inline blocks.
func makeSpineItem(id, href string, blocks ...epub.Block) SpineItem {
	return SpineItem{ID: id, Href: href, Blocks: blocks}
}

func h(level int, text, anchor string) epub.Block {
	return epub.Block{Kind: string(StrategyHeading), Level: level, Anchor: anchor, Text: text}
}

func p(text string) epub.Block {
	return epub.Block{Kind: "paragraph", Text: text}
}

func TestSplitByTOC_CleanBook(t *testing.T) {
	t.Parallel()
	in := SplitInput{
		Spine: []SpineItem{
			makeSpineItem("cover", "cover.html", h(1, "Cover", ""), p("Cover text")),
			makeSpineItem("ch1", "chapter1.html", h(2, "Chapter 1: Beginnings", "ch1"), p("Best of times")),
			makeSpineItem("ch2", "chapter2.html", h(2, "Chapter 2: Endings", "ch2"), p("The end")),
		},
		TOC: []epub.TOCEntry{
			{Title: "Cover", SrcFile: "cover.html", Order: 1},
			{Title: "Chapter 1: Beginnings", SrcFile: "chapter1.html", Order: 2},
			{Title: "Chapter 2: Endings", SrcFile: "chapter2.html", Order: 3},
		},
	}
	res, err := Split(in)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if res.Index.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", res.Index.ChapterCount)
	}
	if res.Index.Strategy != StrategyTOC {
		t.Errorf("Strategy = %q, want toc", res.Index.Strategy)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("Skipped length = %d, want 1", len(res.Skipped))
	}
	if res.Skipped[0].Title != "Cover" {
		t.Errorf("Skipped[0].Title = %q, want Cover", res.Skipped[0].Title)
	}
	if res.Chapters[0].ID != 1 || res.Chapters[0].Title != "Chapter 1: Beginnings" {
		t.Errorf("Chapters[0] = %+v, want id=1 title='Chapter 1: Beginnings'", res.Chapters[0])
	}
	if res.Chapters[0].Source != "Chapter 1: Beginnings\n\nBest of times" {
		t.Errorf("Chapters[0].Source = %q", res.Chapters[0].Source)
	}
	if res.Chapters[1].ID != 2 || res.Chapters[1].Title != "Chapter 2: Endings" {
		t.Errorf("Chapters[1] = %+v, want id=2 title='Chapter 2: Endings'", res.Chapters[1])
	}
}

func TestSplitByTOC_AmbiguousDefaultsToSkip(t *testing.T) {
	t.Parallel()
	in := SplitInput{
		Spine: []SpineItem{
			makeSpineItem("front", "front.html", h(1, "Welcome", ""), p("Welcome text")),
			makeSpineItem("ch1", "ch1.html", h(2, "Chapter 1", "ch1"), p("Body")),
			makeSpineItem("ch2", "ch2.html", h(2, "Chapter 2", "ch2"), p("Body 2")),
		},
		TOC: []epub.TOCEntry{
			{Title: "Welcome", SrcFile: "front.html", Order: 1},
			{Title: "Chapter 1", SrcFile: "ch1.html", Order: 2},
			{Title: "Chapter 2", SrcFile: "ch2.html", Order: 3},
		},
	}
	// Test SplitByTOC directly (Split would fall back since 2 chapters is the
	// minimum, and TOC here yields exactly 2 after skipping "Welcome").
	res, err := SplitByTOC(in)
	if err != nil {
		t.Fatalf("SplitByTOC: %v", err)
	}
	if res.Index.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", res.Index.ChapterCount)
	}
	// "Welcome" is ambiguous -> recorded as skip with reason ambiguous-default-skip.
	found := false
	for _, s := range res.Skipped {
		if s.Title == "Welcome" && s.Reason == reasonAmbiguousSkip {
			found = true
		}
	}
	if !found {
		t.Errorf("ambiguous 'Welcome' not recorded in skipped with reason ambiguous-default-skip; got %+v", res.Skipped)
	}
}

func TestSplitFallsBackToHeadingsWhenTOCIsMessy(t *testing.T) {
	t.Parallel()
	// TOC has only front-matter entries (all Skip), so TOC strategy yields 0
	// chapters. Heading strategy should find the h2 "Chapter 1" heading.
	in := SplitInput{
		Spine: []SpineItem{
			makeSpineItem("cover", "cover.html", h(1, "Cover", ""), p("cover")),
			makeSpineItem("body", "body.html",
				h(2, "Chapter 1", "c1"), p("para 1"),
				h(2, "Chapter 2", "c2"), p("para 2"),
			),
		},
		TOC: []epub.TOCEntry{
			{Title: "Cover", SrcFile: "cover.html", Order: 1},
			{Title: "Copyright", SrcFile: "cover.html", Order: 2},
		},
	}
	res, err := Split(in)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if res.Index.Strategy != StrategyHeading {
		t.Errorf("Strategy = %q, want heading (fallback)", res.Index.Strategy)
	}
	if res.Index.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", res.Index.ChapterCount)
	}
	if res.Chapters[0].Title != "Chapter 1" {
		t.Errorf("Chapters[0].Title = %q, want 'Chapter 1'", res.Chapters[0].Title)
	}
	if res.Chapters[1].Title != "Chapter 2" {
		t.Errorf("Chapters[1].Title = %q, want 'Chapter 2'", res.Chapters[1].Title)
	}
}

func TestSplitFallsBackToPerItemWhenNoHeadings(t *testing.T) {
	t.Parallel()
	// No usable TOC, no chapter headings -> per-item fallback.
	in := SplitInput{
		Spine: []SpineItem{
			makeSpineItem("a", "a.html", p("text a")),
			makeSpineItem("b", "b.html", p("text b")),
		},
		TOC: nil,
	}
	res, err := Split(in)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if res.Index.Strategy != StrategyPerItem {
		t.Errorf("Strategy = %q, want per-item", res.Index.Strategy)
	}
	if res.Index.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", res.Index.ChapterCount)
	}
	if len(res.Index.Warnings) == 0 {
		t.Error("expected a warning about per-item fallback, got none")
	}
}

func TestSplitByTOCWithAnchorSplittingOneFile(t *testing.T) {
	t.Parallel()
	// One XHTML file with two chapters distinguished by heading anchors.
	in := SplitInput{
		Spine: []SpineItem{
			makeSpineItem("all", "all.html",
				h(2, "Chapter 1", "ch1"), p("first"),
				h(2, "Chapter 2", "ch2"), p("second"),
			),
		},
		TOC: []epub.TOCEntry{
			{Title: "Chapter 1", SrcFile: "all.html", SrcAnchor: "ch1", Order: 1},
			{Title: "Chapter 2", SrcFile: "all.html", SrcAnchor: "ch2", Order: 2},
		},
	}
	res, err := SplitByTOC(in)
	if err != nil {
		t.Fatalf("SplitByTOC: %v", err)
	}
	if res.Index.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", res.Index.ChapterCount)
	}
	if res.Chapters[0].Source != "Chapter 1\n\nfirst" {
		t.Errorf("Chapters[0].Source = %q, want 'Chapter 1\\n\\nfirst'", res.Chapters[0].Source)
	}
	if res.Chapters[1].Source != "Chapter 2\n\nsecond" {
		t.Errorf("Chapters[1].Source = %q, want 'Chapter 2\\n\\nsecond'", res.Chapters[1].Source)
	}
}

func TestSplitByHeadingsSkipsFrontMatter(t *testing.T) {
	t.Parallel()
	in := SplitInput{
		Spine: []SpineItem{
			makeSpineItem("cover", "cover.html", h(1, "Cover", ""), p("cover")),
			makeSpineItem("ch1", "ch1.html", h(2, "Chapter 1", "c1"), p("body")),
		},
		TOC: nil,
	}
	res, err := SplitByHeadings(in)
	if err != nil {
		t.Fatalf("SplitByHeadings: %v", err)
	}
	if res.Index.ChapterCount != 1 {
		t.Fatalf("ChapterCount = %d, want 1", res.Index.ChapterCount)
	}
	if res.Chapters[0].Title != "Chapter 1" {
		t.Errorf("Chapters[0].Title = %q, want 'Chapter 1'", res.Chapters[0].Title)
	}
	// Cover should be in skipped.
	if len(res.Skipped) != 1 || res.Skipped[0].Title != "Cover" {
		t.Errorf("Skipped = %+v, want Cover", res.Skipped)
	}
}

func TestBlocksToText(t *testing.T) {
	t.Parallel()
	blocks := []epub.Block{
		h(2, "Title", ""),
		p("Para 1"),
		p("Para 2"),
		{Kind: "image", Alt: "diagram"},
		{Kind: "hr"},
	}
	got := blocksToText(blocks)
	want := "Title\n\nPara 1\n\nPara 2\n\n[image: diagram]"
	if got != want {
		t.Errorf("blocksToText = %q, want %q", got, want)
	}
}

func TestBasename(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"OEBPS/page-0.html", "page-0.html"},
		{"page-0.html", "page-0.html"},
		{"a/b/c.xhtml", "c.xhtml"},
		{"a\\b\\c.xhtml", "c.xhtml"},
	}
	for _, c := range cases {
		if got := basename(c.in); got != c.want {
			t.Errorf("basename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
