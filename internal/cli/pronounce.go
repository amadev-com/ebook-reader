package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/config"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
	"ebook-reader/internal/tts"
)

// newPronounceCmd implements `bookai pronounce`: scans each chapter's
// translation for words that XTTS v2 will likely mispronounce and generates
// phonetic respellings via the OpenAI Batch API. Each chapter is an
// independent batch item — the model sees the full chapter text and returns
// a list of {term, respelled} pairs. Results are saved per-chapter to
// ai/respelling_NNN.json.
//
// The command supports a --continue flag to resume polling an interrupted
// batch. Batch state is persisted locally in ai/batch_pronounce.json.
func newPronounceCmd() *cobra.Command {
	var (
		force   bool
		cont    bool
		chapter int
		chRange string
		pollInt int
	)
	cmd := &cobra.Command{
		Use:   "pronounce",
		Short: "Generate XTTS v2 respellings per chapter via OpenAI Batch API",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runPronounce(ctx, proj, force, cont, chapter, chRange, pollInt)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-generate respellings even if they already exist")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume polling an interrupted batch")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "respell only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "respell a range of chapter ids, e.g. 5-12")
	cmd.Flags().IntVar(&pollInt, "poll-interval", 60, "seconds between batch status polls")
	return cmd
}

// runPronounce orchestrates the batch-based respelling flow:
//  1. If --continue: load existing batch state and jump to polling.
//  2. Otherwise: build JSONL with one request per chapter, upload, create batch.
//  3. Poll batch status every pollInt seconds until terminal.
//  4. Download results, save per-chapter respelling files.
func runPronounce(ctx context.Context, proj *project.Project, force, cont bool, chapter int, chRange string, pollInt int) error {
	if pollInt < 10 {
		pollInt = 60
	}

	// --continue: resume polling an existing batch.
	if cont {
		return resumePronounceBatch(ctx, proj, pollInt)
	}

	// Check for an existing pending batch.
	if state, _ := translation.LoadBatchState(proj.AIDir(), "pronounce"); state != nil {
		if !translation.IsTerminalStatus(state.Status) {
			slog.Info("found pending pronounce batch, resuming polling (use --force to start a new one)",
				"batch_id", state.BatchID, "status", state.Status)
			return pollPronounceBatch(ctx, proj, state, pollInt)
		}
		slog.Info("cleaning up completed batch state from previous run", "batch_id", state.BatchID)
		_ = translation.DeleteBatchState(proj.AIDir(), "pronounce")
	}

	// Load all chapters.
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	// Determine which chapters to respell.
	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil {
		return err
	}
	writeAll := ids == nil

	// Filter chapters that have translations and need respelling.
	targetLang := proj.Cfg.Languages.Target
	var toRespell []chapters.Chapter
	skipped := 0
	for _, ch := range chs {
		if !writeAll && !ids[ch.ID] {
			continue
		}
		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		if !project.Exists(translationPath) {
			skipped++
			continue
		}
		respPath := filepath.Join(proj.AIDir(), fmt.Sprintf("respelling_%03d.json", ch.ID))
		if project.Exists(respPath) && !force {
			skipped++
			continue
		}
		toRespell = append(toRespell, ch)
	}
	if len(toRespell) == 0 {
		slog.Info("no chapters to respell", "skipped", skipped)
		return nil
	}
	slog.Info("chapters to respell", "count", len(toRespell), "skipped", skipped)

	// Build batch requests: one per chapter.
	model := proj.Cfg.OpenAI.HelperModel
	overrides := proj.Cfg.Pronunciation
	reqs := make([]translation.BatchRequest, len(toRespell))
	chapterIDs := make([]int, len(toRespell))
	for i, ch := range toRespell {
		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		text, err := os.ReadFile(translationPath)
		if err != nil {
			return fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
		}

		reqs[i] = translation.BatchRequest{
			CustomID:     fmt.Sprintf("pronounce-chapter-%d", ch.ID),
			Instructions: translation.RespellingSystem,
			Input:        translation.RespellingUser(string(text), overrides),
			Model:        model,
			JSONMode:     true,
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
		"type":    "pronounce",
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
		Type:        "pronounce",
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
	return pollPronounceBatch(ctx, proj, state, pollInt)
}

// resumePronounceBatch loads the persisted batch state and resumes polling.
func resumePronounceBatch(ctx context.Context, proj *project.Project, pollInt int) error {
	state, err := translation.LoadBatchState(proj.AIDir(), "pronounce")
	if err != nil {
		return fmt.Errorf("load batch state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no pending pronounce batch found — run `bookai pronounce` without --continue to start a new one")
	}
	slog.Info("resuming batch polling", "batch_id", state.BatchID, "status", state.Status)
	return pollPronounceBatch(ctx, proj, state, pollInt)
}

// pollPronounceBatch polls the batch status every pollInt seconds. When the
// batch reaches a terminal status, it downloads results and processes them.
func pollPronounceBatch(ctx context.Context, proj *project.Project, state *translation.BatchState, pollInt int) error {
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
		return processPronounceResults(ctx, proj, state, batchClient)
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

// processPronounceResults downloads the batch output and saves per-chapter
// respelling files (ai/respelling_NNN.json).
func processPronounceResults(ctx context.Context, proj *project.Project, state *translation.BatchState, batchClient *translation.BatchClient) error {
	slog.Info("downloading batch results", "batch_id", state.BatchID, "output_file_id", state.OutputFileID)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download results: %w", err)
	}

	respelled := 0
	failedCount := 0

	for _, res := range results {
		chID := translation.SplitCustomID(res.CustomID, "pronounce")
		if chID == 0 {
			slog.Warn("unrecognized custom_id in batch output", "custom_id", res.CustomID)
			continue
		}
		if res.Error != "" {
			slog.Warn("respelling failed in batch", "chapter", chID, "error", res.Error)
			failedCount++
			continue
		}

		// Parse the JSON response.
		var result respellingResult
		if err := json.Unmarshal([]byte(res.Content), &result); err != nil {
			slog.Warn("failed to parse respelling JSON", "chapter", chID, "error", err, "content", truncate(res.Content, 200))
			failedCount++
			continue
		}

		// Apply config overrides.
		applyRespellingOverrides(&result, proj.Cfg.Pronunciation)

		// Save per-chapter respelling file.
		re := &tts.Respelling{Entries: result.Entries}
		if err := re.SaveChapterRespelling(proj.AIDir(), chID); err != nil {
			return fmt.Errorf("save respelling for chapter %d: %w", chID, err)
		}

		slog.Info("respellings saved", "chapter", chID, "entries", len(re.Entries))
		respelled++
	}

	// Clean up batch state.
	_ = translation.DeleteBatchState(proj.AIDir(), "pronounce")
	slog.Info("pronounce batch complete", "batch_id", state.BatchID, "respelled", respelled, "failed", failedCount)

	return nil
}

// respellingResult is the JSON shape we expect from the model.
type respellingResult struct {
	Entries []tts.RespellingEntry `json:"entries"`
}

// applyRespellingOverrides forces the respelling of any entry that matches a
// config pronunciation override. Config entries not found by the model are
// added as new entries.
func applyRespellingOverrides(result *respellingResult, overrides []config.PronunciationOverride) {
	if len(overrides) == 0 {
		return
	}
	overridden := 0
	for i := range result.Entries {
		for _, ov := range overrides {
			if strings.EqualFold(result.Entries[i].Term, ov.Term) {
				result.Entries[i].Respelled = ov.Phonemes
				overridden++
				break
			}
		}
	}
	for _, ov := range overrides {
		found := false
		for _, e := range result.Entries {
			if strings.EqualFold(e.Term, ov.Term) {
				found = true
				break
			}
		}
		if !found && ov.Term != "" && ov.Phonemes != "" {
			result.Entries = append(result.Entries, tts.RespellingEntry{
				Term:      ov.Term,
				Respelled: ov.Phonemes,
			})
			overridden++
		}
	}
	if overridden > 0 {
		slog.Info("applied respelling overrides", "overridden", overridden, "config_entries", len(overrides))
	}
}
