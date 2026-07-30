package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/config"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// newAnalyzeCmd implements `bookai analyze`: extracts a glossary and character
// list from the book by sending full chapter texts in batches to the helper
// model (gpt-4.1-mini). Batches are processed sequentially; the accumulated
// glossary from previous batches is fed into each subsequent call so the model
// can merge new findings without producing duplicates.
func newAnalyzeCmd() *cobra.Command {
	var (
		force     bool
		chapter   int
		chRange   string
		batchSize int
	)
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Extract glossary and characters from the book (Milestone 2)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runAnalyze(ctx, proj, force, chapter, chRange, batchSize)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-analyze even if glossary.json already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "analyze only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "analyze a range of chapter ids, e.g. 5-12")
	cmd.Flags().IntVar(&batchSize, "batch-size", 10, "number of chapters per API call (full text)")
	return cmd
}

// runAnalyze loads chapters, sends them in sequential batches to the helper
// model for glossary extraction, and writes ai/glossary.json +
// ai/characters.json. Each batch receives the accumulated glossary from
// previous batches so the model merges rather than duplicates.
func runAnalyze(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string, batchSize int) error {
	glossaryPath := filepath.Join(proj.AIDir(), "glossary.json")
	if project.Exists(glossaryPath) && !force {
		slog.Info("glossary already exists, skipping (use --force to re-analyze)", "path", glossaryPath)
		return nil
	}

	if batchSize < 1 {
		batchSize = 10
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
	slog.Info("loaded chapters for analysis", "count", len(chs), "batch_size", batchSize)

	// Create the OpenAI client.
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}
	slog.Info("openai client ready", "helper_model", client.HelperModel())

	// Process chapters in sequential batches. The accumulated glossary JSON
	// from previous batches is passed to each subsequent call so the model can
	// merge new terms with existing ones.
	var accumulated glossaryExtractionResult
	totalBatches := (len(chs) + batchSize - 1) / batchSize
	for i := 0; i < len(chs); i += batchSize {
		if ctx.Err() != nil {
			slog.Info("interrupted by signal", "completed_batches", i/batchSize)
			return ctx.Err()
		}

		end := i + batchSize
		if end > len(chs) {
			end = len(chs)
		}
		batch := chs[i:end]
		batchNum := i/batchSize + 1

		// Build ChapterText slice with full source text.
		chTexts := make([]translation.ChapterText, len(batch))
		for j, ch := range batch {
			chTexts[j] = translation.ChapterText{
				Title: ch.Title,
				Text:  ch.Source,
			}
		}

		// Serialize the accumulated glossary so far for the model to merge with.
		existingGlossary := ""
		if len(accumulated.Characters) > 0 || len(accumulated.Terms) > 0 {
			if data, err := json.Marshal(accumulated); err == nil {
				existingGlossary = string(data)
			}
		}

		// Build locked-translations string from config overrides.
		lockedTerms := buildLockedTerms(proj.Cfg.Glossary)

		slog.Info("processing batch",
			"batch", batchNum, "of", totalBatches,
			"chapters", fmt.Sprintf("%d-%d", batch[0].ID, batch[len(batch)-1].ID),
			"existing_terms", len(accumulated.Terms), "existing_characters", len(accumulated.Characters),
			"locked_overrides", len(proj.Cfg.Glossary.Characters)+len(proj.Cfg.Glossary.Terms))

		resp, err := client.Chat(ctx, translation.ChatRequest{
			System:   translation.GlossaryExtractionSystem,
			User:     translation.GlossaryExtractionUser(chTexts, existingGlossary, lockedTerms),
			Model:    client.HelperModel(),
			JSONMode: true,
		})
		if err != nil {
			return fmt.Errorf("batch %d: %w", batchNum, err)
		}
		slog.Info("batch complete", "batch", batchNum,
			"prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens)

		// Parse the response and replace accumulated with the merged result.
		var result glossaryExtractionResult
		if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
			return fmt.Errorf("parse glossary JSON (batch %d): %w (content: %s)", batchNum, err, truncate(resp.Content, 200))
		}
		accumulated = result
	}

	// Apply config overrides: force the translation/target of any term or
	// character that matches a config override. The AI fills in other fields
	// (role, description, type) from context, but the translation is locked.
	applyGlossaryOverrides(&accumulated, proj.Cfg.Glossary)

	// Build and save the glossary (terms only — characters are stored
	// separately in characters.json to avoid duplication).
	glossary := &translation.Glossary{Terms: accumulated.Terms}
	slog.Info("extracted glossary", "terms", len(glossary.Terms),
		"characters", len(accumulated.Characters))

	if err := glossary.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved glossary", "path", glossaryPath)

	// Build and save the characters store.
	characters := &translation.Characters{Characters: accumulated.Characters}
	if err := characters.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved characters", "path", filepath.Join(proj.AIDir(), "characters.json"))

	return nil
}

// glossaryExtractionResult is the JSON shape we expect from the model.
type glossaryExtractionResult struct {
	Characters []translation.Character    `json:"characters"`
	Terms      []translation.GlossaryTerm `json:"terms"`
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
