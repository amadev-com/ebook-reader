package chapters

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"ebook-reader/internal/epub"
)

// minTOCEntries is the minimum number of chapters a strategy must produce to
// be considered successful (otherwise the next strategy is tried).
const minTOCEntries = 2

// minHeadingLength is the maximum snippet length (in bytes) returned by
// blocksSnippet when the full text exceeds it.
const minHeadingLength = 200

// Chapter status and skip reason constants.
const (
	chapterStatusRaw    = "raw"
	reasonAmbiguousSkip = "ambiguous-default-skip"
)

// Strategy is the detection strategy used by the splitter.
type Strategy string

const (
	// StrategyTOC is the TOC-driven strategy: TOC entries whose titles
	// classify as Keep become chapters. Content spans from the TOC entry's
	// anchor up to the next kept entry.
	StrategyTOC Strategy = "toc"
	// StrategyHeading is the heading-driven strategy: scan spine blocks for
	// h1/h2 headings matching Keep patterns; each such heading starts a new
	// chapter.
	StrategyHeading Strategy = "heading"
	// StrategyPerItem is the last-resort strategy: each spine item is one
	// chapter.
	StrategyPerItem Strategy = "per-item"
)

// Chapter is the canonical chapter artifact written to chapters/chapter_NNN.json.
type Chapter struct {
	ID                int      `json:"id"`
	Title             string   `json:"title"`
	Source            string   `json:"source"`                    // full English text, paragraphs joined by \n\n
	RawSize           int      `json:"raw_size,omitempty"`        // source length before strip filters (0 = not stripped)
	SectionIDs        []string `json:"section_ids"`               // spine item ids contributing text
	TOCEntryIndex     int      `json:"toc_entry_index,omitempty"` // 0-based index into toc, -1 if none
	DetectionStrategy Strategy `json:"detection_strategy"`
	Status            string   `json:"status"` // "raw" | "translated" | ...
}

// SkippedSection is an entry in chapters/_skipped.json: a section that was
// excluded from translation, with the reason and a text snippet for review.
type SkippedSection struct {
	Title    string   `json:"title"`
	Reason   string   `json:"reason"` // classifier rule name or "ambiguous-default-skip"
	SrcFile  string   `json:"src_file"`
	Snippet  string   `json:"snippet"` // first ~200 chars of text
	Strategy Strategy `json:"strategy"`
}

// Index is the chapters/_index.json artifact summarizing the analyze run.
type Index struct {
	ChapterCount int      `json:"chapter_count"`
	Strategy     Strategy `json:"strategy"`
	Warnings     []string `json:"warnings,omitempty"`
}

// SpineItem is the input representation of one spine entry plus its extracted
// blocks. The splitter consumes a slice of these.
type SpineItem struct {
	Index  int          // 0-based position in the spine
	ID     string       // manifest id
	Href   string       // manifest href
	Blocks []epub.Block // extracted blocks
}

// SplitInput is everything the splitter needs from the extracted/ stage.
type SplitInput struct {
	Spine []SpineItem
	TOC   []epub.TOCEntry
}

// SplitResult is the output of Split: kept chapters + skipped sections + index.
type SplitResult struct {
	Chapters []Chapter
	Skipped  []SkippedSection
	Index    Index
}

// Split detects chapters from the extracted spine + TOC. It tries strategies in
// order: TOC-driven, then heading-driven, then per-item. The first strategy
// that yields at least minChapters (2) chapters wins; otherwise the last
// Split selects the first chapter-detection strategy that produces at least two chapters.
// It tries TOC, heading, and per-item detection in that order, using the per-item result
// with a warning when no earlier strategy succeeds. It returns an error if a strategy
// fails.
func Split(in SplitInput) (*SplitResult, error) {
	strategies := []Strategy{StrategyTOC, StrategyHeading, StrategyPerItem}
	var last *SplitResult
	for _, s := range strategies {
		var res *SplitResult
		var err error
		switch s {
		case StrategyTOC:
			res, err = SplitByTOC(in)
		case StrategyHeading:
			res, err = SplitByHeadings(in)
		case StrategyPerItem:
			res, err = SplitPerItem(in)
		}
		if err != nil {
			return nil, fmt.Errorf("strategy %s: %w", s, err)
		}
		res.Index.Strategy = s
		last = res
		if res.Index.ChapterCount >= minTOCEntries {
			// Per-item is a last resort; even when it "succeeds" it likely
			// produces poor chapter boundaries, so warn the user.
			if s == StrategyPerItem {
				res.Index.Warnings = append(
					res.Index.Warnings,
					"used per-item fallback (no TOC or chapter headings detected); chapter boundaries may be poor. Review _skipped.json and chapters/ manually.",
				)
			}
			slog.Default().Info("chapter detection succeeded", "strategy", s, "chapters", res.Index.ChapterCount)
			return res, nil
		}
		slog.Default().
			Info("strategy yielded too few chapters, trying next", "strategy", s, "chapters", res.Index.ChapterCount)
	}
	last.Index.Warnings = append(last.Index.Warnings,
		"no strategy found 2+ chapters; using per-item fallback. Review _skipped.json and chapters/ manually.")
	return last, nil
}

