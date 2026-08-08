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
// into an SSML file with stress marks from the global ai/stress.json and
// SSML tags (<speak>, <p>, <s>) for Silero TTS. The output files are written
// to tts/chapter_NNN.ssml and are the input to the `bookai tts` command.
func newSSMLCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
	)
	cmd := &cobra.Command{
		Use:   "ssml",
		Short: "Apply stress marks and wrap translations in SSML for Silero TTS",
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
	cmd.Flags().IntVar(&chapter, "chapter", 0, "process a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "process a range of chapter ids, e.g. 5-12")
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

	targetLang := proj.Cfg.Languages.Target
	if err := proj.EnsureDirs(); err != nil {
		return err
	}

	// Load the global stress vocabulary.
	stress, err := tts.LoadStress(proj.AIDir())
	if err != nil {
		return fmt.Errorf("load stress vocabulary: %w", err)
	}
	slog.Info("loaded stress vocabulary", "entries", len(stress.Entries))

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

		ssmlPath := ssmlFilePath(proj.TTSDir(), ch.ID)
		if project.Exists(ssmlPath) && !force {
			slog.Debug("skip existing SSML", "chapter", ch.ID)
			skipped++
			continue
		}

		text, err := os.ReadFile(translationPath)
		if err != nil {
			return fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
		}

		// Apply stress marks (term → stressed form with + before vowel).
		processed := stress.Apply(string(text))
		// Generate SSML (wrap in <speak>/<p>/<s> tags).
		ssmlText := tts.GenerateSSML(processed)

		if err := project.SaveBytes(ssmlPath, []byte(ssmlText)); err != nil {
			return fmt.Errorf("write SSML for chapter %d: %w", ch.ID, err)
		}

		// Update chapter status.
		ch.Status = "ssml"
		if err := project.SaveJSON(filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)), ch); err != nil {
			slog.Warn("failed to update chapter status", "chapter", ch.ID, "error", err)
		}

		slog.Info("SSML generated", "chapter", ch.ID, "stress_entries", len(stress.Entries))
		generated++
	}

	slog.Info("ssml run complete", "generated", generated, "skipped", skipped)
	return nil
}

// ssmlFilePath returns the path for a chapter's SSML file.
func ssmlFilePath(ttsDir string, chapterID int) string {
	return filepath.Join(ttsDir, fmt.Sprintf("chapter_%03d.ssml", chapterID))
}
