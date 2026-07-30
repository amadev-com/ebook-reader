package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
)

// newVerifyGlossaryCmd implements `bookai verify-glossary`: scans all
// translations for glossary source terms and reports any that appear
// untranslated (still in English) in the Russian text, indicating the model
// missed or ignored a glossary entry.
func newVerifyGlossaryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify-glossary",
		Short: "Check translations for untranslated or inconsistent glossary terms",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runVerifyGlossary(ctx, proj)
		},
	}
	return cmd
}

// GlossaryViolation is one inconsistency found by verify-glossary.
type GlossaryViolation struct {
	ChapterID          int    `json:"chapter_id"`
	SourceTerm         string `json:"source_term"`
	ExpectedTarget     string `json:"expected_target"`
	FoundInTranslation string `json:"found_in_translation,omitempty"`
}

func runVerifyGlossary(_ context.Context, proj *project.Project) error {
	glossary, err := translation.LoadGlossary(proj.AIDir())
	if err != nil {
		return err
	}
	characters, err := translation.LoadCharacters(proj.AIDir())
	if err != nil {
		slog.Warn("failed to load characters", "error", err)
		characters = &translation.Characters{}
	}
	glossary = glossary.WithCharacters(characters)
	if len(glossary.Terms) == 0 {
		return fmt.Errorf("no glossary found — run `bookai analyze` first")
	}
	slog.Info("loaded glossary", "terms", len(glossary.Terms), "characters", len(characters.Characters))

	translatedIDs, err := loadTranslatedChapters(proj)
	if err != nil {
		return err
	}
	if len(translatedIDs) == 0 {
		return fmt.Errorf("no translated chapters found — run `bookai translate` first")
	}
	slog.Info("found translated chapters", "count", len(translatedIDs))

	targetLang := proj.Cfg.Languages.Target
	var violations []GlossaryViolation

	for _, chID := range translatedIDs {
		path := translationPath(proj.TranslationDir(), chID, targetLang)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read translation for chapter %d: %w", chID, err)
		}
		translationText := string(data)

		for _, term := range glossary.Terms {
			// Check if the English source term appears in the Russian
			// translation (case-insensitive). This indicates the model
			// left it untranslated.
			if term.Source == "" {
				continue
			}
			if containsCI(translationText, term.Source) {
				violations = append(violations, GlossaryViolation{
					ChapterID:          chID,
					SourceTerm:         term.Source,
					ExpectedTarget:     term.Target,
					FoundInTranslation: term.Source,
				})
			}
		}
	}

	// Report.
	if len(violations) == 0 {
		fmt.Printf("No violations found. All %d glossary terms are consistently translated across %d chapters.\n",
			len(glossary.Terms), len(translatedIDs))
		return nil
	}

	fmt.Printf("Found %d glossary violation(s) across %d translated chapters:\n\n",
		len(violations), len(translatedIDs))
	for _, v := range violations {
		fmt.Printf("  Chapter %d: %q appears untranslated (should be %q)\n",
			v.ChapterID, v.SourceTerm, v.ExpectedTarget)
	}

	// Write violations to ai/glossary_violations.json for review.
	violationsPath := filepath.Join(proj.AIDir(), "glossary_violations.json")
	if err := project.SaveJSON(violationsPath, violations); err != nil {
		return fmt.Errorf("write violations: %w", err)
	}
	fmt.Printf("\nViolations written to %s\n", violationsPath)
	return nil
}

// containsCI reports whether s contains substr, case-insensitive.
func containsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