// SplitByTOC builds chapters from TOC entries classified as Keep. Each kept
// entry's content is the blocks from its source file (and anchor, if present)
// up to the next kept entry. Non-kept entries become SkippedSection records.
// SplitByTOC creates chapters from table-of-contents entries and matching spine items.
// Entries classified as skipped, ambiguous, or unknown are recorded in the result's skipped sections.
func SplitByTOC(in SplitInput) (*SplitResult, error) {
	res := &SplitResult{}
	if len(in.TOC) == 0 {
		res.Index = Index{ChapterCount: 0, Warnings: []string{"no TOC entries found"}}
		return res, nil
	}

	// Build a lookup: src file -> ordered spine items for that file. Most
	// EPUBs have one spine item per file, but some share.
	fileToItems := make(map[string][]SpineItem)
	for _, si := range in.Spine {
		// TOC SrcFile is resolved against the OPF dir, same as the spine
		// href resolved by the importer. We stored the href in the spine
		// entry; resolve it the same way the importer did by comparing on
		// the basename to be robust to path differences.
		fileToItems[basename(si.Href)] = append(fileToItems[basename(si.Href)], si)
	}

	keptEntries := 0
	for i, entry := range in.TOC {
		reason := ClassifyTitle(entry.Title)
		switch reason.Decision {
		case DecisionKeep:
			keptEntries++
			ch := buildChapterFromTOC(in, i, entry)
			if ch != nil {
				ch.ID = keptEntries
				ch.DetectionStrategy = StrategyTOC
				ch.Status = chapterStatusRaw
				res.Chapters = append(res.Chapters, *ch)
			}
		case DecisionSkip:
			res.Skipped = append(res.Skipped, SkippedSection{
				Title:    entry.Title,
				Reason:   reason.Rule,
				SrcFile:  entry.SrcFile,
				Snippet:  snippetForTOC(in, entry),
				Strategy: StrategyTOC,
			})
		case DecisionAmbiguous:
			// M1: treat ambiguous as skip, but record it distinctly so a
			// human (or future AI fallback) can review.
			res.Skipped = append(res.Skipped, SkippedSection{
				Title:    entry.Title,
				Reason:   reasonAmbiguousSkip,
				SrcFile:  entry.SrcFile,
				Snippet:  snippetForTOC(in, entry),
				Strategy: StrategyTOC,
			})
		case DecisionUnknown:
			// DecisionUnknown should not appear in classification results;
			// treat it as a skip for safety.
			res.Skipped = append(res.Skipped, SkippedSection{
				Title:    entry.Title,
				Reason:   "unknown-decision-skip",
				SrcFile:  entry.SrcFile,
				Snippet:  snippetForTOC(in, entry),
				Strategy: StrategyTOC,
			})
		}
	}
	res.Index = Index{ChapterCount: len(res.Chapters)}
	return res, nil
}

// buildChapterFromTOC collects the text blocks belonging to one TOC entry.
// In the common case (one spine item per TOC entry, no anchor), this is just
// that spine item's blocks. With anchors, we split the item's blocks at the
// buildChapterFromTOC constructs a chapter from the spine items matching a TOC entry.
// When the entry specifies an anchor, extracts content starting at the anchored heading;
// otherwise, includes the matching spine content. Returns nil when no spine item matches.
func buildChapterFromTOC(in SplitInput, tocIdx int, entry epub.TOCEntry) *Chapter {
	// Find the spine item(s) whose href basename matches the TOC src file.
	target := basename(entry.SrcFile)
	var items []SpineItem
	for _, si := range in.Spine {
		if basename(si.Href) == target {
			items = append(items, si)
		}
	}
	if len(items) == 0 {
		slog.Default().Warn("TOC entry has no matching spine item", "title", entry.Title, "src", entry.SrcFile)
		return nil
	}

	// For a single matching item with no anchor, take all its blocks.
	if entry.SrcAnchor == "" && len(items) == 1 {
		return chapterFromBlocks(entry.Title, items, tocIdx, StrategyTOC)
	}

	// With an anchor, find the heading block with that anchor and take from
	// there to the end (or to the next heading with an id). This handles the
	// "one XHTML file, many chapters" pattern.
	if entry.SrcAnchor != "" {
		return chapterFromAnchor(entry, items, tocIdx)
	}

	return chapterFromBlocks(entry.Title, items, tocIdx, StrategyTOC)
}

