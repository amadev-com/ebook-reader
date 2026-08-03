package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/tts"
)

// newTTSCmd implements `bookai tts`: synthesizes audio from preprocessed text
// files using the configured TTS engine. The engine is selected from
// config.yaml (tts.engine) and is swappable via the tts.Engine registry.
// Engines produce WAV audio; the CLI layer converts to the configured output
// format (MP3 by default) via ffmpeg. Output files are written to
// audio/chapter_NNN.mp3.
func newTTSCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
	)
	cmd := &cobra.Command{
		Use:   "tts",
		Short: "Synthesize audio from preprocessed text via the configured TTS engine",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runTTS(ctx, proj, force, chapter, chRange)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-synthesize chapters whose audio already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "synthesize only a single chapter id (1-based)")
	cmd.Flags().StringVar(&chRange, "range", "", "synthesize a range of chapter ids, e.g. 5-12")
	return cmd
}

func runTTS(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string) error {
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

	// Determine output format and extension.
	audioFormat := proj.Cfg.TTS.AudioFormat
	if audioFormat == "" {
		audioFormat = "mp3"
	}
	audioExt := audioExtension(audioFormat)
	slog.Info("audio output", "format", audioFormat, "extension", audioExt)

	synthesized := 0
	skipped := 0

	for _, ch := range chs {
		if ctx.Err() != nil {
			slog.Info("interrupted by signal", "completed", synthesized)
			return ctx.Err()
		}

		ttsPath := ttsTextPath(proj.TTSDir(), ch.ID)
		if !project.Exists(ttsPath) {
			// Check for legacy SSML files and warn.
			if project.Exists(ssmlPath(proj.TTSDir(), ch.ID)) {
				slog.Warn("found stale .ssml file — run `bookai preprocess` to generate .txt files", "chapter", ch.ID)
			} else {
				slog.Debug("skip chapter without preprocessed text", "chapter", ch.ID)
			}
			skipped++
			continue
		}

		if !writeAll && !ids[ch.ID] {
			continue
		}

		outPath := audioPath(proj.AudioDir(), ch.ID, audioExt)
		if project.Exists(outPath) && !force {
			slog.Debug("skip existing audio", "chapter", ch.ID)
			skipped++
			continue
		}

		textContent, err := os.ReadFile(ttsPath)
		if err != nil {
			return fmt.Errorf("read TTS text for chapter %d: %w", ch.ID, err)
		}

		slog.Info("synthesizing chapter", "id", ch.ID, "title", ch.Title)

		if audioFormat == "wav" {
			// Engine writes WAV directly to the output path.
			if err := engine.Synthesize(ctx, string(textContent), outPath); err != nil {
				return fmt.Errorf("synthesize chapter %d: %w", ch.ID, err)
			}
		} else {
			// Engine writes WAV to a temp file, then we convert to the target format.
			wavPath := tempWAVPath(proj.AudioDir(), ch.ID)
			if err := engine.Synthesize(ctx, string(textContent), wavPath); err != nil {
				return fmt.Errorf("synthesize chapter %d: %w", ch.ID, err)
			}
			if err := convertAudio(wavPath, outPath, audioFormat, proj.Cfg.TTS.AudioBitrate); err != nil {
				_ = os.Remove(wavPath)
				return fmt.Errorf("convert chapter %d to %s: %w", ch.ID, audioFormat, err)
			}
			_ = os.Remove(wavPath)
		}

		// Update chapter status.
		ch.Status = "audio"
		if err := project.SaveJSON(filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)), ch); err != nil {
			slog.Warn("failed to update chapter status", "chapter", ch.ID, "error", err)
		}

		slog.Info("audio synthesized", "chapter", ch.ID, "file", outPath)
		synthesized++
	}

	slog.Info("TTS run complete", "synthesized", synthesized, "skipped", skipped)
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

// audioExtension returns the file extension for the given audio format.
func audioExtension(format string) string {
	switch strings.ToLower(format) {
	case "wav":
		return ".wav"
	default:
		return "." + strings.ToLower(format)
	}
}

// audioPath returns the path for a chapter's audio file.
func audioPath(audioDir string, chapterID int, ext string) string {
	return filepath.Join(audioDir, fmt.Sprintf("chapter_%03d%s", chapterID, ext))
}

// tempWAVPath returns a temporary WAV path for intermediate engine output
// before format conversion.
func tempWAVPath(audioDir string, chapterID int) string {
	return filepath.Join(audioDir, fmt.Sprintf(".chapter_%03d.tmp.wav", chapterID))
}

// convertAudio converts a WAV file to the target format (e.g. MP3) using
// ffmpeg. The output is mono, at the given bitrate for lossy formats.
func convertAudio(input, output, format, bitrate string) error {
	args := []string{"-y", "-i", input}

	switch strings.ToLower(format) {
	case "mp3":
		args = append(args,
			"-ac", "1", // mono
			"-c:a", "libmp3lame",
			"-b:a", normalizeBitrate(bitrate),
		)
	case "aac":
		args = append(args,
			"-ac", "1",
			"-c:a", "aac",
			"-b:a", normalizeBitrate(bitrate),
		)
	default:
		// For unknown formats, let ffmpeg pick the codec.
		args = append(args, "-ac", "1")
	}

	args = append(args, output)

	// #nosec G204 -- ffmpeg is a known binary, args are controlled.
	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// normalizeBitrate ensures the bitrate string has a "k" suffix (e.g. "128" →
// "128k"). Empty defaults to "128k".
func normalizeBitrate(bitrate string) string {
	if bitrate == "" {
		return "128k"
	}
	if !strings.HasSuffix(strings.ToLower(bitrate), "k") {
		return bitrate + "k"
	}
	return bitrate
}
