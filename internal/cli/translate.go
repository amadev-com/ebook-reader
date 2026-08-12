package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// prevSummaryCount is the number of previous chapter summaries loaded as
// context for each translation request.
const prevSummaryCount = 2

// newTranslateCmd implements `bookai translate`: translates chapters to the
// target language using the OpenAI Batch API for cost-effective processing.
// Each chapter is submitted as an independent batch item with the full
// glossary + previous chapter summaries as context. After the batch completes,
// translations are written to disk and optional post-processing (summaries,
// new-term extraction) runs as live calls.
//
// The command supports a --continue flag to resume polling an interrupted
// batch. Batch state is persisted locally in ai/batch_translate.json.
func newTranslateCmd() *cobra.Command {
	var (
		force   bool
		cont    bool
		chapter int
		chRange string
		skipMem bool
		pollInt int
	)
	cmd := &cobra.Command{
		Use:   "translate",
		Short: "Translate chapters to the target language via OpenAI Batch API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger()
			ctx := cmd.Context()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runTranslate(ctx, proj, force, cont, chapter, chRange, skipMem, pollInt)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-translate chapters whose translation already exists")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume polling an interrupted batch")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "translate only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "translate a range of chapter ids, e.g. 5-12")
	cmd.Flags().BoolVar(&skipMem, "skip-memory", false, "don't load previous chapter summaries as context")
	cmd.Flags().IntVar(&pollInt, "poll-interval", defaultPollInterval, "seconds between batch status polls")
	return cmd
}

// runTranslate orchestrates the batch-based translation flow:
//  1. If --continue: load existing batch state and jump to polling.
//  2. Otherwise: build JSONL with one translation request per chapter, upload, create batch.
//  3. Poll batch status every pollInt seconds until terminal.
//  4. Download results, write translation files.
//
// Summaries are generated in the analyze step (not here) so they're available
// as context at batch submission time. No post-processing is done after
// translation — the glossary is finalized by analyze.
func runTranslate(
	ctx context.Context,
	proj *project.Project,
	force, cont bool,
	chapter int,
	chRange string,
	skipMem bool,
	pollInt int,
) error {
	if pollInt < minPollInterval {
		pollInt = defaultPollInterval
	}

	// --continue: resume polling an existing batch.
	if cont {
		return resumeTranslateBatch(ctx, proj, pollInt)
	}

	// Check for an existing pending batch.
	if state, _ := translation.LoadBatchState(proj.AIDir(), translation.BatchTypeTranslate); state != nil {
		if !translation.IsTerminalStatus(state.Status) {
			slog.Default().
				InfoContext(ctx, "found pending translate batch, resuming polling (use --force to start a new one)",
					"batch_id", state.BatchID, "status", state.Status)
			return pollTranslateBatch(ctx, proj, state, pollInt)
		}
		slog.Default().
			InfoContext(ctx, "cleaning up completed batch state from previous run", "batch_id", state.BatchID)
		_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypeTranslate)
	}

	// Load all chapters to know the range.
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	// Determine which chapters to translate.
	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil && !errors.Is(err, errNoChapterFilter) {
		return err
	}
	writeAll := errors.Is(err, errNoChapterFilter)

	// Filter chapters that need translation.
	targetLang := proj.Cfg.Languages.Target
	toTranslate, skipped := filterTranslateChapters(chs, proj, ids, writeAll, targetLang, force)
	if len(toTranslate) == 0 {
		slog.Default().InfoContext(ctx, "no chapters to translate", "skipped", skipped)
		return nil
	}
	slog.Default().InfoContext(ctx, "chapters to translate", "count", len(toTranslate), "skipped", skipped)

	// Load the glossary + characters and merge them for translation context.
	glossary, err := translation.LoadGlossary(proj.AIDir())
	if err != nil {
		return err
	}
	characters, err := translation.LoadCharacters(proj.AIDir())
	if err != nil {
		slog.Default().WarnContext(ctx, "failed to load characters, continuing with glossary only", "error", err)
		characters = &translation.Characters{}
	}
	glossary = glossary.WithCharacters(characters)
	if len(glossary.Terms) == 0 {
		slog.Default().
			WarnContext(ctx, "no glossary found — translations may be inconsistent. Run `bookai analyze` first.")
	}

	// Build batch requests: one per chapter.
	model := proj.Cfg.OpenAI.TranslationModel
	reqs, chapterIDs, err := buildTranslateRequests(toTranslate, glossary, proj, model, skipMem)
	if err != nil {
		return err
	}

	state, err := submitTranslateBatch(ctx, proj, reqs, chapterIDs, model)
	if err != nil {
		return err
	}

	// Poll until completion.
	return pollTranslateBatch(ctx, proj, state, pollInt)
}

