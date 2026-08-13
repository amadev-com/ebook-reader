package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/config"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// errMergeInputNotFound is returned by loadAnalyzeMergeInput when no merge
// input file exists.
var errMergeInputNotFound = errors.New("analyze merge input not found")

// newAnalyzeCmd implements `bookai analyze`: extracts a glossary and character
// list from the book using the OpenAI Batch API for cost-effective processing.
// Each chapter is submitted as an independent batch item. After the batch
// completes, a single "live" (non-batch) request merges and unifies all
// per-chapter results into the final glossary.json + characters.json.
//
// When run on a subset of chapters (--chapter/--range), new results are merged
// with the existing glossary.json + characters.json. The --force flag starts
// fresh (ignores existing files and replaces them).
//
// The command supports a --continue flag to resume polling an interrupted
// newAnalyzeCmd creates the analyze command for extracting glossary terms and
// characters, with options for fresh or resumed analysis, chapter selection,
// and batch polling interval. Batch state is persisted locally in
// ai/batch_analyze.json.
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger()
			ctx := cmd.Context()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runAnalyze(ctx, proj, force, cont, chapter, chRange, pollInt)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "start fresh — ignore existing glossary/characters and replace them")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume polling an interrupted batch")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "analyze only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "analyze a range of chapter ids, e.g. 5-12")
	cmd.Flags().IntVar(&pollInt, "poll-interval", defaultPollInterval, "seconds between batch status polls")
	return cmd
}

// runAnalyze orchestrates the batch-based glossary extraction flow:
//  1. If --continue: load existing batch state and jump to polling.
//  2. Otherwise: build JSONL with one request per chapter, upload, create batch.
//  3. Poll batch status every pollInt seconds until terminal.
//
// runAnalyze orchestrates chapter analysis, batch submission and resumption, and glossary and character merging.
// It returns an error if chapter loading, batch preparation, submission, or processing fails.
func runAnalyze(
	ctx context.Context,
	proj *project.Project,
	force, cont bool,
	chapter int,
	chRange string,
	pollInt int,
) error {
	if pollInt < minPollInterval {
		pollInt = defaultPollInterval
	}

	// --continue: resume polling an existing batch.
	if cont {
		return resumeAnalyzeBatch(ctx, proj, pollInt)
	}

	// Load existing glossary + characters for merging (unless --force).
	// --force means "start fresh" — ignore existing files.
	var existingGlossary *translation.Glossary
	var existingCharacters *translation.Characters
	if !force {
		existingGlossary, _ = translation.LoadGlossary(proj.AIDir())
		existingCharacters, _ = translation.LoadCharacters(proj.AIDir())
		if len(existingGlossary.Terms) > 0 || len(existingCharacters.Characters) > 0 {
			slog.Default().InfoContext(ctx, "merging with existing vocabulary",
				"existing_terms", len(existingGlossary.Terms),
				"existing_characters", len(existingCharacters.Characters))
		}
	} else {
		slog.Default().InfoContext(ctx, "starting fresh (--force, ignoring existing glossary/characters)")
	}

	// Check for an existing pending batch.
	state, err := translation.LoadBatchState(proj.AIDir(), translation.BatchTypeAnalyze)
	if err != nil && !errors.Is(err, translation.ErrBatchStateNotFound) {
		return fmt.Errorf("load batch state: %w", err)
	}
	if state != nil {
		if !translation.IsTerminalStatus(state.Status) {
			slog.Default().
				InfoContext(ctx, "found pending analyze batch, resuming polling (use --force to start a new one)",
					"batch_id", state.BatchID, "status", state.Status)
			return pollAnalyzeBatch(ctx, proj, state, pollInt, existingGlossary, existingCharacters)
		}
		slog.Default().
			InfoContext(ctx, "cleaning up completed batch state from previous run", "batch_id", state.BatchID)
		_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypeAnalyze)
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
	if chs, err = filterChapters(chs, chapter, chRange); err != nil {
		return err
	}
	slog.Default().InfoContext(ctx, "loaded chapters for analysis", "count", len(chs))

	// Build locked-translations string from config overrides.
	lockedTerms := buildLockedTerms(proj.Cfg.Glossary)

	// Build batch requests: one per chapter. Each request extracts the
	// glossary AND generates a chapter summary in a single pass — the model
	// reads the chapter once and returns both in one JSON response.
	model := proj.Cfg.OpenAI.HelperModel
	reqs, chapterIDs := buildAnalyzeBatchRequests(chs, lockedTerms, model)

	// Build JSONL.
	jsonlData, err := translation.BuildJSONL(reqs)
	if err != nil {
		return fmt.Errorf("build batch JSONL: %w", err)
	}
	slog.Default().InfoContext(ctx,
		"built batch input",
		"requests",
		len(reqs),
		"chapters",
		len(chs),
		"jsonl_bytes",
		len(jsonlData),
		"model",
		model,
	)

	// Create the batch client and submit.
	state, err = submitAnalyzeBatch(ctx, proj, jsonlData, model, chapterIDs)
	if err != nil {
		return err
	}

	// Poll until completion.
	return pollAnalyzeBatch(ctx, proj, state, pollInt, existingGlossary, existingCharacters)
}

