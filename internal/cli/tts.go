package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/project"
	"ebook-reader/internal/tts"
)

// newTTSCmd implements `bookai tts`: synthesizes audio from SSML files using
// the configured TTS engine. The engine is selected from config.yaml
// (tts.engine) and is swappable via the tts.Engine registry. Engines produce
// WAV audio; the CLI layer converts to the configured output format (MP3 by
// default) via ffmpeg. Output files are written to audio/chapter_NNN.mp3.
func newTTSCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		chRange string
	)
	cmd := &cobra.Command{
		Use:   "tts",
		Short: "Synthesize audio from SSML via the configured TTS engine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger()
			ctx := cmd.Context()
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

// runTTS synthesizes speech for the project's chapters, optionally filtering by chapter or range. It respects cancellation and can force regeneration of existing audio.
func runTTS(ctx context.Context, proj *project.Project, force bool, chapter int, chRange string) error {
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	ids, err := parseChapterFilter(chapter, chRange, len(chs))
	if err != nil && !errors.Is(err, errNoChapterFilter) {
		return err
	}
	writeAll := errors.Is(err, errNoChapterFilter)

	if err = proj.EnsureDirs(); err != nil {
		return err
	}

	// Build the engine config from project config.
	engineCfg := buildEngineConfig(proj)
	slog.Default().InfoContext(ctx, "initializing TTS engine", "engine", engineCfg.Engine)
	engine, err := tts.NewEngine(engineCfg)
	if err != nil {
		return err
	}
	slog.Default().InfoContext(ctx, "TTS engine ready", "name", engine.Name())

	// Determine output format and extension.
	audioFormat := resolveAudioFormat(proj.Cfg.TTS.AudioFormat)
	audioExt := audioExtension(audioFormat)
	slog.Default().InfoContext(ctx, "audio output", "format", audioFormat, "extension", audioExt)

	synthesized := 0
	skipped := 0
	var s, sk int
	for _, ch := range chs {
		if ctx.Err() != nil {
			slog.Default().InfoContext(ctx, "interrupted by signal", "completed", synthesized)
			return ctx.Err()
		}

		if s, sk, err = synthesizeChapter(
			ctx,
			ch,
			proj,
			ids,
			writeAll,
			force,
			engine,
			audioFormat,
			audioExt,
		); err != nil {
			return err
		}
		synthesized += s
		skipped += sk
	}

	slog.Default().InfoContext(ctx, "TTS run complete", "synthesized", synthesized, "skipped", skipped)
	return nil
}

// resolveAudioFormat returns the configured audio format, defaulting to MP3.
func resolveAudioFormat(format string) string {
	if format == "" {
		return audioFormatMP3
	}
	return format
}

// synthesizeChapter synthesizes a single chapter's audio from its SSML file.
// Returns the synthesized (1 or 0) and skipped (1 or 0) counts.
func synthesizeChapter(
	ctx context.Context,
	ch chapters.Chapter,
	proj *project.Project,
	ids map[int]bool,
	writeAll, force bool,
	engine tts.Engine,
	audioFormat, audioExt string,
) (int, int, error) {
	if !writeAll && !ids[ch.ID] {
		return 0, 0, nil
	}

	ssmlPath := ssmlFilePath(proj.TTSDir(), ch.ID)
	if !project.Exists(ssmlPath) {
		slog.Default().DebugContext(ctx, "skip chapter without SSML", "chapter", ch.ID)
		return 0, 1, nil
	}

	outPath := audioPath(proj.AudioDir(), ch.ID, audioExt)
	if project.Exists(outPath) && !force {
		slog.Default().DebugContext(ctx, "skip existing audio", "chapter", ch.ID)
		return 0, 1, nil
	}

	ssmlContent, err := os.ReadFile(ssmlPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read SSML for chapter %d: %w", ch.ID, err)
	}

	slog.Default().InfoContext(ctx, "synthesizing chapter", "id", ch.ID, "title", ch.Title)

	if err = synthesizeAudio(ctx, engine, string(ssmlContent), outPath, audioFormat, proj, ch.ID); err != nil {
		return 0, 0, err
	}

	// Update chapter status.
	ch.Status = "audio"
	err = project.SaveJSON(
		filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)),
		ch,
	)
	if err != nil {
		slog.Default().WarnContext(ctx, "failed to update chapter status", "chapter", ch.ID, "error", err)
	}

	slog.Default().InfoContext(ctx, "audio synthesized", "chapter", ch.ID, "file", outPath)
	return 1, 0, nil
}

// synthesizeAudio runs the TTS engine and converts the output to the target
// format if needed.
func synthesizeAudio(
	ctx context.Context,
	engine tts.Engine,
	ssmlContent, outPath, audioFormat string,
	proj *project.Project,
	chID int,
) error {
	if audioFormat == "wav" {
		// Engine writes WAV directly to the output path.
		if err := engine.Synthesize(ctx, ssmlContent, outPath); err != nil {
			return fmt.Errorf("synthesize chapter %d: %w", chID, err)
		}
		return nil
	}
	// Engine writes WAV to a temp file, then we convert to the target format.
	wavPath := tempWAVPath(proj.AudioDir(), chID)
	if err := engine.Synthesize(ctx, ssmlContent, wavPath); err != nil {
		return fmt.Errorf("synthesize chapter %d: %w", chID, err)
	}
	if err := convertAudio(ctx, wavPath, outPath, audioFormat, proj.Cfg.TTS.AudioBitrate); err != nil {
		_ = os.Remove(wavPath)
		return fmt.Errorf("convert chapter %d to %s: %w", chID, audioFormat, err)
	}
	_ = os.Remove(wavPath)
	return nil
}

// buildEngineConfig converts the project's TTS config into an tts.EngineConfig.
func buildEngineConfig(proj *project.Project) tts.EngineConfig {
	cfg := proj.Cfg.TTS
	lang := cfg.Language
	if lang == "" {
		lang = proj.Cfg.Languages.Target
	}
	return tts.EngineConfig{
		Engine:     cfg.Engine,
		Language:   lang,
		Voice:      cfg.Voice,
		Speed:      cfg.Speed,
		Pitch:      cfg.Pitch,
		SampleRate: cfg.SampleRate,
		ServerURL:  cfg.ServerURL,
		Parallel:   cfg.Parallel,
	}
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
// ffmpeg. The output is mono, at the given bitrate for lossy formats. The
// conversion writes to a temporary file in the same directory and renames
// atomically on success, so a cancelled or failed conversion never leaves a
// partially-written output file that would cause later runs to skip the chapter.
func convertAudio(ctx context.Context, input, output, format, bitrate string) error {
	// Build a temp name that preserves the real extension (so ffmpeg can
	// infer the muxer) but is clearly temporary and hidden from countFiles.
	// e.g. "chapter_001.mp3" → ".chapter_001.tmp.mp3"
	dir, base := filepath.Split(output)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	tmpOut := filepath.Join(dir, "."+stem+".tmp"+ext)
	args := []string{"-y", "-i", input}

	switch strings.ToLower(format) {
	case audioFormatMP3:
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

	args = append(args, tmpOut)

	// #nosec G204 -- ffmpeg is a known binary, args are controlled.
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpOut)
		return fmt.Errorf("ffmpeg: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmpOut, output); err != nil {
		_ = os.Remove(tmpOut)
		return fmt.Errorf("rename temp output to %s: %w", output, err)
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