// submitTranslateBatch builds the JSONL, submits the batch to OpenAI, and
// persists the batch state.
func submitTranslateBatch(
	ctx context.Context,
	proj *project.Project,
	reqs []translation.BatchRequest,
	chapterIDs []int,
	model string,
) (*translation.BatchState, error) {
	jsonlData, err := translation.BuildJSONL(reqs)
	if err != nil {
		return nil, fmt.Errorf("build batch JSONL: %w", err)
	}
	slog.Default().
		InfoContext(ctx, "built batch input", "requests", len(reqs), "jsonl_bytes", len(jsonlData), "model", model)

	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return nil, err
	}

	batchID, inputFileID, err := batchClient.SubmitBatch(ctx, jsonlData, map[string]string{
		translation.BatchMetadataKeyType:    translation.BatchTypeTranslate,
		translation.BatchMetadataKeyProject: proj.Cfg.Project,
	})
	if err != nil {
		return nil, err
	}
	slog.Default().InfoContext(ctx, "batch submitted", "batch_id", batchID, "input_file_id", inputFileID)

	state := &translation.BatchState{
		BatchID:     batchID,
		InputFileID: inputFileID,
		Type:        translation.BatchTypeTranslate,
		Model:       model,
		Endpoint:    translation.BatchEndpoint,
		Status:      translation.BatchStatusValidating,
		ChapterIDs:  chapterIDs,
		CreatedAt:   time.Now(),
	}
	if err = translation.SaveBatchState(proj.AIDir(), state); err != nil {
		return nil, fmt.Errorf("save batch state: %w", err)
	}
	return state, nil
}

// filterTranslateChapters selects chapters that need translation (don't
// already have a translation file unless force is set). Returns the chapters
// to translate and the number skipped.
func filterTranslateChapters(
	chs []chapters.Chapter,
	proj *project.Project,
	ids map[int]bool,
	writeAll bool,
	targetLang string,
	force bool,
) ([]chapters.Chapter, int) {
	var toTranslate []chapters.Chapter
	skipped := 0
	for _, ch := range chs {
		if !writeAll && !ids[ch.ID] {
			continue
		}
		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		if project.Exists(translationPath) && !force {
			skipped++
			continue
		}
		toTranslate = append(toTranslate, ch)
	}
	return toTranslate, skipped
}

// buildTranslateRequests builds batch requests (one per chapter) with
// per-chapter glossary filtering and previous chapter summaries as context.
func buildTranslateRequests(
	toTranslate []chapters.Chapter,
	glossary *translation.Glossary,
	proj *project.Project,
	model string,
	skipMem bool,
) ([]translation.BatchRequest, []int, error) {
	reqs := make([]translation.BatchRequest, len(toTranslate))
	chapterIDs := make([]int, len(toTranslate))
	for i, ch := range toTranslate {
		// Build the system prompt: persona + glossary + previous summaries.
		// Use per-chapter glossary filtering: only send terms that were
		// encountered in this chapter (or legacy terms with no chapter tags).
		glossaryBlock := glossary.PromptBlockForChapter(ch.ID)

		prevContext := ""
		if !skipMem {
			var err error
			if prevContext, err = translation.PreviousSummaries(proj.MemoryDir(), ch.ID, prevSummaryCount); err != nil {
				return nil, nil, fmt.Errorf("load previous summaries: %w", err)
			}
		}
		systemPrompt := translation.System(glossaryBlock, prevContext)

		reqs[i] = translation.BatchRequest{
			CustomID:     fmt.Sprintf("translate-chapter-%d", ch.ID),
			Instructions: systemPrompt,
			Input:        translation.User(translation.ChapterInfo{ID: ch.ID, Title: ch.Title}, ch.Source),
			Model:        model,
		}
		chapterIDs[i] = ch.ID
	}
	return reqs, chapterIDs, nil
}

// resumeTranslateBatch loads the persisted batch state and resumes polling.
func resumeTranslateBatch(ctx context.Context, proj *project.Project, pollInt int) error {
	state, err := translation.LoadBatchState(proj.AIDir(), translation.BatchTypeTranslate)
	if err != nil {
		if errors.Is(err, translation.ErrBatchStateNotFound) {
			return fmt.Errorf(
				"no pending translate batch found — run `bookai translate` without --continue to start a new one",
			)
		}
		return fmt.Errorf("load batch state: %w", err)
	}
	slog.Default().InfoContext(ctx, "resuming batch polling", "batch_id", state.BatchID, "status", state.Status)
	return pollTranslateBatch(ctx, proj, state, pollInt)
}

