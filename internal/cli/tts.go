package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/tts"
)

// newTTSCmd implements `bookai tts`: synthesizes audio from SSML files using
// the configured TTS engine. The engine is selected from config.yaml
// (tts.engine) and is swappable via the tts.Engine registry. Output WAVs are
// written to audio/chapter_NNN.wav.
func newTTSCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
		merge   bool
	)
	cmd := &cobra.Command{
		Use:   "tts",
		Short: "Synthesize audio from SSML via the configured TTS engine",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runTTS(ctx, proj, force, chapter, chRange, merge)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-synthesize chapters whose audio already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "synthesize only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "synthesize a range of chapter ids, e.g. 5-12")
	cmd.Flags().BoolVar(&merge, "merge", false, "merge per-chapter WAVs into one book.wav")
	return cmd
}

func runTTS(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string, merge bool) error {
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

	if err := proj.EnsureDirs(); err != nil {
		return err
	}

	// Build the engine config from project config.
	engineCfg := buildEngineConfig(proj)
	slog.Info("initializing TTS engine", "engine", engineCfg.Engine)
	engine, err := tts.NewEngine(engineCfg)
	if err != nil {
		return err
	}
	slog.Info("TTS engine ready", "name", engine.Name())

	synthesized := 0
	skipped := 0
	var audioPaths []string

	for _, ch := range chs {
		if ctx.Err() != nil {
			slog.Info("interrupted by signal", "completed", synthesized)
			return ctx.Err()
		}

		ssmlPath := ssmlPath(proj.TTSDir(), ch.ID)
		if !project.Exists(ssmlPath) {
			slog.Debug("skip chapter without SSML", "chapter", ch.ID)
			skipped++
			continue
		}

		if !writeAll && !ids[ch.ID] {
			continue
		}

		audioPath := audioPath(proj.AudioDir(), ch.ID)
		if project.Exists(audioPath) && !force {
			slog.Debug("skip existing audio", "chapter", ch.ID)
			audioPaths = append(audioPaths, audioPath)
			skipped++
			continue
		}

		ssmlContent, err := os.ReadFile(ssmlPath)
		if err != nil {
			return fmt.Errorf("read SSML for chapter %d: %w", ch.ID, err)
		}

		slog.Info("synthesizing chapter", "id", ch.ID, "title", ch.Title)
		if err := engine.Synthesize(ctx, string(ssmlContent), audioPath); err != nil {
			return fmt.Errorf("synthesize chapter %d: %w", ch.ID, err)
		}

		// Update chapter status.
		ch.Status = "audio"
		if err := project.SaveJSON(filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)), ch); err != nil {
			slog.Warn("failed to update chapter status", "chapter", ch.ID, "error", err)
		}

		slog.Info("audio synthesized", "chapter", ch.ID, "file", audioPath)
		audioPaths = append(audioPaths, audioPath)
		synthesized++
	}

	slog.Info("TTS run complete", "synthesized", synthesized, "skipped", skipped)

	if merge && len(audioPaths) > 0 {
		mergedPath := filepath.Join(proj.AudioDir(), "book.wav")
		slog.Info("merging audio files", "count", len(audioPaths), "output", mergedPath)
		if err := mergeAudio(audioPaths, mergedPath); err != nil {
			slog.Warn("merge failed (ffmpeg not available?)", "error", err)
		}
	}

	return nil
}

// buildEngineConfig converts the project's TTS config into an tts.EngineConfig
// with resolved paths.
func buildEngineConfig(proj *project.Project) tts.EngineConfig {
	cfg := proj.Cfg.TTS
	lang := cfg.Language
	if lang == "" {
		lang = proj.Cfg.Languages.Target
	}
	return tts.EngineConfig{
		Engine:      cfg.Engine,
		Language:    lang,
		VoiceSample: resolvePath(proj.Root, cfg.VoiceSample),
		ModelPath:   resolvePath(proj.Root, cfg.ModelPath),
		DataDir:     resolvePath(proj.Root, cfg.DataDir),
		TokensPath:  resolvePath(proj.Root, cfg.TokensPath),
		Python:      cfg.Python,
		Device:      cfg.Device,
		Speed:       cfg.Speed,
		ServerURL:   cfg.ServerURL,
		Speaker:     cfg.Speaker,
	}
}

// resolvePath makes a path absolute relative to the project root if it is
// non-empty and not already absolute.
func resolvePath(root, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// audioPath returns the path for a chapter's audio WAV file.
func audioPath(audioDir string, chapterID int) string {
	return filepath.Join(audioDir, fmt.Sprintf("chapter_%03d.wav", chapterID))
}

// mergeAudio concatenates WAV files into a single output WAV using ffmpeg.
// If ffmpeg is not available, an error is returned.
func mergeAudio(inputs []string, output string) error {
	// Build a concat list file for ffmpeg's concat demuxer.
	listFile, err := os.CreateTemp("", "bookai-concat-*.txt")
	if err != nil {
		return fmt.Errorf("create concat list: %w", err)
	}
	listPath := listFile.Name()
	defer func() { _ = os.Remove(listPath) }()

	for _, p := range inputs {
		// ffmpeg concat requires escaped paths; single quotes work on Linux.
		if _, err := fmt.Fprintf(listFile, "file '%s'\n", p); err != nil {
			return fmt.Errorf("write concat list: %w", err)
		}
	}
	if err := listFile.Close(); err != nil {
		return fmt.Errorf("close concat list: %w", err)
	}

	// #nosec G204 -- ffmpeg is a known binary, args are controlled.
	cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0",
		"-i", listFile.Name(), "-c", "copy", output)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg concat: %w (is ffmpeg installed?)", err)
	}
	return nil
}