// chapterFromAnchor builds a Chapter from the blocks between the heading with
// the TOC entry's anchor and the next heading with a different anchor. If the
// chapterFromAnchor creates a chapter from the TOC entry's anchored section.
// It includes content from the matching heading through the next heading with a
// different nonempty anchor, or all blocks from the first matching item when the
// anchor is not found.
func chapterFromAnchor(entry epub.TOCEntry, items []SpineItem, tocIdx int) *Chapter {
	blocks := items[0].Blocks
	start := -1
	for i, b := range blocks {
		if b.Kind == epub.BlockKindHeading && b.Anchor == entry.SrcAnchor {
			start = i
			break
		}
	}
	if start < 0 {
		// Anchor not found; fall back to all blocks of the item.
		slog.Default().Warn("TOC anchor not found in blocks, using whole item",
			"title", entry.Title, "anchor", entry.SrcAnchor)
		return chapterFromBlocks(entry.Title, items, tocIdx, StrategyTOC)
	}
	// Find the next heading with a different anchor (chapter boundary).
	end := len(blocks)
	for i := start + 1; i < len(blocks); i++ {
		if blocks[i].Kind == epub.BlockKindHeading && blocks[i].Anchor != "" &&
			blocks[i].Anchor != entry.SrcAnchor {
			end = i
			break
		}
	}
	text := blocksToText(blocks[start:end])
	return &Chapter{
		Title:             entry.Title,
		Source:            text,
		SectionIDs:        []string{items[0].ID},
		TOCEntryIndex:     tocIdx,
		DetectionStrategy: StrategyTOC,
	}
}

// chapterFromBlocks builds a Chapter from one or more complete spine items.
func chapterFromBlocks(title string, items []SpineItem, tocIdx int, strategy Strategy) *Chapter {
	var allBlocks []epub.Block
	var ids []string
	for _, si := range items {
		allBlocks = append(allBlocks, si.Blocks...)
		ids = append(ids, si.ID)
	}
	return &Chapter{
		Title:             title,
		Source:            blocksToText(allBlocks),
		SectionIDs:        ids,
		TOCEntryIndex:     tocIdx,
		DetectionStrategy: strategy,
	}
}

// SplitByHeadings scans all spine blocks for h1/h2 headings that classify as
// Keep. Each such heading starts a new chapter; content runs until the next
// kept heading. Spine items with no kept heading are skipped (recorded).
// SplitByHeadings creates chapters from level-one and level-two headings in the spine, recording unassociated spine items as skipped sections.
func SplitByHeadings(in SplitInput) (*SplitResult, error) {
	res := &SplitResult{}
	var cur *headingPending
	flush := func() {
		if cur == nil || len(cur.blocks) == 0 {
			return
		}
		res.Chapters = append(res.Chapters, Chapter{
			Title:             cur.title,
			Source:            blocksToText(cur.blocks),
			SectionIDs:        cur.ids,
			TOCEntryIndex:     cur.tocIdx,
			DetectionStrategy: StrategyHeading,
			Status:            chapterStatusRaw,
		})
	}

	for _, si := range in.Spine {
		hasKeptHeading := processHeadingBlocks(si, &cur, flush)
		// If a spine item had no kept heading and nothing is pending, it's
		// front/back matter — record as skipped.
		if !hasKeptHeading && cur == nil && len(si.Blocks) > 0 {
			res.Skipped = append(res.Skipped, skipSectionFromSpineItem(si))
		}
	}
	flush()

	// Assign sequential IDs.
	for i := range res.Chapters {
		res.Chapters[i].ID = i + 1
	}
	res.Index = Index{ChapterCount: len(res.Chapters)}
	return res, nil
}

// headingPending accumulates blocks for the chapter currently being built by
// SplitByHeadings.
type headingPending struct {
	title  string
	ids    []string
	blocks []epub.Block
	tocIdx int
}

