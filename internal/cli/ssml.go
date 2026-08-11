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
// into an SSML file with stress marks and SSML tags (<speak>, <p>, <s>) for
// Silero TTS. The output files are written to tts/chapter_NNN.ssml and are
// the input to the `bookai tts` command.
//
// By default, stress marks come from the global ai/stress.json (built by
// `bookai pronounce`). With --auto-stress, the silero-stress model on the
// TTS server is used instead — no stress.json needed. Config pronunciation
// overrides are always applied on top.
func newSSMLCmd() *cobra.Command {
	var (
		force      bool
		chapter    int
		chRange    string
		autoStress bool
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
			return runSSML(ctx, proj, force, chapter, chRange, autoStress)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "regenerate SSML for chapters whose .ssml file already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "process a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "process a range of chapter ids, e.g. 5-12")
	cmd.Flags().BoolVar(&autoStress, "auto-stress", false, "use silero-stress model on TTS server instead of ai/stress.json (config overrides still apply)")
	return cmd
}

func runSSML(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string, autoStress bool) error {
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

	// Build the override stress store from config pronunciation overrides.
	// These are always applied (on top of either stress.json or auto-stress).
	var overrides *tts.Stress
	if len(proj.Cfg.Pronunciation) > 0 {
		ovs := make([]tts.StressOverride, len(proj.Cfg.Pronunciation))
		for i, p := range proj.Cfg.Pronunciation {
			ovs[i] = tts.StressOverride{Term: p.Term, Phonemes: p.Phonemes}
		}
		overrides = tts.NewStressFromOverrides(ovs)
		slog.Info("loaded config pronunciation overrides", "entries", len(overrides.Entries))
	}

	// Stress source: either auto-stress (silero-stress model on TTS server)
	// or the global ai/stress.json vocabulary.
	var stressClient *tts.StressClient
	var stress *tts.Stress
	if autoStress {
		if proj.Cfg.TTS.ServerURL == "" {
			return fmt.Errorf("--auto-stress requires tts.server_url to be set in config")
		}
		stressClient = tts.NewStressClient(proj.Cfg.TTS.ServerURL)
		slog.Info("using auto-stress (silero-stress model on TTS server)", "server_url", proj.Cfg.TTS.ServerURL)
	} else {
		stress, err = tts.LoadStress(proj.AIDir())
		if err != nil {
			return fmt.Errorf("load stress vocabulary: %w", err)
		}
		slog.Info("loaded stress vocabulary", "entries", len(stress.Entries))
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

		var processed string
		if autoStress {
			// Send text to the silero-stress model on the TTS server.
			stressed, err := stressClient.StressText(ctx, string(text))
			if err != nil {
				return fmt.Errorf("auto-stress chapter %d: %w", ch.ID, err)
			}
			processed = stressed
			// Apply config overrides on top of the model output.
			if overrides != nil {
				processed = overrides.Apply(processed)
			}
		} else {
			// Apply stress marks from stress.json (term → stressed form).
			processed = stress.Apply(string(text))
			// Apply config overrides on top.
			if overrides != nil {
				processed = overrides.Apply(processed)
			}
		}

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

		slog.Info("SSML generated", "chapter", ch.ID, "auto_stress", autoStress)
		generated++
	}

	slog.Info("ssml run complete", "generated", generated, "skipped", skipped)
	return nil
}

// ssmlFilePath returns the path for a chapter's SSML file.
func ssmlFilePath(ttsDir string, chapterID int) string {
	return filepath.Join(ttsDir, fmt.Sprintf("chapter_%03d.ssml", chapterID))
}
