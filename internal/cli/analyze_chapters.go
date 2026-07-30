package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/epub"
	"ebook-reader/internal/project"
)

// newAnalyzeChaptersCmd implements `bookai analyze-chapters`. It reads the
// extracted/ artifacts, runs the chapter splitter, and writes chapters/*.json.
func newAnalyzeChaptersCmd() *cobra.Command {
	var (
		force    bool
		chapter  int
		chRange  string
		strategy string
		strip    []string
	)
	cmd := &cobra.Command{
		Use:   "analyze-chapters",
		Short: "Detect chapters from extracted spine/TOC and write chapters/*.json",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runAnalyzeChapters(ctx, proj, force, chapter, chRange, strategy, strip)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-analyze chapters whose JSON already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "analyze only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "analyze a range of chapter ids, e.g. 5-12")
	cmd.Flags().StringVar(&strategy, "strategy", "", "force detection strategy: toc|heading|per-item")
	cmd.Flags().StringArrayVar(&strip, "strip", nil, "remove all occurrences of this text from chapter source (repeatable)")
	return cmd
}

func runAnalyzeChapters(_ context.Context, proj *project.Project, force bool, chapter int, chRange, strategy string, strip []string) error {
	extractedDir := proj.ExtractedDir()
	spinePath := filepath.Join(extractedDir, "spine.json")
	if !project.Exists(spinePath) {
		return fmt.Errorf("no extracted/spine.json found — run `bookai import` first")
	}

	// Load spine + blocks + toc into SplitInput.
	in, err := loadSplitInput(extractedDir)
	if err != nil {
		return err
	}
	slog.Info("loaded extracted artifacts", "spine_items", len(in.Spine), "toc_entries", len(in.TOC))

	// Run the splitter. If --strategy is set, force that single strategy.
	var res *chapters.SplitResult
	if strategy != "" {
		res, err = splitWithStrategy(in, chapters.Strategy(strategy))
	} else {
		res, err = chapters.Split(in)
	}
	if err != nil {
		return err
	}
	slog.Info("chapter detection complete",
		"strategy", res.Index.Strategy, "chapters", res.Index.ChapterCount, "skipped", len(res.Skipped))

	// Apply --strip filters: each strip string is a "trigger" — if it appears
	// in the last 400 chars of a chapter's source, the chapter is truncated from
	// the last "***" separator (3+ stars) before the trigger to the end. This
	// removes promotional notices, author notes, and other boilerplate that
	// appears after a *** separator at the end of chapters.
	if len(strip) > 0 {
		stripped := 0
		for i := range res.Chapters {
			orig := res.Chapters[i].Source
			cleaned := orig
			for _, s := range strip {
				cleaned = stripTrailer(cleaned, s)
			}
			cleaned = strings.TrimSpace(cleaned)
			if cleaned != orig {
				res.Chapters[i].Source = cleaned
				stripped++
			}
		}
		slog.Info("applied strip filters", "patterns", len(strip), "chapters_modified", stripped)
	}

	// Determine which chapter ids to write (default: all).
	ids, err := parseChapterFilter(chapter, chRange, res.Index.ChapterCount)
	if err != nil {
		return err
	}
	writeAll := ids == nil // nil means no filter -> write all

	chaptersDir := proj.ChaptersDir()
	if err := os.MkdirAll(chaptersDir, 0o755); err != nil {
		return fmt.Errorf("create chapters dir: %w", err)
	}

	written := 0
	for _, ch := range res.Chapters {
		if !writeAll && !ids[ch.ID] {
			continue
		}
		path := chapterPath(chaptersDir, ch.ID)
		if project.Exists(path) && !force {
			slog.Debug("skip existing chapter", "id", ch.ID)
			continue
		}
		if err := project.SaveJSON(path, ch); err != nil {
			return err
		}
		written++
	}
	slog.Info("wrote chapters", "count", written)

	// Always (re)write _skipped.json and _index.json so they reflect the
	// latest analysis run.
	if err := project.SaveJSON(filepath.Join(chaptersDir, "_skipped.json"), res.Skipped); err != nil {
		return err
	}
	if err := project.SaveJSON(filepath.Join(chaptersDir, "_index.json"), res.Index); err != nil {
		return err
	}
	return nil
}

// loadSplitInput reads spine.json, toc.json, and blocks/itemNNN.json from the
// extracted directory and assembles a chapters.SplitInput.
func loadSplitInput(extractedDir string) (chapters.SplitInput, error) {
	var spine []spineEntry
	if err := project.LoadJSON(filepath.Join(extractedDir, "spine.json"), &spine); err != nil {
		return chapters.SplitInput{}, fmt.Errorf("load spine: %w", err)
	}
	var toc []epub.TOCEntry
	if err := project.LoadJSON(filepath.Join(extractedDir, "toc.json"), &toc); err != nil {
		return chapters.SplitInput{}, fmt.Errorf("load toc: %w", err)
	}
	items := make([]chapters.SpineItem, 0, len(spine))
	for i, entry := range spine {
		blockPath := filepath.Join(extractedDir, "blocks", fmt.Sprintf("item%03d.json", i))
		if !project.Exists(blockPath) {
			slog.Warn("missing blocks file for spine item", "index", i, "id", entry.ID)
			items = append(items, chapters.SpineItem{Index: i, ID: entry.ID, Href: entry.Href})
			continue
		}
		var ib itemBlocks
		if err := project.LoadJSON(blockPath, &ib); err != nil {
			return chapters.SplitInput{}, fmt.Errorf("load blocks for item %d: %w", i, err)
		}
		items = append(items, chapters.SpineItem{
			Index:  i,
			ID:     entry.ID,
			Href:   entry.Href,
			Blocks: ib.Blocks,
		})
	}
	return chapters.SplitInput{Spine: items, TOC: toc}, nil
}

// splitWithStrategy forces a single strategy instead of the auto-fallback.
func splitWithStrategy(in chapters.SplitInput, s chapters.Strategy) (*chapters.SplitResult, error) {
	var res *chapters.SplitResult
	var err error
	switch s {
	case chapters.StrategyTOC:
		res, err = chapters.SplitByTOC(in)
	case chapters.StrategyHeading:
		res, err = chapters.SplitByHeadings(in)
	case chapters.StrategyPerItem:
		res, err = chapters.SplitPerItem(in)
	default:
		return nil, fmt.Errorf("unknown strategy %q (want toc|heading|per-item)", s)
	}
	if err != nil {
		return nil, err
	}
	res.Index.Strategy = s
	return res, nil
}

// parseChapterFilter interprets --chapter and --range. Returns nil if no
// filter is set (meaning "all chapters"). Otherwise returns a set keyed by
// chapter id (1-based).
func parseChapterFilter(chapter int, chRange string, maxID int) (map[int]bool, error) {
	if chapter == 0 && chRange == "" {
		return nil, nil
	}
	set := make(map[int]bool)
	if chapter != 0 {
		if chapter < 1 || chapter > maxID {
			return nil, fmt.Errorf("--chapter %d out of range (1-%d)", chapter, maxID)
		}
		set[chapter] = true
	}
	if chRange != "" {
		parts := strings.SplitN(chRange, "-", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("--range must be M-N, got %q", chRange)
		}
		var lo, hi int
		if _, err := fmt.Sscanf(parts[0], "%d", &lo); err != nil {
			return nil, fmt.Errorf("--range lower bound %q is not a number", parts[0])
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &hi); err != nil {
			return nil, fmt.Errorf("--range upper bound %q is not a number", parts[1])
		}
		if lo < 1 || hi > maxID || lo > hi {
			return nil, fmt.Errorf("--range %d-%d out of range (1-%d)", lo, hi, maxID)
		}
		for i := lo; i <= hi; i++ {
			set[i] = true
		}
	}
	return set, nil
}

func chapterPath(chaptersDir string, id int) string {
	return filepath.Join(chaptersDir, fmt.Sprintf("chapter_%03d.json", id))
}

// stripTrailer checks if the trigger string appears in the last 400 chars of
// text. If it does, it finds the last "***" separator (3+ consecutive stars)
// that appears before the trigger and removes everything from that separator
// to the end of the text. If no "***" separator is found before the trigger,
// the text is truncated at the trigger position instead.
func stripTrailer(text, trigger string) string {
	if trigger == "" || len(text) == 0 {
		return text
	}

	// Check only the last 400 chars for the trigger.
	tailStart := len(text) - 400
	if tailStart < 0 {
		tailStart = 0
	}
	tail := text[tailStart:]

	triggerIdx := strings.Index(strings.ToLower(tail), strings.ToLower(trigger))
	if triggerIdx < 0 {
		return text // trigger not found in tail
	}

	// Absolute position of the trigger in the full text.
	triggerAbs := tailStart + triggerIdx

	// Search backwards from the trigger for a "***" separator (3+ stars).
	// We look in the text up to the trigger position.
	cutFrom := triggerAbs
	for i := triggerAbs - 1; i >= 2; i-- {
		if text[i] == '*' && text[i-1] == '*' && text[i-2] == '*' {
			// Found a 3+ star separator. Walk back to include all leading
			// stars and any whitespace before them.
			cutFrom = i - 2
			for cutFrom > 0 && text[cutFrom-1] == '*' {
				cutFrom--
			}
			// Trim trailing whitespace/newlines before the separator.
			for cutFrom > 0 && (text[cutFrom-1] == '\n' || text[cutFrom-1] == '\r' || text[cutFrom-1] == ' ' || text[cutFrom-1] == '\t') {
				cutFrom--
			}
			break
		}
	}

	return text[:cutFrom]
}
