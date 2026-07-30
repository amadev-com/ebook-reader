package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/tts"
)

// newSSMLCmd implements `bookai ssml`: converts each chapter's translation
// into an SSML file with pronunciation hints from ai/pronunciation.json.
// The SSML files are written to tts/chapter_NNN.ssml and are the input to
// the `bookai tts` command.
func newSSMLCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
	)
	cmd := &cobra.Command{
		Use:   "ssml",
		Short: "Generate SSML with pronunciation hints from translations",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runSSML(ctx, proj, force, chapter, chRange)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "regenerate SSML for chapters whose .ssml file already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "generate SSML for a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "generate SSML for a range of chapter ids, e.g. 5-12")
	return cmd
}

func runSSML(_ context.Context, proj *project.Project, force bool, chapter int, chRange string) error {
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil {
		return err
	}
	writeAll := ids == nil

	// Load pronunciation hints (optional — may be empty).
	pron, err := tts.LoadPronunciation(proj.AIDir())
	if err != nil {
		return err
	}
	if len(pron.Entries) > 0 {
		slog.Info("loaded pronunciation hints", "count", len(pron.Entries))
	} else {
		slog.Info("no pronunciation hints found (ai/pronunciation.json absent or empty)")
	}

	targetLang := proj.Cfg.Languages.Target
	if err := proj.EnsureDirs(); err != nil {
		return err
	}

	generated := 0
	skipped := 0
	for _, ch := range chs {
		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		if !project.Exists(translationPath) {
			slog.Debug("skip chapter without translation", "chapter", ch.ID)
			skipped++
			continue
		}

		if !writeAll && !ids[ch.ID] {
			continue
		}

		ssmlPath := ssmlPath(proj.TTSDir(), ch.ID)
		if project.Exists(ssmlPath) && !force {
			slog.Debug("skip existing SSML", "chapter", ch.ID)
			skipped++
			continue
		}

		text, err := os.ReadFile(translationPath)
		if err != nil {
			return fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
		}

		doc := tts.ParseTranslation(string(text), pron)
		ssmlContent := doc.Render()

		if err := project.SaveBytes(ssmlPath, []byte(ssmlContent)); err != nil {
			return fmt.Errorf("write SSML for chapter %d: %w", ch.ID, err)
		}

		// Update chapter status.
		ch.Status = "ssml"
		if err := project.SaveJSON(filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)), ch); err != nil {
			slog.Warn("failed to update chapter status", "chapter", ch.ID, "error", err)
		}

		slog.Info("SSML generated", "chapter", ch.ID, "paragraphs", len(doc.Paragraphs))
		generated++
	}

	slog.Info("SSML run complete", "generated", generated, "skipped", skipped)
	return nil
}

// ssmlPath returns the path for a chapter's SSML file.
func ssmlPath(ttsDir string, chapterID int) string {
	return filepath.Join(ttsDir, fmt.Sprintf("chapter_%03d.ssml", chapterID))
}
