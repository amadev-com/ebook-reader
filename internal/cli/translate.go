package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// newTranslateCmd implements `bookai translate`: translates chapters to the
// target language using the glossary for consistency and per-chapter memory
// summaries for continuity. Each chapter is translated in three steps:
//  1. Translate the chapter text (translation model, e.g. gpt-4.1).
//  2. Generate a summary for future context (helper model, e.g. gpt-4.1-mini).
//  3. Extract any new glossary terms (helper model, JSON mode).
//
// The translation is written to translation/chapter_NNN.<target>.txt, the
// summary to memory/chapter_NNN.summary.txt, and the chapter's status is
// updated to "translated". New terms are merged into ai/glossary.json.
func newTranslateCmd() *cobra.Command {
	var (
		force     bool
		chapter   int
		chRange   string
		skipMem   bool
		skipGloss bool
	)
	cmd := &cobra.Command{
		Use:   "translate",
		Short: "Translate chapters to the target language using glossary + memory",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runTranslate(ctx, proj, force, chapter, chRange, skipMem, skipGloss)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-translate chapters whose translation already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "translate only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "translate a range of chapter ids, e.g. 5-12")
	cmd.Flags().BoolVar(&skipMem, "skip-memory", false, "skip summary generation (faster, less context continuity)")
	cmd.Flags().BoolVar(&skipGloss, "skip-glossary-update", false, "skip new-term extraction after each chapter")
	return cmd
}

func runTranslate(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string, skipMem, skipGloss bool) error {
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

	// Load the glossary + characters and merge them for translation context.
	// Characters are stored separately (characters.json) but need to be in the
	// glossary block for consistent name translation.
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

	// Create the OpenAI client.
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}
	slog.Info("openai client ready",
		"translate_model", client.TranslateModel(), "helper_model", client.HelperModel())

	targetLang := proj.Cfg.Languages.Target
	translated := 0
	skipped := 0

	for _, ch := range chs {
		// Check context cancellation.
		if ctx.Err() != nil {
			slog.Info("interrupted by signal", "completed", translated)
			return ctx.Err()
		}

		if !writeAll && !ids[ch.ID] {
			continue
		}

		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		if project.Exists(translationPath) && !force {
			slog.Debug("skip existing translation", "chapter", ch.ID)
			skipped++
			continue
		}

		slog.Info("translating chapter", "id", ch.ID, "title", ch.Title)

		// Build the system prompt: persona + glossary + previous summaries.
		glossaryBlock := glossary.PromptBlock()
		prevContext := ""
		if !skipMem {
			prevContext, err = translation.PreviousSummaries(proj.MemoryDir(), ch.ID, 2)
			if err != nil {
				return fmt.Errorf("load previous summaries: %w", err)
			}
		}
		systemPrompt := translation.System(glossaryBlock, prevContext)

		// Step 1: translate.
		resp, err := client.Chat(ctx, translation.ChatRequest{
			System: systemPrompt,
			User:   translation.User(translation.ChapterInfo{ID: ch.ID, Title: ch.Title}, ch.Source),
			Model:  client.TranslateModel(),
		})
		if err != nil {
			return fmt.Errorf("translate chapter %d: %w", ch.ID, err)
		}
		slog.Info("chapter translated",
			"chapter", ch.ID, "prompt_tokens", resp.Usage.PromptTokens,
			"completion_tokens", resp.Usage.CompletionTokens)

		// Write the translation.
		if err := project.SaveBytes(translationPath, []byte(strings.TrimSpace(resp.Content)+"\n")); err != nil {
			return fmt.Errorf("write translation for chapter %d: %w", ch.ID, err)
		}

		// Step 2: generate summary for future context.
		if !skipMem {
			summaryResp, err := client.Chat(ctx, translation.ChatRequest{
				System: translation.SummarySystem,
				User:   translation.SummaryUser(translation.ChapterInfo{ID: ch.ID, Title: ch.Title}, ch.Source),
				Model:  client.HelperModel(),
			})
			if err != nil {
				slog.Warn("failed to generate summary, continuing", "chapter", ch.ID, "error", err)
			} else {
				if err := translation.SaveSummary(proj.MemoryDir(), ch.ID, summaryResp.Content); err != nil {
					slog.Warn("failed to save summary, continuing", "chapter", ch.ID, "error", err)
				}
			}
		}

		// Step 3: extract new glossary terms.
		if !skipGloss {
			newTermsResp, err := client.Chat(ctx, translation.ChatRequest{
				System:   translation.NewTermsSystem,
				User:     translation.NewTermsUser(translation.ChapterInfo{ID: ch.ID, Title: ch.Title}, ch.Source, resp.Content),
				Model:    client.HelperModel(),
				JSONMode: true,
			})
			if err != nil {
				slog.Warn("failed to extract new terms, continuing", "chapter", ch.ID, "error", err)
			} else {
				var newTerms struct {
					Terms []translation.GlossaryTerm `json:"terms"`
				}
				if err := json.Unmarshal([]byte(newTermsResp.Content), &newTerms); err != nil {
					slog.Warn("failed to parse new terms JSON, continuing", "chapter", ch.ID, "error", err)
				} else {
					added := glossary.Merge(newTerms.Terms)
					if added > 0 {
						if err := glossary.Save(proj.AIDir()); err != nil {
							slog.Warn("failed to save updated glossary", "error", err)
						} else {
							slog.Info("glossary updated", "new_terms", added, "chapter", ch.ID)
						}
					}
				}
			}
		}

		// Update the chapter status.
		ch.Status = "translated"
		if err := project.SaveJSON(filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)), ch); err != nil {
			slog.Warn("failed to update chapter status", "chapter", ch.ID, "error", err)
		}

		translated++
	}

	slog.Info("translation run complete", "translated", translated, "skipped", skipped)
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

// Ensure os import is used (os.IsNotExist is used in translation package, but
// we reference os here for potential future use).
var _ = os.Stat