// processHeadingBlocks scans one spine item's blocks for kept h1/h2 headings.
// Each kept heading flushes the current pending chapter and starts a new one.
// Non-heading blocks are appended to the current pending chapter (if any).
// processHeadingBlocks processes a spine item's blocks, starting a pending
// chapter for each kept level-one or level-two heading and adding other blocks
// to the current chapter. It returns whether the item contains a kept heading.
func processHeadingBlocks(si SpineItem, cur **headingPending, flush func()) bool {
	hasKeptHeading := false
	for _, b := range si.Blocks {
		if b.Kind == epub.BlockKindHeading && (b.Level == 1 || b.Level == 2) && IsKeep(b.Text) {
			flush()
			*cur = &headingPending{title: b.Text, ids: []string{si.ID}, tocIdx: -1}
			hasKeptHeading = true
			continue
		}
		if *cur != nil {
			(*cur).blocks = append((*cur).blocks, b)
			// Accumulate section ids without duplicating.
			if !contains((*cur).ids, si.ID) {
				(*cur).ids = append((*cur).ids, si.ID)
			}
		}
	}
	return hasKeptHeading
}

// skipSectionFromSpineItem classifies a spine item with no kept heading as a
// skipSectionFromSpineItem creates skipped-section metadata for a spine item.
// It uses the first heading as the title, or the item ID when no heading is present,
// and records ambiguous classifications with the dedicated ambiguous-skip reason.
func skipSectionFromSpineItem(si SpineItem) SkippedSection {
	title := firstHeadingText(si.Blocks)
	if title == "" {
		title = si.ID
	}
	reason := ClassifyTitle(title)
	rule := reason.Rule
	if reason.Decision == DecisionAmbiguous {
		rule = reasonAmbiguousSkip
	}
	return SkippedSection{
		Title:    title,
		Reason:   rule,
		SrcFile:  si.Href,
		Snippet:  blocksSnippet(si.Blocks),
		Strategy: StrategyHeading,
	}
}

// SplitPerItem is the last-resort strategy: every spine item with text becomes
// a chapter. This guarantees output but produces poor results for multi-file
// SplitPerItem creates one raw chapter for each nonempty spine item, using its
// first heading as the title or the item ID when no heading is present. It is
// intended for per-item splitting of single-file multi-chapter books.
func SplitPerItem(in SplitInput) (*SplitResult, error) {
	res := &SplitResult{}
	id := 0
	for _, si := range in.Spine {
		if len(si.Blocks) == 0 {
			continue
		}
		title := firstHeadingText(si.Blocks)
		if title == "" {
			title = si.ID
		}
		id++
		res.Chapters = append(res.Chapters, Chapter{
			ID:                id,
			Title:             title,
			Source:            blocksToText(si.Blocks),
			SectionIDs:        []string{si.ID},
			TOCEntryIndex:     -1,
			DetectionStrategy: StrategyPerItem,
			Status:            chapterStatusRaw,
		})
	}
	res.Index = Index{ChapterCount: len(res.Chapters)}
	return res, nil
}

// --- helpers ---

// blocksToText joins block texts into a single string, paragraphs separated
// by blank lines. Headings are included as their text (the chapter title is
// blocksToText converts supported content blocks to text, including image alt text.
// It separates each included block with a blank line.
func blocksToText(blocks []epub.Block) string {
	var parts []string
	for _, b := range blocks {
		switch b.Kind {
		case epub.BlockKindHeading, "paragraph", "list_item", "pre", "blockquote":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "image":
			if b.Alt != "" {
				parts = append(parts, "[image: "+b.Alt+"]")
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// blocksSnippet returns the text from the provided blocks, truncated to the configured snippet length when necessary.
func blocksSnippet(blocks []epub.Block) string {
	text := blocksToText(blocks)
	if len(text) > minHeadingLength {
		return text[:minHeadingLength] + "..."
	}
	return text
}

func snippetForTOC(in SplitInput, entry epub.TOCEntry) string {
	for _, si := range in.Spine {
		if basename(si.Href) == basename(entry.SrcFile) {
			return blocksSnippet(si.Blocks)
		}
	}
	return ""
}

// firstHeadingText returns the text of the first heading block, or an empty string if no heading is present.
func firstHeadingText(blocks []epub.Block) string {
	for _, b := range blocks {
		if b.Kind == epub.BlockKindHeading {
			return b.Text
		}
	}
	return ""
}

// basename returns the final component of a path after normalizing directory separators.
func basename(path string) string {
	// Strip directory, keep filename. Works for both / and \ separators.
	path = strings.ReplaceAll(path, "\\", "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func contains(slice []string, s string) bool {
	return slices.Contains(slice, s)
}