// buildAnalyzeBatchRequests builds one batch request per chapter. Each request
// buildAnalyzeBatchRequests creates one JSON batch request for each chapter and
// collects the corresponding chapter IDs. Each request extracts glossary and
// character data and generates a chapter summary.
func buildAnalyzeBatchRequests(
	chs []chapters.Chapter,
	lockedTerms string,
	model string,
) ([]translation.BatchRequest, []int) {
	reqs := make([]translation.BatchRequest, len(chs))
	chapterIDs := make([]int, len(chs))
	for i, ch := range chs {
		reqs[i] = translation.BatchRequest{
			CustomID:     fmt.Sprintf("analyze-chapter-%d", ch.ID),
			Instructions: translation.GlossaryExtractionSystem,
			Input: translation.GlossaryExtractionUser(
				[]translation.ChapterText{{Title: ch.Title, Text: ch.Source}},
				"",
				lockedTerms,
			),
			Model:    model,
			JSONMode: true,
		}
		chapterIDs[i] = ch.ID
	}
	return reqs, chapterIDs
}

// submitAnalyzeBatch creates the batch client, submits the JSONL, saves the
// submitAnalyzeBatch submits chapter analysis requests as a batch, persists its polling state, and returns that state.
func submitAnalyzeBatch(
	ctx context.Context,
	proj *project.Project,
	jsonlData []byte,
	model string,
	chapterIDs []int,
) (*translation.BatchState, error) {
	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return nil, err
	}

	batchID, inputFileID, err := batchClient.SubmitBatch(ctx, jsonlData, map[string]string{
		translation.BatchMetadataKeyType:    translation.BatchTypeAnalyze,
		translation.BatchMetadataKeyProject: proj.Cfg.Project,
	})
	if err != nil {
		return nil, err
	}
	slog.Default().InfoContext(ctx, "batch submitted", "batch_id", batchID, "input_file_id", inputFileID)

	state := &translation.BatchState{
		BatchID:     batchID,
		InputFileID: inputFileID,
		Type:        translation.BatchTypeAnalyze,
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

// filterChapters applies the --chapter/--range filter to the chapter list.
// It returns an error when the filter is invalid.
func filterChapters(chs []chapters.Chapter, chapter int, chRange string) ([]chapters.Chapter, error) {
	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil && !errors.Is(err, errNoChapterFilter) {
		return nil, err
	}
	if ids == nil {
		return chs, nil
	}
	filtered := chs[:0]
	for _, ch := range chs {
		if ids[ch.ID] {
			filtered = append(filtered, ch)
		}
	}
	return filtered, nil
}

// resumeAnalyzeBatch loads the persisted batch state and resumes polling. It
// checks for a merge batch first (the second phase), then falls back to the
// resumeAnalyzeBatch resumes a pending analysis batch and processes completed results.
func resumeAnalyzeBatch(ctx context.Context, proj *project.Project, pollInt int) error {
	// Check for a pending merge batch first (second phase).
	mergeState, err := translation.LoadBatchState(proj.AIDir(), translation.BatchTypeAnalyzeMerge)
	if err != nil && !errors.Is(err, translation.ErrBatchStateNotFound) {
		return fmt.Errorf("load merge batch state: %w", err)
	}
	if mergeState != nil {
		if !translation.IsTerminalStatus(mergeState.Status) {
			slog.Default().InfoContext(
				ctx, "resuming merge batch polling",
				"batch_id", mergeState.BatchID,
				"status", mergeState.Status,
			)
			return pollAnalyzeMergeBatch(ctx, proj, mergeState)
		}
		slog.Default().
			InfoContext(ctx, "merge batch already completed, processing results", "batch_id", mergeState.BatchID)
		var batchClient *translation.BatchClient
		batchClient, err = translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
		if err != nil {
			return err
		}
		return processAnalyzeMergeResults(ctx, proj, mergeState, batchClient)
	}

	// Fall back to the extraction batch (first phase).
	state, err := translation.LoadBatchState(proj.AIDir(), translation.BatchTypeAnalyze)
	if err != nil {
		if errors.Is(err, translation.ErrBatchStateNotFound) {
			return fmt.Errorf(
				"no pending analyze batch found — run `bookai analyze` without --continue to start a new one",
			)
		}
		return fmt.Errorf("load batch state: %w", err)
	}
	if translation.IsTerminalStatus(state.Status) {
		slog.Default().InfoContext(
			ctx, "batch already reached terminal status, processing results",
			"batch_id", state.BatchID,
			"status", state.Status,
		)
	}
	slog.Default().InfoContext(ctx, "resuming batch polling", "batch_id", state.BatchID, "status", state.Status)

	// Load existing glossary/characters for the merge phase.
	existingGlossary, _ := translation.LoadGlossary(proj.AIDir())
	existingCharacters, _ := translation.LoadCharacters(proj.AIDir())
	return pollAnalyzeBatch(ctx, proj, state, pollInt, existingGlossary, existingCharacters)
}

// pollAnalyzeBatch polls the batch status every pollInt seconds. When the batch
// reaches a terminal status, it downloads results and processes them.
// existingGlossary and existingCharacters are passed to the merge step so new
// results can be merged with existing vocabulary (nil for --force or --continue
// pollAnalyzeBatch waits for an analysis batch to reach a terminal state and processes
// its results when completed. It returns an error if polling fails or the batch does not
// complete successfully.
func pollAnalyzeBatch(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	pollInt int,
	existingGlossary *translation.Glossary,
	existingCharacters *translation.Characters,
) error {
	batchClient, err := pollBatchUntilTerminal(ctx, proj, state, pollInt, "batch")
	if err != nil {
		return err
	}
	if state.Status != translation.BatchStatusCompleted {
		return batchTerminalError(state, "batch")
	}
	return processAnalyzeResults(ctx, proj, state, batchClient, existingGlossary, existingCharacters)
}

// processAnalyzeResults downloads the batch output, extracts per-chapter
// glossary results and summaries (both from the same JSON response), saves
// summaries to memory/, then submits the merge/unify step as a second batch
// (for 50% cost discount). The per-chapter results and existing vocabulary are
// persisted to ai/analyze_merge_input.json so the merge batch can be resumed
// processAnalyzeResults processes batch extraction results, saves chapter summaries,
// persists merge input, and submits the glossary and character merge batch.
func processAnalyzeResults(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	batchClient *translation.BatchClient,
	existingGlossary *translation.Glossary,
	existingCharacters *translation.Characters,
) error {
	slog.Default().
		InfoContext(ctx, "downloading batch results", "batch_id", state.BatchID, "output_file_id", state.OutputFileID)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download results: %w", err)
	}

	// Each result contains both glossary/characters AND a chapter summary
	// in a single JSON response. Save summaries to memory/ as we go.
	var chapterResults []chapterResult
	var perChapter []glossaryExtractionResult // for merge prompt serialization
	var failedCount int
	summariesSaved := 0
	for _, res := range results {
		if res.Error != "" {
			slog.Default().WarnContext(ctx, "request failed in batch", "custom_id", res.CustomID, "error", res.Error)
			failedCount++
			continue
		}
		chID := translation.SplitCustomID(res.CustomID, translation.BatchTypeAnalyze)
		if chID == 0 {
			slog.Default().WarnContext(ctx, "unrecognized custom_id in batch output", "custom_id", res.CustomID)
			continue
		}

		var result glossaryExtractionResult
		if err = json.Unmarshal([]byte(res.Content), &result); err != nil {
			slog.Default().WarnContext(ctx, "failed to parse glossary JSON from batch result",
				"custom_id", res.CustomID, "error", err, "content", truncate(res.Content, truncateLength))
			failedCount++
			continue
		}
		chapterResults = append(chapterResults, chapterResult{ChapterID: chID, Result: result})
		perChapter = append(perChapter, result)

		// Save the chapter summary to memory/.
		if result.Summary != "" {
			if err = translation.SaveSummary(proj.MemoryDir(), chID, result.Summary); err != nil {
				slog.Default().WarnContext(ctx, "failed to save summary", "chapter", chID, "error", err)
			} else {
				summariesSaved++
			}
		}
	}
	slog.Default().InfoContext(
		ctx, "batch results parsed",
		"glossary_results", len(perChapter),
		"summaries_saved", summariesSaved,
		"failed", failedCount,
	)

	if len(perChapter) == 0 {
		return fmt.Errorf("no successful glossary results in batch — all %d requests failed", failedCount)
	}

	// Clean up the analyze batch state — the extraction phase is done.
	_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypeAnalyze)

	// Save merge input to disk so the merge batch can be resumed with --continue.
	mergeInput := analyzeMergeInput{
		ChapterResults:     chapterResults,
		SummariesSaved:     summariesSaved,
		ExistingGlossary:   existingGlossary,
		ExistingCharacters: existingCharacters,
	}
	if err = saveAnalyzeMergeInput(proj.AIDir(), &mergeInput); err != nil {
		return fmt.Errorf("save merge input: %w", err)
	}

	// Submit the merge as a batch.
	return submitAnalyzeMergeBatch(ctx, proj, &mergeInput)
}

