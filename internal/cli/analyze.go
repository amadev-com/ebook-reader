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
	"ebook-reader/internal/config"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// newAnalyzeCmd implements `bookai analyze`: extracts a glossary and character
// list from the book using the OpenAI Batch API for cost-effective processing.
// Each chapter is submitted as an independent batch item. After the batch
// completes, a single "live" (non-batch) request merges and unifies all
// per-chapter results into the final glossary.json + characters.json.
//
// The command supports a --continue flag to resume polling an interrupted
// batch. Batch state is persisted locally in ai/batch_analyze.json.
func newAnalyzeCmd() *cobra.Command {
	var (
		force   bool
		cont    bool
		chapter int
		chRange string
		pollInt int
	)
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Extract glossary and characters via OpenAI Batch API",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runAnalyze(ctx, proj, force, cont, chapter, chRange, pollInt)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-analyze even if glossary.json already exists")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume polling an interrupted batch")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "analyze only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "analyze a range of chapter ids, e.g. 5-12")
	cmd.Flags().IntVar(&pollInt, "poll-interval", 60, "seconds between batch status polls")
	return cmd
}

// runAnalyze orchestrates the batch-based glossary extraction flow:
//  1. If --continue: load existing batch state and jump to polling.
//  2. Otherwise: build JSONL with one request per chapter, upload, create batch.
//  3. Poll batch status every pollInt seconds until terminal.
//  4. Download results, send a live merge/unify request, apply overrides, save.
func runAnalyze(ctx context.Context, proj *project.Project, force, cont bool, chapter int, chRange string, pollInt int) error {
	if pollInt < 10 {
		pollInt = 60
	}

	glossaryPath := filepath.Join(proj.AIDir(), "glossary.json")

	// --continue: resume polling an existing batch.
	if cont {
		return resumeAnalyzeBatch(ctx, proj, pollInt)
	}

	// Fresh run: check if glossary already exists.
	if project.Exists(glossaryPath) && !force {
		slog.Info("glossary already exists, skipping (use --force to re-analyze)", "path", glossaryPath)
		return nil
	}

	// Check for an existing pending batch.
	if state, _ := translation.LoadBatchState(proj.AIDir(), "analyze"); state != nil {
		if !translation.IsTerminalStatus(state.Status) {
			slog.Info("found pending analyze batch, resuming polling (use --force to start a new one)",
				"batch_id", state.BatchID, "status", state.Status)
			return pollAnalyzeBatch(ctx, proj, state, pollInt)
		}
		slog.Info("cleaning up completed batch state from previous run", "batch_id", state.BatchID)
		_ = translation.DeleteBatchState(proj.AIDir(), "analyze")
	}

	// Load all chapters.
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	// Filter chapters if --chapter or --range is set.
	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil {
		return err
	}
	if ids != nil {
		filtered := chs[:0]
		for _, ch := range chs {
			if ids[ch.ID] {
				filtered = append(filtered, ch)
			}
		}
		chs = filtered
	}
	slog.Info("loaded chapters for analysis", "count", len(chs))

	// Build locked-translations string from config overrides.
	lockedTerms := buildLockedTerms(proj.Cfg.Glossary)

	// Build batch requests: one per chapter. Each request extracts the
	// glossary AND generates a chapter summary in a single pass — the model
	// reads the chapter once and returns both in one JSON response.
	model := proj.Cfg.OpenAI.HelperModel
	reqs := make([]translation.BatchRequest, len(chs))
	chapterIDs := make([]int, len(chs))
	for i, ch := range chs {
		reqs[i] = translation.BatchRequest{
			CustomID:     fmt.Sprintf("analyze-chapter-%d", ch.ID),
			Instructions: translation.GlossaryExtractionSystem,
			Input:        translation.GlossaryExtractionUser([]translation.ChapterText{{Title: ch.Title, Text: ch.Source}}, "", lockedTerms),
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
	slog.Info("built batch input", "requests", len(reqs), "chapters", len(chs), "jsonl_bytes", len(jsonlData), "model", model)

	// Create the batch client and submit.
	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return err
	}

	batchID, inputFileID, err := batchClient.SubmitBatch(ctx, jsonlData, map[string]string{
		"type":    "analyze",
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
		Type:        "analyze",
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
	return pollAnalyzeBatch(ctx, proj, state, pollInt)
}

// resumeAnalyzeBatch loads the persisted batch state and resumes polling.
func resumeAnalyzeBatch(ctx context.Context, proj *project.Project, pollInt int) error {
	state, err := translation.LoadBatchState(proj.AIDir(), "analyze")
	if err != nil {
		return fmt.Errorf("load batch state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no pending analyze batch found — run `bookai analyze` without --continue to start a new one")
	}
	if translation.IsTerminalStatus(state.Status) {
		slog.Info("batch already reached terminal status, processing results", "batch_id", state.BatchID, "status", state.Status)
	}
	slog.Info("resuming batch polling", "batch_id", state.BatchID, "status", state.Status)
	return pollAnalyzeBatch(ctx, proj, state, pollInt)
}

// pollAnalyzeBatch polls the batch status every pollInt seconds. When the batch
// reaches a terminal status, it downloads results and processes them.
func pollAnalyzeBatch(ctx context.Context, proj *project.Project, state *translation.BatchState, pollInt int) error {
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

	// Terminal status reached.
	switch state.Status {
	case "completed":
		return processAnalyzeResults(ctx, proj, state, batchClient)
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

// processAnalyzeResults downloads the batch output, extracts per-chapter
// glossary results and summaries (both from the same JSON response), saves
// summaries to memory/, sends a live merge/unify request for glossary results,
// applies config overrides, and saves the final glossary.json + characters.json.
func processAnalyzeResults(ctx context.Context, proj *project.Project, state *translation.BatchState, batchClient *translation.BatchClient) error {
	slog.Info("downloading batch results", "batch_id", state.BatchID, "output_file_id", state.OutputFileID)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download results: %w", err)
	}

	// Each result contains both glossary/characters AND a chapter summary
	// in a single JSON response. Save summaries to memory/ as we go.
	var perChapter []glossaryExtractionResult
	var failedCount int
	summariesSaved := 0
	for _, res := range results {
		if res.Error != "" {
			slog.Warn("request failed in batch", "custom_id", res.CustomID, "error", res.Error)
			failedCount++
			continue
		}
		chID := translation.SplitCustomID(res.CustomID, "analyze")
		if chID == 0 {
			slog.Warn("unrecognized custom_id in batch output", "custom_id", res.CustomID)
			continue
		}

		var result glossaryExtractionResult
		if err := json.Unmarshal([]byte(res.Content), &result); err != nil {
			slog.Warn("failed to parse glossary JSON from batch result",
				"custom_id", res.CustomID, "error", err, "content", truncate(res.Content, 200))
			failedCount++
			continue
		}
		perChapter = append(perChapter, result)

		// Save the chapter summary to memory/.
		if result.Summary != "" {
			if err := translation.SaveSummary(proj.MemoryDir(), chID, result.Summary); err != nil {
				slog.Warn("failed to save summary", "chapter", chID, "error", err)
			} else {
				summariesSaved++
			}
		}
	}
	slog.Info("batch results parsed", "glossary_results", len(perChapter), "summaries_saved", summariesSaved, "failed", failedCount)

	if len(perChapter) == 0 {
		return fmt.Errorf("no successful glossary results in batch — all %d requests failed", failedCount)
	}

	// Serialize per-chapter results for the merge prompt.
	perChapterJSON, err := json.Marshal(perChapter)
	if err != nil {
		return fmt.Errorf("marshal per-chapter results: %w", err)
	}

	// Send a live merge/unify request.
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}
	lockedTerms := buildLockedTerms(proj.Cfg.Glossary)

	slog.Info("sending merge/unify request", "chapters", len(perChapter), "model", client.HelperModel())

	resp, err := client.Chat(ctx, translation.ChatRequest{
		System:   translation.GlossaryMergeSystem,
		User:     translation.GlossaryMergeUser(string(perChapterJSON), lockedTerms),
		Model:    client.HelperModel(),
		JSONMode: true,
	})
	if err != nil {
		return fmt.Errorf("merge/unify request: %w", err)
	}
	slog.Info("merge/unify complete",
		"prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens)

	// Parse the unified result.
	var unified glossaryExtractionResult
	if err := json.Unmarshal([]byte(resp.Content), &unified); err != nil {
		return fmt.Errorf("parse unified glossary JSON: %w (content: %s)", err, truncate(resp.Content, 200))
	}

	// Apply config overrides.
	applyGlossaryOverrides(&unified, proj.Cfg.Glossary)

	// Save glossary (terms only — no characters).
	glossary := &translation.Glossary{Terms: unified.Terms}
	slog.Info("unified glossary", "terms", len(glossary.Terms), "characters", len(unified.Characters))

	if err := glossary.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved glossary", "path", filepath.Join(proj.AIDir(), "glossary.json"))

	// Save characters.
	characters := &translation.Characters{Characters: unified.Characters}
	if err := characters.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved characters", "path", filepath.Join(proj.AIDir(), "characters.json"))

	// Clean up batch state.
	_ = translation.DeleteBatchState(proj.AIDir(), "analyze")
	slog.Info("analyze batch complete", "batch_id", state.BatchID, "summaries", summariesSaved)

	return nil
}

// glossaryExtractionResult is the JSON shape we expect from the model. The
// summary field is generated alongside the glossary in a single pass — no
// separate batch item needed.
type glossaryExtractionResult struct {
	Characters []translation.Character    `json:"characters"`
	Terms      []translation.GlossaryTerm `json:"terms"`
	Summary    string                     `json:"summary"`
}

// loadAllChapters reads every chapter_NNN.json from the chapters directory,
// sorted by ID.
func loadAllChapters(proj *project.Project) ([]chapters.Chapter, error) {
	indexPath := filepath.Join(proj.ChaptersDir(), "_index.json")
	if !project.Exists(indexPath) {
		return nil, fmt.Errorf("no chapters/_index.json found — run `bookai analyze-chapters` first")
	}
	var idx chapters.Index
	if err := project.LoadJSON(indexPath, &idx); err != nil {
		return nil, fmt.Errorf("load chapter index: %w", err)
	}
	var all []chapters.Chapter
	for id := 1; id <= idx.ChapterCount; id++ {
		path := filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", id))
		var ch chapters.Chapter
		if err := project.LoadJSON(path, &ch); err != nil {
			return nil, fmt.Errorf("load chapter %d: %w", id, err)
		}
		all = append(all, ch)
	}
	return all, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// buildLockedTerms formats config glossary overrides as a human-readable list
// for the model prompt. The model is instructed to use these exact
// translations and not change them.
func buildLockedTerms(overrides config.GlossaryOverrides) string {
	if len(overrides.Characters) == 0 && len(overrides.Terms) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range overrides.Characters {
		if c.Source != "" && c.Target != "" {
			fmt.Fprintf(&b, "  %s = %s\n", c.Source, c.Target)
		}
	}
	for _, t := range overrides.Terms {
		if t.Source != "" && t.Target != "" {
			fmt.Fprintf(&b, "  %s = %s\n", t.Source, t.Target)
		}
	}
	return b.String()
}

// applyGlossaryOverrides forces the translation/target of any extracted
// character or term that matches a config override. The AI-extracted values
// for other fields (role, description, type) are preserved. If a config
// override has no match in the extracted results, it is added as a new entry.
func applyGlossaryOverrides(result *glossaryExtractionResult, overrides config.GlossaryOverrides) {
	// Override character translations.
	for i := range result.Characters {
		for _, oc := range overrides.Characters {
			if strings.EqualFold(result.Characters[i].Name, oc.Source) {
				result.Characters[i].Translation = oc.Target
				break
			}
		}
	}
	// Add config characters not found by the model.
	for _, oc := range overrides.Characters {
		found := false
		for _, c := range result.Characters {
			if strings.EqualFold(c.Name, oc.Source) {
				found = true
				break
			}
		}
		if !found && oc.Source != "" && oc.Target != "" {
			result.Characters = append(result.Characters, translation.Character{
				Name:        oc.Source,
				Translation: oc.Target,
			})
		}
	}

	// Override term translations.
	for i := range result.Terms {
		for _, ot := range overrides.Terms {
			if strings.EqualFold(result.Terms[i].Source, ot.Source) {
				result.Terms[i].Target = ot.Target
				if ot.Type != "" {
					result.Terms[i].Type = ot.Type
				}
				break
			}
		}
	}
	// Add config terms not found by the model.
	for _, ot := range overrides.Terms {
		found := false
		for _, t := range result.Terms {
			if strings.EqualFold(t.Source, ot.Source) {
				found = true
				break
			}
		}
		if !found && ot.Source != "" && ot.Target != "" {
			termType := ot.Type
			if termType == "" {
				termType = "term"
			}
			result.Terms = append(result.Terms, translation.GlossaryTerm{
				Source: ot.Source,
				Target: ot.Target,
				Type:   termType,
			})
		}
	}
}
