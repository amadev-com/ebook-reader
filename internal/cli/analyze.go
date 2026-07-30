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
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// newAnalyzeCmd implements `bookai analyze`: extracts a glossary and character
// list from the book by sending chapter titles + opening snippets to the
// helper model (gpt-4.1-mini). This is a cheap pre-translation pass that
// establishes terminology consistency before any chapter is translated.
func newAnalyzeCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
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
			return runAnalyze(ctx, proj, force, chapter, chRange)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-analyze even if glossary.json already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "analyze only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "analyze a range of chapter ids, e.g. 5-12")
	return cmd
}

// runAnalyze loads chapters, builds snippets, calls the helper model for
// glossary extraction, and writes ai/glossary.json + ai/characters.json.
// When chapter/chRange are set, only the selected chapters are included in
// the snippets sent to the model.
func runAnalyze(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string) error {
	glossaryPath := filepath.Join(proj.AIDir(), "glossary.json")
	if project.Exists(glossaryPath) && !force {
		slog.Info("glossary already exists, skipping (use --force to re-analyze)", "path", glossaryPath)
		return nil
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

	// Build snippets: title + first ~500 chars of source.
	snippets := buildSnippets(chs)
	slog.Info("built chapter snippets", "count", len(snippets), "snippet_chars", 500)

	// Create the OpenAI client.
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}
	slog.Info("openai client ready", "helper_model", client.HelperModel())

	// Call the helper model for glossary extraction.
	slog.Info("calling model for glossary extraction", "model", client.HelperModel())
	resp, err := client.Chat(ctx, translation.ChatRequest{
		System:   translation.GlossaryExtractionSystem,
		User:     translation.GlossaryExtractionUser(snippets),
		Model:    client.HelperModel(),
		JSONMode: true,
	})
	if err != nil {
		return err
	}
	slog.Info("glossary extraction complete", "prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens)

	// Parse the JSON response.
	var result glossaryExtractionResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return fmt.Errorf("parse glossary JSON: %w (content: %s)", err, truncate(resp.Content, 200))
	}

	// Build and save the glossary.
	glossary := &translation.Glossary{}
	for _, c := range result.Characters {
		glossary.Terms = append(glossary.Terms, translation.GlossaryTerm{
			Source: c.Name,
			Target: c.Translation,
			Type:   "character",
		})
	}
	glossary.Terms = append(glossary.Terms, result.Terms...)
	slog.Info("extracted glossary", "terms", len(glossary.Terms),
		"characters", len(result.Characters), "other_terms", len(result.Terms))

	if err := glossary.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved glossary", "path", glossaryPath)

	// Build and save the characters store.
	characters := &translation.Characters{Characters: result.Characters}
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

// buildSnippets converts chapters to the ChapterSnippet format, taking the
// first ~500 characters of each chapter's source text.
func buildSnippets(chs []chapters.Chapter) []translation.ChapterSnippet {
	snippets := make([]translation.ChapterSnippet, len(chs))
	for i, ch := range chs {
		snippet := ch.Source
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		snippets[i] = translation.ChapterSnippet{
			Title:   ch.Title,
			Snippet: snippet,
		}
	}
	return snippets
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Ensure the import is used (strings is used in buildSnippets via truncate).
var _ = strings.TrimSpace