// analyzeMergeInput is the persisted state between the extraction batch and the
// merge batch. It contains the per-chapter results (with chapter IDs for
// tagging) and existing vocabulary needed to build the merge prompt.
type analyzeMergeInput struct {
	ChapterResults     []chapterResult         `json:"chapter_results"`
	SummariesSaved     int                     `json:"summaries_saved"`
	ExistingGlossary   *translation.Glossary   `json:"existing_glossary,omitempty"`
	ExistingCharacters *translation.Characters `json:"existing_characters,omitempty"`
}

// saveAnalyzeMergeInput writes the merge input to ai/analyze_merge_input.json.
func saveAnalyzeMergeInput(aiDir string, input *analyzeMergeInput) error {
	return project.SaveJSON(filepath.Join(aiDir, "analyze_merge_input.json"), input)
}

// loadAnalyzeMergeInput reads the merge input from ai/analyze_merge_input.json.
func loadAnalyzeMergeInput(aiDir string) (*analyzeMergeInput, error) {
	path := filepath.Join(aiDir, "analyze_merge_input.json")
	if !project.Exists(path) {
		return nil, errMergeInputNotFound
	}
	var input analyzeMergeInput
	if err := project.LoadJSON(path, &input); err != nil {
		return nil, fmt.Errorf("load merge input: %w", err)
	}
	return &input, nil
}

