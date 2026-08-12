package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
// translation for words with non-obvious stress and generates stress marks
// (Silero convention: '+' before the stressed vowel) via the OpenAI Batch
// API. Each chapter is an independent batch item — the model sees the full
// chapter text and returns a list of {term, stressed} pairs. Per-chapter
// results are saved to ai/stress_NNN.json, then merged into a single global
// ai/stress.json vocabulary.
//
// The command supports a --continue flag to resume polling an interrupted
// batch. Batch state is persisted locally in ai/batch_pronounce.json.
// The --reset flag erases the existing ai/stress.json before merging.
func newPronounceCmd() *cobra.Command {
	var (
		force   bool
		cont    bool
		reset   bool
		chapter int
		chRange string
		pollInt int
	)
	cmd := &cobra.Command{
		Use:   "pronounce",
		Short: "Generate Silero stress marks per chapter via OpenAI Batch API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger()
			ctx := cmd.Context()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runPronounce(ctx, proj, force, cont, reset, chapter, chRange, pollInt)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-generate stress marks even if they already exist")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume polling an interrupted batch")
	cmd.Flags().BoolVar(&reset, "reset", false, "erase existing ai/stress.json and start merge from scratch")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "process only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "process a range of chapter ids, e.g. 5-12")
	cmd.Flags().IntVar(&pollInt, "poll-interval", defaultPollInterval, "seconds between batch status polls")
	return cmd
}

// runPronounce orchestrates the batch-based stress marks flow:
//  1. If --continue: load existing batch state and jump to polling.
//  2. Otherwise: build JSONL with one request per chapter, upload, create batch.
//  3. Poll batch status every pollInt seconds until terminal.
//  4. Download results, save per-chapter stress files, merge into global vocabulary.
func runPronounce(
	ctx context.Context,
	proj *project.Project,
	force, cont, reset bool,
	chapter int,
	chRange string,
	pollInt int,
) error {
	if pollInt < minPollInterval {
		pollInt = defaultPollInterval
	}

	// --continue: resume polling an existing batch.
	if cont {
		return resumePronounceBatch(ctx, proj, reset, pollInt)
	}

	// Check for an existing pending batch.
	state, err := translation.LoadBatchState(proj.AIDir(), translation.BatchTypePronounce)
	if err != nil && !errors.Is(err, translation.ErrBatchStateNotFound) {
		return fmt.Errorf("load batch state: %w", err)
	}
	if state != nil {
		if !translation.IsTerminalStatus(state.Status) {
			slog.Default().
				InfoContext(ctx, "found pending pronounce batch, resuming polling (use --force to start a new one)",
					"batch_id", state.BatchID, "status", state.Status)
			return pollPronounceBatch(ctx, proj, state, pollInt, reset)
		}
		slog.Default().
			InfoContext(ctx, "cleaning up completed batch state from previous run", "batch_id", state.BatchID)
		_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypePronounce)
	}

	// Load all chapters.
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	// Determine which chapters to process.
	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil && !errors.Is(err, errNoChapterFilter) {
		return err
	}
	writeAll := errors.Is(err, errNoChapterFilter)

	// Filter chapters that have translations and need stress marks.
	targetLang := proj.Cfg.Languages.Target
	toProcess, skipped := filterPronounceChapters(chs, proj, ids, writeAll, targetLang, force)
	if len(toProcess) == 0 {
		slog.Default().InfoContext(ctx, "no chapters to process", "skipped", skipped)
		return nil
	}
	slog.Default().InfoContext(ctx, "chapters to process", "count", len(toProcess), "skipped", skipped)

	// Build batch requests: one per chapter.
	model := proj.Cfg.OpenAI.HelperModel
	overrides := proj.Cfg.Pronunciation
	reqs, chapterIDs, err := buildPronounceRequests(toProcess, proj, targetLang, model, overrides)
	if err != nil {
		return err
	}

	// Build JSONL.
	jsonlData, err := translation.BuildJSONL(reqs)
	if err != nil {
		return fmt.Errorf("build batch JSONL: %w", err)
	}
	slog.Default().
		InfoContext(ctx, "built batch input", "requests", len(reqs), "jsonl_bytes", len(jsonlData), "model", model)

	// Create the batch client and submit.
	batchClient, err := translation.NewBatchClient(proj.Cfg.OpenAI.BaseURL, proj.Cfg.OpenAI.MaxRetries)
	if err != nil {
		return err
	}

	batchID, inputFileID, err := batchClient.SubmitBatch(ctx, jsonlData, map[string]string{
		translation.BatchMetadataKeyType:    translation.BatchTypePronounce,
		translation.BatchMetadataKeyProject: proj.Cfg.Project,
	})
	if err != nil {
		return err
	}
	slog.Default().InfoContext(ctx, "batch submitted", "batch_id", batchID, "input_file_id", inputFileID)

	// Save batch state.
	state = &translation.BatchState{
		BatchID:     batchID,
		InputFileID: inputFileID,
		Type:        translation.BatchTypePronounce,
		Model:       model,
		Endpoint:    translation.BatchEndpoint,
		Status:      translation.BatchStatusValidating,
		ChapterIDs:  chapterIDs,
		CreatedAt:   time.Now(),
	}
	if err = translation.SaveBatchState(proj.AIDir(), state); err != nil {
		return fmt.Errorf("save batch state: %w", err)
	}

	// Poll until completion.
	return pollPronounceBatch(ctx, proj, state, pollInt, reset)
}

