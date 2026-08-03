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

// newPreprocessCmd implements `bookai preprocess`: converts each chapter's
// translation into a TTS-ready text file with phonetic respellings from
// ai/respelling.json and Russian text normalization rules. The output files
// are written to tts/chapter_NNN.txt and are the input to the `bookai tts`
// command.
func newPreprocessCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
	)
	cmd := &cobra.Command{
		Use:   "preprocess",
		Short: "Apply respellings and text normalization to translations for TTS",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runPreprocess(ctx, proj, force, chapter, chRange)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "regenerate TTS text for chapters whose .txt file already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "preprocess a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "preprocess a range of chapter ids, e.g. 5-12")
	return cmd
}

func runPreprocess(_ context.Context, proj *project.Project, force bool, chapter int, chRange string) error {
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

		ttsPath := ttsTextPath(proj.TTSDir(), ch.ID)
		if project.Exists(ttsPath) && !force {
			slog.Debug("skip existing TTS text", "chapter", ch.ID)
			skipped++
			continue
		}

		text, err := os.ReadFile(translationPath)
		if err != nil {
			return fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
		}

		// Load per-chapter respellings (ai/respelling_NNN.json).
		resp, err := tts.LoadChapterRespelling(proj.AIDir(), ch.ID)
		if err != nil {
			return fmt.Errorf("load respelling for chapter %d: %w", ch.ID, err)
		}

		// Apply respellings (term → phonetic replacement).
		processed := resp.Apply(string(text))
		// Apply Russian text normalization for XTTS v2.
		processed = tts.NormalizeRussian(processed)

		if err := project.SaveBytes(ttsPath, []byte(processed)); err != nil {
			return fmt.Errorf("write TTS text for chapter %d: %w", ch.ID, err)
		}

		// Update chapter status.
		ch.Status = "preprocessed"
		if err := project.SaveJSON(filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)), ch); err != nil {
			slog.Warn("failed to update chapter status", "chapter", ch.ID, "error", err)
		}

		slog.Info("TTS text generated", "chapter", ch.ID, "respellings", len(resp.Entries))
		generated++
	}

	slog.Info("preprocess run complete", "generated", generated, "skipped", skipped)
	return nil
}

// ttsTextPath returns the path for a chapter's preprocessed TTS text file.
func ttsTextPath(ttsDir string, chapterID int) string {
	return filepath.Join(ttsDir, fmt.Sprintf("chapter_%03d.txt", chapterID))
}

// ssmlPath is kept for backward compatibility checks — returns the old SSML
// path so the tts command can clean up stale files.
func ssmlPath(ttsDir string, chapterID int) string {
	return filepath.Join(ttsDir, fmt.Sprintf("chapter_%03d.ssml", chapterID))
}