// deleteAnalyzeMergeInput removes the merge input file.
func deleteAnalyzeMergeInput(aiDir string) {
	_ = os.Remove(filepath.Join(aiDir, "analyze_merge_input.json"))
}

// submitAnalyzeMergeBatch builds a single-item batch for the merge/unify step
// and submits it. The per-chapter results and existing vocabulary are serialized
// submitAnalyzeMergeBatch submits the merged analysis results for glossary and character
// consolidation, persists the batch state, and polls the batch until completion.
func submitAnalyzeMergeBatch(ctx context.Context, proj *project.Project, input *analyzeMergeInput) error {
	// Serialize just the results (without chapter IDs) for the merge prompt.
	perChapter := make([]glossaryExtractionResult, len(input.ChapterResults))
	for i, cr := range input.ChapterResults {
		perChapter[i] = cr.Result
	}
	perChapterJSON, err := json.Marshal(perChapter)
	if err != nil {
		return fmt.Errorf("marshal per-chapter results: %w", err)
	}

	var existingGlossaryJSON, existingCharactersJSON string
	if input.ExistingGlossary != nil && len(input.ExistingGlossary.Terms) > 0 {
		if existingGlossaryJSON, err = input.ExistingGlossary.MarshalForPrompt(); err != nil {
			return fmt.Errorf("marshal existing glossary: %w", err)
		}
	}
	if input.ExistingCharacters != nil && len(input.ExistingCharacters.Characters) > 0 {
		if existingCharactersJSON, err = input.ExistingCharacters.MarshalForPrompt(); err != nil {
			return fmt.Errorf("marshal existing characters: %w", err)
		}
	}

	lockedTerms := buildLockedTerms(proj.Cfg.Glossary)
	model := proj.Cfg.OpenAI.HelperModel

	reqs := []translation.BatchRequest{
		{
			CustomID:     translation.BatchTypeAnalyzeMerge,
			Instructions: translation.GlossaryMergeSystem,
			Input: translation.GlossaryMergeUser(
				string(perChapterJSON),
				lockedTerms,
				existingGlossaryJSON,
				existingCharactersJSON,
			),
			Model:    model,
			JSONMode: true,
		},
	}

	jsonlData, err := translation.BuildJSONL(reqs)
	if err != nil {
		return fmt.Errorf("build merge JSONL: %w", err)
	}

	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return err
	}

	batchID, inputFileID, err := batchClient.SubmitBatch(ctx, jsonlData, map[string]string{
		translation.BatchMetadataKeyType:    translation.BatchTypeAnalyzeMerge,
		translation.BatchMetadataKeyProject: proj.Cfg.Project,
	})
	if err != nil {
		return err
	}

	exTerms := 0
	if input.ExistingGlossary != nil {
		exTerms = len(input.ExistingGlossary.Terms)
	}
	exChars := 0
	if input.ExistingCharacters != nil {
		exChars = len(input.ExistingCharacters.Characters)
	}
	slog.Default().InfoContext(
		ctx, "merge batch submitted",
		"batch_id", batchID,
		"chapters", len(input.ChapterResults),
		"existing_terms", exTerms,
		"existing_characters", exChars,
		"model", model,
	)

	mergeState := &translation.BatchState{
		BatchID:     batchID,
		InputFileID: inputFileID,
		Type:        translation.BatchTypeAnalyzeMerge,
		Model:       model,
		Endpoint:    translation.BatchEndpoint,
		Status:      translation.BatchStatusValidating,
		CreatedAt:   time.Now(),
	}
	if err = translation.SaveBatchState(proj.AIDir(), mergeState); err != nil {
		return fmt.Errorf("save merge batch state: %w", err)
	}

	return pollAnalyzeMergeBatch(ctx, proj, mergeState)
}