// filterPronounceChapters selects chapters that have translations and don't
// already have stress marks (unless force is set). Returns the chapters to
// process and the number skipped.
func filterPronounceChapters(
	chs []chapters.Chapter,
	proj *project.Project,
	ids map[int]bool,
	writeAll bool,
	targetLang string,
	force bool,
) ([]chapters.Chapter, int) {
	var toProcess []chapters.Chapter
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
		stressPath := filepath.Join(proj.AIDir(), fmt.Sprintf("stress_%03d.json", ch.ID))
		if project.Exists(stressPath) && !force {
			skipped++
			continue
		}
		toProcess = append(toProcess, ch)
	}
	return toProcess, skipped
}

// buildPronounceRequests builds batch requests (one per chapter) and the
// corresponding chapter ID list.
func buildPronounceRequests(
	toProcess []chapters.Chapter,
	proj *project.Project,
	targetLang, model string,
	overrides []config.PronunciationOverride,
) ([]translation.BatchRequest, []int, error) {
	reqs := make([]translation.BatchRequest, len(toProcess))
	chapterIDs := make([]int, len(toProcess))
	for i, ch := range toProcess {
		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		text, err := os.ReadFile(translationPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
		}

		reqs[i] = translation.BatchRequest{
			CustomID:     fmt.Sprintf("pronounce-chapter-%d", ch.ID),
			Instructions: translation.StressSystem,
			Input:        translation.StressUser(string(text), overrides),
			Model:        model,
			JSONMode:     true,
		}
		chapterIDs[i] = ch.ID
	}
	return reqs, chapterIDs, nil
}

// resumePronounceBatch loads the persisted batch state and resumes polling.
func resumePronounceBatch(ctx context.Context, proj *project.Project, reset bool, pollInt int) error {
	state, err := translation.LoadBatchState(proj.AIDir(), translation.BatchTypePronounce)
	if err != nil {
		if errors.Is(err, translation.ErrBatchStateNotFound) {
			return fmt.Errorf(
				"no pending pronounce batch found — run `bookai pronounce` without --continue to start a new one",
			)
		}
		return fmt.Errorf("load batch state: %w", err)
	}
	slog.Default().InfoContext(ctx, "resuming batch polling", "batch_id", state.BatchID, "status", state.Status)
	return pollPronounceBatch(ctx, proj, state, pollInt, reset)
}

// pollPronounceBatch polls the batch status every pollInt seconds. When the
// batch reaches a terminal status, it downloads results and processes them.
func pollPronounceBatch(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	pollInt int,
	reset bool,
) error {
	batchClient, err := pollBatchUntilTerminal(ctx, proj, state, pollInt, "batch")
	if err != nil {
		return err
	}
	if state.Status != translation.BatchStatusCompleted {
		return batchTerminalError(state, "batch")
	}
	return processPronounceResults(ctx, proj, state, batchClient, reset)
}

