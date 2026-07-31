package cli

import (
	"context"
	"encoding/json"
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
		force     bool
		cont      bool
		chapter   int
		chRange   string
		skipMem   bool
		skipGloss bool
		pollInt   int
	)
	cmd := &cobra.Command{
		Use:   "translate",
		Short: "Translate chapters to the target language via OpenAI Batch API",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runTranslate(ctx, proj, force, cont, chapter, chRange, skipMem, skipGloss, pollInt)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-translate chapters whose translation already exists")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume polling an interrupted batch")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "translate only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "translate a range of chapter ids, e.g. 5-12")
	cmd.Flags().BoolVar(&skipMem, "skip-memory", false, "skip summary generation (faster, less context continuity)")
	cmd.Flags().BoolVar(&skipGloss, "skip-glossary-update", false, "skip new-term extraction after each chapter")
	cmd.Flags().IntVar(&pollInt, "poll-interval", 60, "seconds between batch status polls")
	return cmd
}

// runTranslate orchestrates the batch-based translation flow:
//  1. If --continue: load existing batch state and jump to polling.
//  2. Otherwise: build JSONL with one translation request per chapter, upload, create batch.
//  3. Poll batch status every pollInt seconds until terminal.
//  4. Download results, write translation files, run optional post-processing.
func runTranslate(ctx context.Context, proj *project.Project, force, cont bool, chapter int, chRange string, skipMem, skipGloss bool, pollInt int) error {
	if pollInt < 10 {
		pollInt = 60
	}

	// --continue: resume polling an existing batch.
	if cont {
		return resumeTranslateBatch(ctx, proj, pollInt, skipMem, skipGloss)
	}

	// Check for an existing pending batch.
	if state, _ := translation.LoadBatchState(proj.AIDir(), "translate"); state != nil {
		if !translation.IsTerminalStatus(state.Status) {
			slog.Info("found pending translate batch, resuming polling (use --force to start a new one)",
				"batch_id", state.BatchID, "status", state.Status)
			return pollTranslateBatch(ctx, proj, state, pollInt, skipMem, skipGloss)
		}
		slog.Info("cleaning up completed batch state from previous run", "batch_id", state.BatchID)
		_ = translation.DeleteBatchState(proj.AIDir(), "translate")
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
	if err != nil {
		return err
	}
	writeAll := ids == nil

	// Filter chapters that need translation.
	var toTranslate []chapters.Chapter
	skipped := 0
	targetLang := proj.Cfg.Languages.Target
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
	if len(toTranslate) == 0 {
		slog.Info("no chapters to translate", "skipped", skipped)
		return nil
	}
	slog.Info("chapters to translate", "count", len(toTranslate), "skipped", skipped)

	// Load the glossary + characters and merge them for translation context.
	glossary, err := translation.LoadGlossary(proj.AIDir())
	if err != nil {
		return err
	}
	characters, err := translation.LoadCharacters(proj.AIDir())
	if err != nil {
		slog.Warn("failed to load characters, continuing with glossary only", "error", err)
		characters = &translation.Characters{}
	}
	glossary = glossary.WithCharacters(characters)
	if len(glossary.Terms) == 0 {
		slog.Warn("no glossary found — translations may be inconsistent. Run `bookai analyze` first.")
	}

	// Build batch requests: one per chapter.
	model := proj.Cfg.OpenAI.TranslationModel
	glossaryBlock := glossary.PromptBlock()
	reqs := make([]translation.BatchRequest, len(toTranslate))
	chapterIDs := make([]int, len(toTranslate))
	for i, ch := range toTranslate {
		// Build the system prompt: persona + glossary + previous summaries.
		prevContext := ""
		if !skipMem {
			prevContext, err = translation.PreviousSummaries(proj.MemoryDir(), ch.ID, 2)
			if err != nil {
				return fmt.Errorf("load previous summaries: %w", err)
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

	// Build JSONL.
	jsonlData, err := translation.BuildJSONL(reqs)
	if err != nil {
		return fmt.Errorf("build batch JSONL: %w", err)
	}
	slog.Info("built batch input", "requests", len(reqs), "jsonl_bytes", len(jsonlData), "model", model)

	// Create the batch client and submit.
	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return err
	}

	batchID, inputFileID, err := batchClient.SubmitBatch(ctx, jsonlData, map[string]string{
		"type":    "translate",
		"project": proj.Cfg.Project,
	})
	if err != nil {
		return err
	}
	slog.Info("batch submitted", "batch_id", batchID, "input_file_id", inputFileID)

	// Save batch state.
	state := &translation.BatchState{
		BatchID:     batchID,
		InputFileID: inputFileID,
		Type:        "translate",
		Model:       model,
		Endpoint:    "/v1/responses",
		Status:      "validating",
		ChapterIDs:  chapterIDs,
		CreatedAt:   time.Now(),
	}
	if err := translation.SaveBatchState(proj.AIDir(), state); err != nil {
		return fmt.Errorf("save batch state: %w", err)
	}

	// Poll until completion.
	return pollTranslateBatch(ctx, proj, state, pollInt, skipMem, skipGloss)
}

// resumeTranslateBatch loads the persisted batch state and resumes polling.
func resumeTranslateBatch(ctx context.Context, proj *project.Project, pollInt int, skipMem, skipGloss bool) error {
	state, err := translation.LoadBatchState(proj.AIDir(), "translate")
	if err != nil {
		return fmt.Errorf("load batch state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no pending translate batch found — run `bookai translate` without --continue to start a new one")
	}
	slog.Info("resuming batch polling", "batch_id", state.BatchID, "status", state.Status)
	return pollTranslateBatch(ctx, proj, state, pollInt, skipMem, skipGloss)
}

// pollTranslateBatch polls the batch status every pollInt seconds. When the
// batch reaches a terminal status, it downloads results and processes them.
func pollTranslateBatch(ctx context.Context, proj *project.Project, state *translation.BatchState, pollInt int, skipMem, skipGloss bool) error {
	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return err
	}

	for {
		if ctx.Err() != nil {
			slog.Info("interrupted by signal", "batch_id", state.BatchID, "last_status", state.Status)
			return ctx.Err()
		}

		info, err := batchClient.PollBatch(ctx, state.BatchID)
		if err != nil {
			return fmt.Errorf("poll batch: %w", err)
		}

		state.Status = info.Status
		state.OutputFileID = info.OutputFileID
		state.ErrorFileID = info.ErrorFileID
		state.Total = info.Total
		state.Completed = info.Completed
		state.Failed = info.Failed
		_ = translation.SaveBatchState(proj.AIDir(), state)

		slog.Info("batch status",
			"batch_id", state.BatchID, "status", info.Status,
			"completed", info.Completed, "failed", info.Failed, "total", info.Total)

		if translation.IsTerminalStatus(info.Status) {
			break
		}

		slog.Info("waiting for batch", "poll_seconds", pollInt)
		select {
		case <-ctx.Done():
			slog.Info("interrupted during poll wait", "batch_id", state.BatchID)
			return ctx.Err()
		case <-time.After(time.Duration(pollInt) * time.Second):
		}
	}

	switch state.Status {
	case "completed":
		return processTranslateResults(ctx, proj, state, batchClient, skipMem, skipGloss)
	case "failed":
		return fmt.Errorf("batch %s failed — check OpenAI dashboard for details", state.BatchID)
	case "expired":
		return fmt.Errorf("batch %s expired before completion", state.BatchID)
	case "cancelled":
		return fmt.Errorf("batch %s was cancelled", state.BatchID)
	default:
		return fmt.Errorf("batch %s ended in unexpected status: %s", state.BatchID, state.Status)
	}
}

// processTranslateResults downloads the batch output, writes translation files,
// updates chapter status, and runs optional post-processing (summaries,
// new-term extraction).
func processTranslateResults(ctx context.Context, proj *project.Project, state *translation.BatchState, batchClient *translation.BatchClient, skipMem, skipGloss bool) error {
	slog.Info("downloading batch results", "batch_id", state.BatchID, "output_file_id", state.OutputFileID)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download results: %w", err)
	}

	targetLang := proj.Cfg.Languages.Target
	translated := 0
	failedCount := 0

	// Write translation files.
	for _, res := range results {
		chID := translation.SplitCustomID(res.CustomID, "translate")
		if chID == 0 {
			slog.Warn("unrecognized custom_id in batch output", "custom_id", res.CustomID)
			continue
		}
		if res.Error != "" {
			slog.Warn("translation failed in batch", "chapter", chID, "error", res.Error)
			failedCount++
			continue
		}

		translationPath := translationPath(proj.TranslationDir(), chID, targetLang)
		if err := project.SaveBytes(translationPath, []byte(strings.TrimSpace(res.Content)+"\n")); err != nil {
			return fmt.Errorf("write translation for chapter %d: %w", chID, err)
		}

		// Update chapter status.
		chPath := filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", chID))
		var ch chapters.Chapter
		if err := project.LoadJSON(chPath, &ch); err != nil {
			slog.Warn("failed to load chapter for status update", "chapter", chID, "error", err)
		} else {
			ch.Status = "translated"
			if err := project.SaveJSON(chPath, ch); err != nil {
				slog.Warn("failed to update chapter status", "chapter", chID, "error", err)
			}
		}
		translated++
		slog.Info("translation written", "chapter", chID, "path", translationPath)
	}

	slog.Info("translations saved", "translated", translated, "failed", failedCount)

	// Post-processing: summaries and new-term extraction (live calls).
	if !skipMem || !skipGloss {
		if err := postProcessTranslations(ctx, proj, results, skipMem, skipGloss); err != nil {
			slog.Warn("post-processing encountered errors", "error", err)
		}
	}

	// Clean up batch state.
	_ = translation.DeleteBatchState(proj.AIDir(), "translate")
	slog.Info("translate batch complete", "batch_id", state.BatchID, "translated", translated, "failed", failedCount)

	return nil
}

// postProcessTranslations runs summary generation and new-term extraction as
// live (non-batch) calls for each successfully translated chapter.
func postProcessTranslations(ctx context.Context, proj *project.Project, results []translation.BatchRequestResult, skipMem, skipGloss bool) error {
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}

	// Load glossary for term merging.
	glossary, err := translation.LoadGlossary(proj.AIDir())
	if err != nil {
		return err
	}
	characters, err := translation.LoadCharacters(proj.AIDir())
	if err != nil {
		characters = &translation.Characters{}
	}
	glossary = glossary.WithCharacters(characters)

	for _, res := range results {
		if res.Error != "" {
			continue
		}
		chID := translation.SplitCustomID(res.CustomID, "translate")
		if chID == 0 {
			continue
		}

		// Load the chapter source for summary/term extraction.
		chPath := filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", chID))
		var ch chapters.Chapter
		if err := project.LoadJSON(chPath, &ch); err != nil {
			slog.Warn("failed to load chapter for post-processing", "chapter", chID, "error", err)
			continue
		}

		// Step 2: generate summary.
		if !skipMem {
			summaryResp, err := client.Chat(ctx, translation.ChatRequest{
				System: translation.SummarySystem,
				User:   translation.SummaryUser(translation.ChapterInfo{ID: ch.ID, Title: ch.Title}, ch.Source),
				Model:  client.HelperModel(),
			})
			if err != nil {
				slog.Warn("failed to generate summary", "chapter", chID, "error", err)
			} else if err := translation.SaveSummary(proj.MemoryDir(), chID, summaryResp.Content); err != nil {
				slog.Warn("failed to save summary", "chapter", chID, "error", err)
			}
		}

		// Step 3: extract new glossary terms.
		if !skipGloss {
			newTermsResp, err := client.Chat(ctx, translation.ChatRequest{
				System:   translation.NewTermsSystem,
				User:     translation.NewTermsUser(translation.ChapterInfo{ID: ch.ID, Title: ch.Title}, ch.Source, res.Content),
				Model:    client.HelperModel(),
				JSONMode: true,
			})
			if err != nil {
				slog.Warn("failed to extract new terms", "chapter", chID, "error", err)
				continue
			}
			var newTerms struct {
				Terms []translation.GlossaryTerm `json:"terms"`
			}
			if err := json.Unmarshal([]byte(newTermsResp.Content), &newTerms); err != nil {
				slog.Warn("failed to parse new terms JSON", "chapter", chID, "error", err)
				continue
			}
			added := glossary.Merge(newTerms.Terms)
			if added > 0 {
				if err := glossary.Save(proj.AIDir()); err != nil {
					slog.Warn("failed to save updated glossary", "error", err)
				} else {
					slog.Info("glossary updated", "new_terms", added, "chapter", chID)
				}
			}
		}

		if ctx.Err() != nil {
			slog.Info("interrupted during post-processing", "chapter", chID)
			return ctx.Err()
		}
	}

	return nil
}

// translationPath returns the path for a chapter's translation file.
func translationPath(translationDir string, chapterID int, targetLang string) string {
	return filepath.Join(translationDir, fmt.Sprintf("chapter_%03d.%s.txt", chapterID, targetLang))
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