// pollAnalyzeMergeBatch polls the merge batch until completion, then processes
// pollAnalyzeMergeBatch polls the merge batch until it reaches a terminal status and
// processes the completed results to save the final glossary and character data.
func pollAnalyzeMergeBatch(ctx context.Context, proj *project.Project, state *translation.BatchState) error {
	batchClient, err := pollBatchUntilTerminal(ctx, proj, state, defaultPollInterval, "merge batch")
	if err != nil {
		return err
	}
	if state.Status != translation.BatchStatusCompleted {
		return batchTerminalError(state, "merge batch")
	}
	return processAnalyzeMergeResults(ctx, proj, state, batchClient)
}

// processAnalyzeMergeResults downloads the merge batch result, parses the
// unified glossary + characters, applies config overrides, and saves the
// processAnalyzeMergeResults finalizes the analysis merge by applying overrides, restoring chapter associations, and saving the unified glossary and character data.
func processAnalyzeMergeResults(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	batchClient *translation.BatchClient,
) error {
	slog.Default().InfoContext(
		ctx, "downloading merge batch results",
		"batch_id", state.BatchID,
		"output_file_id", state.OutputFileID,
	)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download merge results: %w", err)
	}

	if len(results) == 0 {
		return fmt.Errorf("no results in merge batch output")
	}

	res := results[0]
	if res.Error != "" {
		return fmt.Errorf("merge request failed: %s", res.Error)
	}

	var unified glossaryExtractionResult
	if err = json.Unmarshal([]byte(res.Content), &unified); err != nil {
		return fmt.Errorf("parse unified glossary JSON: %w (content: %s)", err, truncate(res.Content, truncateLength))
	}

	// Apply config overrides.
	applyGlossaryOverrides(&unified, proj.Cfg.Glossary)

	// Tag terms and characters with chapter IDs from per-chapter results.
	// The AI merge produces deduplicated entries but loses the chapter origin.
	// We match by source/name back to the per-chapter results to fill the
	// Chapters field, which is used by translate to send only relevant
	// glossary entries per chapter (reducing token costs).
	input, err := loadAnalyzeMergeInput(proj.AIDir())
	if err != nil && !errors.Is(err, errMergeInputNotFound) {
		// Log but don't fail — the merge result is complete and paid for.
		// Skipping chapter tagging is better than discarding the glossary.
		slog.Default().WarnContext(ctx, "failed to load merge input, skipping chapter tagging", "error", err)
		input = nil
	}
	if input != nil {
		tagChapters(&unified, input.ChapterResults, input.ExistingGlossary, input.ExistingCharacters)
	}

	// Save glossary (terms only — no characters).
	glossary := &translation.Glossary{Terms: unified.Terms}
	slog.Default().
		InfoContext(ctx, "unified glossary", "terms", len(glossary.Terms), "characters", len(unified.Characters))

	if err = glossary.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Default().InfoContext(ctx, "saved glossary", "path", filepath.Join(proj.AIDir(), "glossary.json"))

	// Save characters.
	characters := &translation.Characters{Characters: unified.Characters}
	if err = characters.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Default().InfoContext(ctx, "saved characters", "path", filepath.Join(proj.AIDir(), "characters.json"))

	// Report summaries count from merge input.
	summaries := 0
	if input != nil {
		summaries = input.SummariesSaved
	}

	// Clean up.
	_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypeAnalyzeMerge)
	deleteAnalyzeMergeInput(proj.AIDir())
	slog.Default().InfoContext(ctx,
		"analyze complete",
		"glossary_terms",
		len(glossary.Terms),
		"characters",
		len(unified.Characters),
		"summaries",
		summaries,
	)

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