// processPronounceResults downloads the batch output and merges each
// chapter's stress marks directly into the global ai/stress.json
// vocabulary. Conflicts (same term, different stressed forms) are resolved
// interactively in the CLI. Per-chapter stress files are never written to
// disk — results are merged in memory as they're parsed.
func processPronounceResults(
	ctx context.Context,
	proj *project.Project,
	state *translation.BatchState,
	batchClient *translation.BatchClient,
	reset bool,
) error {
	slog.Default().
		InfoContext(ctx, "downloading batch results", "batch_id", state.BatchID, "output_file_id", state.OutputFileID)

	results, err := batchClient.DownloadResults(ctx, state.OutputFileID)
	if err != nil {
		return fmt.Errorf("download results: %w", err)
	}

	// Load existing global stress or start fresh.
	var global *tts.Stress
	if reset {
		slog.Default().InfoContext(ctx, "resetting global stress vocabulary (--reset)")
		global = &tts.Stress{}
	} else {
		var existing *tts.Stress
		if existing, err = tts.LoadStress(proj.AIDir()); err != nil {
			return fmt.Errorf("load existing stress: %w", err)
		}
		global = existing
		slog.Default().
			InfoContext(ctx, "merging with existing stress vocabulary", "existing_entries", len(global.Entries))
	}

	processed := 0
	failedCount := 0
	var allConflicts []tts.StressConflict

	for _, res := range results {
		chID := translation.SplitCustomID(res.CustomID, translation.BatchTypePronounce)
		if chID == 0 {
			slog.Default().WarnContext(ctx, "unrecognized custom_id in batch output", "custom_id", res.CustomID)
			continue
		}
		if res.Error != "" {
			slog.Default().WarnContext(ctx, "stress generation failed in batch", "chapter", chID, "error", res.Error)
			failedCount++
			continue
		}

		// Parse the JSON response.
		var result stressResult
		if err = json.Unmarshal([]byte(res.Content), &result); err != nil {
			slog.Default().WarnContext(ctx,
				"failed to parse stress JSON",
				"chapter",
				chID,
				"error",
				err,
				"content",
				truncate(res.Content, truncateLength),
			)
			failedCount++
			continue
		}

		// Sanitize AI output: strip Unicode stress marks, drop entries
		// without a '+', and drop entries with '+' before a non-vowel.
		sanitizeStressResult(&result)

		// Apply config overrides.
		applyStressOverrides(&result, proj.Cfg.Pronunciation)

		// Merge directly into global vocabulary (no per-chapter file),
		// tagging entries with the chapter ID for per-chapter tracking.
		chapterStress := &tts.Stress{Entries: result.Entries}
		conflicts := global.MergeChapter(chapterStress, chID)
		allConflicts = append(allConflicts, conflicts...)

		slog.Default().InfoContext(ctx, "stress marks merged", "chapter", chID, "entries", len(result.Entries))
		processed++
	}

	// Resolve conflicts interactively.
	// Deduplicate conflicts by term — the same term may conflict in multiple
	// chapters, producing repeated conflicts with the same variants.
	deduped := deduplicateConflicts(allConflicts)
	if len(deduped) > 0 {
		slog.Default().InfoContext(ctx, "found stress mark conflicts, resolving interactively",
			"conflicts", len(deduped), "raw_conflicts", len(allConflicts))
		if err = resolveStressConflicts(global, deduped); err != nil {
			return fmt.Errorf("resolve conflicts: %w", err)
		}
	}

	// Save the merged global vocabulary.
	if err = global.Save(proj.AIDir()); err != nil {
		return fmt.Errorf("save global stress: %w", err)
	}
	slog.Default().InfoContext(ctx, "global stress vocabulary saved", "entries", len(global.Entries))

	// Clean up batch state.
	_ = translation.DeleteBatchState(proj.AIDir(), translation.BatchTypePronounce)
	slog.Default().InfoContext(
		ctx, "pronounce batch complete",
		"batch_id", state.BatchID,
		"processed", processed,
		"failed", failedCount,
	)

	return nil
}

// deduplicateConflicts merges conflicts for the same term into a single
// conflict with all unique variants. This avoids asking the user to resolve
// the same term multiple times when it conflicts across several chapters.
func deduplicateConflicts(conflicts []tts.StressConflict) []tts.StressConflict {
	seen := make(map[string]int) // lower(term) → index in result
	var result []tts.StressConflict
	for _, c := range conflicts {
		key := strings.ToLower(c.Term)
		if idx, ok := seen[key]; ok {
			// Merge variants into existing conflict.
			for _, v := range c.Variants {
				if !containsVariant(result[idx].Variants, v) {
					result[idx].Variants = append(result[idx].Variants, v)
				}
			}
		} else {
			seen[key] = len(result)
			// Copy variants to avoid aliasing.
			variants := make([]string, len(c.Variants))
			copy(variants, c.Variants)
			result = append(result, tts.StressConflict{Term: c.Term, Variants: variants})
		}
	}
	return result
}