// pollTranslateBatch polls the batch status every pollInt seconds. When the
// batch reaches a terminal status, it downloads results and processes them.
func pollTranslateBatch(ctx context.Context, proj *project.Project, state *translation.BatchState, pollInt int) error {
	batchClient, err := pollBatchUntilTerminal(ctx, proj, state, pollInt, "batch")
	if err != nil {
		return err
	}
	if state.Status != translation.BatchStatusCompleted {
		return batchTerminalError(state, "batch")
	}
	return processTranslateResults(ctx, proj, state, batchClient)
}

// processTranslateResults downloads the batch output and writes translation
// files. No post-processing is done — summaries are generated in the analyze
// step, and the glossary is finalized by analyze. Chapter status is updated
// to "translated".
func processTranslateResults(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	batchClient *translation.BatchClient,
) error {
	slog.Default().
		InfoContext(ctx, "downloading batch results", "batch_id", state.BatchID, "output_file_id", state.OutputFileID)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download results: %w", err)
	}

	targetLang := proj.Cfg.Languages.Target
	translated := 0
	failedCount := 0

	// Write translation files.
	for _, res := range results {
		chID := translation.SplitCustomID(res.CustomID, translation.BatchTypeTranslate)
		if chID == 0 {
			slog.Default().WarnContext(ctx, "unrecognized custom_id in batch output", "custom_id", res.CustomID)
			continue
		}
		if res.Error != "" {
			slog.Default().WarnContext(ctx, "translation failed in batch", "chapter", chID, "error", res.Error)
			failedCount++
			continue
		}

		translationPath := translationPath(proj.TranslationDir(), chID, targetLang)
		content := strings.TrimSpace(res.Content)
		content = deduplicateTitle(content)
		if err = project.SaveBytes(translationPath, []byte(content+"\n")); err != nil {
			return fmt.Errorf("write translation for chapter %d: %w", chID, err)
		}

		// Update chapter status.
		chPath := filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", chID))
		var ch chapters.Chapter
		if err = project.LoadJSON(chPath, &ch); err != nil {
			slog.Default().WarnContext(ctx, "failed to load chapter for status update", "chapter", chID, "error", err)
		} else {
			ch.Status = "translated"
			if err = project.SaveJSON(chPath, ch); err != nil {
				slog.Default().WarnContext(ctx, "failed to update chapter status", "chapter", chID, "error", err)
			}
		}
		translated++
		slog.Default().InfoContext(ctx, "translation written", "chapter", chID, "path", translationPath)
	}

	// Clean up batch state.
	_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypeTranslate)
	slog.Default().InfoContext(
		ctx, "translate batch complete",
		"batch_id", state.BatchID,
		"translated", translated,
		"failed", failedCount,
	)

	return nil
}

// translationPath returns the path for a chapter's translation file.
func translationPath(translationDir string, chapterID int, targetLang string) string {
	return filepath.Join(translationDir, fmt.Sprintf("chapter_%03d.%s.txt", chapterID, targetLang))
}

// deduplicateTitle removes a duplicated title line at the start of a
// translation. Some chapter sources begin with the title line, and the
// translation prompt used to repeat the title in the header — causing the
// model to output the title twice. This strips the duplicate: if the text
// starts with "Title\n\nTitle\n...", it becomes "Title\n\n...".
func deduplicateTitle(text string) string {
	// Find the first non-empty line.
	before, after, ok := strings.Cut(text, "\n")
	if !ok {
		return text
	}
	firstLine := strings.TrimSpace(before)
	if firstLine == "" {
		return text
	}

	// Skip the blank line(s) after the first line.
	rest := after
	rest = strings.TrimLeft(rest, "\n\r \t")
	if rest == "" {
		return text
	}

	// Check if the remaining text starts with the same line.
	before0, after0, ok0 := strings.Cut(rest, "\n")
	var secondLine string
	if !ok0 {
		secondLine = rest
	} else {
		secondLine = before0
	}
	secondLine = strings.TrimSpace(secondLine)

	if secondLine == firstLine {
		// Duplicate detected: keep the first line + blank line + rest after second line.
		if !ok0 {
			return firstLine
		}
		return firstLine + "\n\n" + strings.TrimLeft(after0, "\n\r \t")
	}
	return text
}

// loadTranslatedChapters returns the IDs of chapters that have a translation
// file. Used by verify-glossary.
func loadTranslatedChapters(proj *project.Project) ([]int, error) {
	indexPath := filepath.Join(proj.ChaptersDir(), "_index.json")
	if !project.Exists(indexPath) {
		return nil, fmt.Errorf("no chapters/_index.json found")
	}
	var idx chapters.Index
	if err := project.LoadJSON(indexPath, &idx); err != nil {
		return nil, err
	}
	var ids []int
	targetLang := proj.Cfg.Languages.Target
	for id := 1; id <= idx.ChapterCount; id++ {
		if project.Exists(translationPath(proj.TranslationDir(), id, targetLang)) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