// chapterResult pairs a glossary extraction result with its chapter ID.
type chapterResult struct {
	ChapterID int                      `json:"chapter_id"`
	Result    glossaryExtractionResult `json:"result"`
}

// tagChapters fills the Chapters field on each unified term and character by
// matching source/name back to the per-chapter results. It also preserves
// existing chapter tags from the existing glossary/characters (for entries
// tagChapters assigns chapter IDs to unified glossary terms and characters using
// extracted chapter results and tags from previously analyzed data.
func tagChapters(
	unified *glossaryExtractionResult,
	chapterResults []chapterResult,
	existingGlossary *translation.Glossary,
	existingCharacters *translation.Characters,
) {
	termChapters := buildTermChapterMap(chapterResults, existingGlossary)
	characterChapters := buildCharacterChapterMap(chapterResults, existingCharacters)

	tagTermsWithChapters(unified.Terms, termChapters)
	tagCharactersWithChapters(unified.Characters, characterChapters)
}

// buildTermChapterMap builds a lower(source)→[]chapterID map from per-chapter
// buildTermChapterMap maps glossary sources to unique chapter IDs from current
// extraction results and previously saved glossary entries, using case-insensitive keys.
func buildTermChapterMap(chapterResults []chapterResult, existingGlossary *translation.Glossary) map[string][]int {
	termChapters := make(map[string][]int) // lower(source) → chapter IDs
	for _, cr := range chapterResults {
		for _, t := range cr.Result.Terms {
			if t.Source == "" {
				continue
			}
			key := strings.ToLower(t.Source)
			termChapters[key] = appendUniqueInt(termChapters[key], cr.ChapterID)
		}
	}
	if existingGlossary != nil {
		for _, t := range existingGlossary.Terms {
			if t.Source == "" {
				continue
			}
			key := strings.ToLower(t.Source)
			termChapters[key] = appendUniqueIntSlice(termChapters[key], t.Chapters)
		}
	}
	return termChapters
}

// buildCharacterChapterMap builds a lower(name)→[]chapterID map from per-chapter
// buildCharacterChapterMap maps character names to the unique chapter IDs in which they appear, including existing chapter tags.
func buildCharacterChapterMap(
	chapterResults []chapterResult,
	existingCharacters *translation.Characters,
) map[string][]int {
	characterChapters := make(map[string][]int) // lower(name) → chapter IDs
	for _, cr := range chapterResults {
		for _, c := range cr.Result.Characters {
			if c.Name == "" {
				continue
			}
			key := strings.ToLower(c.Name)
			characterChapters[key] = appendUniqueInt(characterChapters[key], cr.ChapterID)
		}
	}
	if existingCharacters != nil {
		for _, c := range existingCharacters.Characters {
			if c.Name == "" {
				continue
			}
			key := strings.ToLower(c.Name)
			characterChapters[key] = appendUniqueIntSlice(characterChapters[key], c.Chapters)
		}
	}
	return characterChapters
}