// containsVariant reports whether variants contains v (case-insensitive).
func containsVariant(variants []string, v string) bool {
	for _, vv := range variants {
		if strings.EqualFold(vv, v) {
			return true
		}
	}
	return false
}

// resolveStressConflicts prompts the user to resolve each conflict one by one.
func resolveStressConflicts(global *tts.Stress, conflicts []tts.StressConflict) error {
	reader := bufio.NewReader(os.Stdin)
	resolved := 0
	skipped := 0

	for _, c := range conflicts {
		fmt.Fprintf(os.Stdout, "\nConflict for term %q:\n", c.Term)
		for i, v := range c.Variants {
			fmt.Fprintf(os.Stdout, "  [%d] %s\n", i+1, v)
		}
		fmt.Fprintf(os.Stdout, "  [%d] skip (leave unstressed)\n", len(c.Variants)+1)

		for {
			fmt.Fprintf(os.Stdout, "Choose: ")
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read input: %w", err)
			}
			line = strings.TrimSpace(line)

			choice, err := parseIntChoice(line, len(c.Variants)+1)
			if err != nil {
				fmt.Fprintf(os.Stdout, "  invalid choice: %s (enter 1-%d)\n", err, len(c.Variants)+1)
				continue
			}

			if choice <= len(c.Variants) {
				global.ResolveConflict(c.Term, c.Variants[choice-1])
				fmt.Fprintf(os.Stdout, "  → %s\n", c.Variants[choice-1])
				resolved++
			} else {
				global.ResolveConflict(c.Term, "")
				fmt.Fprintf(os.Stdout, "  → skipped\n")
				skipped++
			}
			break
		}
	}

	slog.Default().Info("conflicts resolved", "resolved", resolved, "skipped", skipped)
	return nil
}

// parseIntChoice parses a 1-based choice from the user. Returns an error if
// the input is not a valid number or out of range.
func parseIntChoice(s string, maxVal int) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("not a number")
	}
	if n < 1 || n > maxVal {
		return 0, fmt.Errorf("out of range (1-%d)", maxVal)
	}
	return n, nil
}

// stressResult is the JSON shape we expect from the model.
type stressResult struct {
	Entries []tts.StressEntry `json:"entries"`
}

// sanitizeStressResult cleans AI-produced stress entries in place:
//  1. Strips Unicode combining stress marks (U+0300–U+0342) and precomposed
//     stressed vowels (и́, е́, etc.) — only ASCII '+' is valid.
//  2. Drops entries where "stressed" has no '+' at all (useless — identical
//     to the term).
//  3. Drops entries where '+' is not before a Cyrillic vowel (would crash
//     Silero).
func sanitizeStressResult(result *stressResult) {
	cleaned := result.Entries[:0]
	for _, e := range result.Entries {
		e.Stressed = stripUnicodeStress(e.Stressed)
		if !strings.Contains(e.Stressed, "+") {
			continue
		}
		if !tts.HasValidStressMark(e.Stressed) {
			continue
		}
		cleaned = append(cleaned, e)
	}
	result.Entries = cleaned
}

// stripUnicodeStress removes Unicode combining diacritical marks (stress,
// grave, acute, etc.) from the string. The Silero convention uses only ASCII
// '+' before the stressed vowel — any Unicode stress marks are AI artifacts
// that must be removed.
func stripUnicodeStress(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Strip combining diacritical marks (U+0300–U+036F) and
		// precomposed Cyrillic stressed vowels (U+0401 is ё, handled
		// separately). Also strip the combining acute/grave/cyrillic
		// stress marks specifically.
		if r >= 0x0300 && r <= 0x036F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// applyStressOverrides forces the stress of any entry that matches a config
// pronunciation override. Config entries not found by the model are added as
// new entries.
func applyStressOverrides(result *stressResult, overrides []config.PronunciationOverride) {
	if len(overrides) == 0 {
		return
	}
	overridden := 0
	for i := range result.Entries {
		for _, ov := range overrides {
			if strings.EqualFold(result.Entries[i].Term, ov.Term) {
				result.Entries[i].Stressed = ov.Phonemes
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
			result.Entries = append(result.Entries, tts.StressEntry{
				Term:     ov.Term,
				Stressed: ov.Phonemes,
			})
			overridden++
		}
	}
	if overridden > 0 {
		slog.Default().Info("applied stress overrides", "overridden", overridden, "config_entries", len(overrides))
	}
}