// tagTermsWithChapters sets the Chapters field on each term by looking up its
// tagTermsWithChapters assigns chapter IDs to glossary terms using their source names.
func tagTermsWithChapters(terms []translation.GlossaryTerm, termChapters map[string][]int) {
	for i := range terms {
		if terms[i].Source == "" {
			continue
		}
		key := strings.ToLower(terms[i].Source)
		if chapters, ok := termChapters[key]; ok {
			terms[i].Chapters = chapters
		}
	}
}

// tagCharactersWithChapters sets the Chapters field on each character by
// tagCharactersWithChapters assigns chapter IDs to characters with matching names.
func tagCharactersWithChapters(characters []translation.Character, characterChapters map[string][]int) {
	for i := range characters {
		if characters[i].Name == "" {
			continue
		}
		key := strings.ToLower(characters[i].Name)
		if chapters, ok := characterChapters[key]; ok {
			characters[i].Chapters = chapters
		}
	}
}

// appendUniqueInt appends v to s when v is not already present and returns the resulting slice.
func appendUniqueInt(s []int, v int) []int {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// appendUniqueIntSlice appends all values from extra to s, skipping duplicates.
func appendUniqueIntSlice(s, extra []int) []int {
	for _, v := range extra {
		s = appendUniqueInt(s, v)
	}
	return s
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
// applyGlossaryOverrides applies configured character and glossary-term overrides to the extracted result, adding configured entries that are absent.
func applyGlossaryOverrides(result *glossaryExtractionResult, overrides config.GlossaryOverrides) {
	result.Characters = applyCharacterOverrides(result.Characters, overrides.Characters)
	result.Terms = applyTermOverrides(result.Terms, overrides.Terms)
}

// applyCharacterOverrides overrides the translation of any extracted character
// that matches a config override, then adds config characters not found by the
// applyCharacterOverrides applies configured character translations and adds configured characters absent from the model output. Empty source or target overrides are ignored.
func applyCharacterOverrides(
	characters []translation.Character,
	configChars []config.GlossaryOverride,
) []translation.Character {
	// Override character translations.
	for i := range characters {
		for _, oc := range configChars {
			if strings.EqualFold(characters[i].Name, oc.Source) {
				characters[i].Translation = oc.Target
				break
			}
		}
	}
	// Add config characters not found by the model.
	for _, oc := range configChars {
		found := false
		for _, c := range characters {
			if strings.EqualFold(c.Name, oc.Source) {
				found = true
				break
			}
		}
		if !found && oc.Source != "" && oc.Target != "" {
			characters = append(characters, translation.Character{
				Name:        oc.Source,
				Translation: oc.Target,
			})
		}
	}
	return characters
}

// applyTermOverrides overrides the target (and optionally type) of any extracted
// term that matches a config override, then adds config terms not found by the
// applyTermOverrides applies configured translations and types to matching glossary terms
// and adds configured terms that are absent from the existing entries.
func applyTermOverrides(
	terms []translation.GlossaryTerm,
	configTerms []config.GlossaryOverride,
) []translation.GlossaryTerm {
	// Override term translations.
	for i := range terms {
		for _, ot := range configTerms {
			if strings.EqualFold(terms[i].Source, ot.Source) {
				terms[i].Target = ot.Target
				if ot.Type != "" {
					terms[i].Type = ot.Type
				}
				break
			}
		}
	}
	return addMissingTerms(terms, configTerms)
}

// addMissingTerms appends config terms that were not found in the extracted
// addMissingTerms appends configured glossary terms that are absent from the existing terms.
// Source matching is case-insensitive, and entries with an empty source or target are ignored.
// Configured terms without a type use the default term type.
func addMissingTerms(
	terms []translation.GlossaryTerm,
	configTerms []config.GlossaryOverride,
) []translation.GlossaryTerm {
	for _, ot := range configTerms {
		found := false
		for _, t := range terms {
			if strings.EqualFold(t.Source, ot.Source) {
				found = true
				break
			}
		}
		if !found && ot.Source != "" && ot.Target != "" {
			termType := ot.Type
			if termType == "" {
				termType = translation.TypeTerm
			}
			terms = append(terms, translation.GlossaryTerm{
				Source: ot.Source,
				Target: ot.Target,
				Type:   termType,
			})
		}
	}
	return terms
}
