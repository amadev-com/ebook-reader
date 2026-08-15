package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/config"
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogger()
			ctx := cmd.Context()
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
	cmd.Flags().
		BoolVar(&autoStress, "auto-stress", false, "use silero-stress model on TTS server instead of ai/stress.json (config overrides still apply)")
	return cmd
}

// runSSML generates SSML files for the project's translated chapters, applying the
// configured pronunciation stress source and processing options. It returns an
// error if chapter loading, filtering, directory setup, stress-source setup, or
// chapter processing fails.
func runSSML(
	ctx context.Context,
	proj *project.Project,
	force bool,
	chapter int,
	chRange string,
	autoStress bool,
) error {
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

	targetLang := proj.Cfg.Languages.Target
	if err = proj.EnsureDirs(); err != nil {
		return err
	}

	// Build the override stress store from config pronunciation overrides.
	// These are always applied (on top of either stress.json or auto-stress).
	overrides := buildStressOverrides(proj.Cfg.Pronunciation)

	// Stress source: either auto-stress (silero-stress model on TTS server)
	// or the global ai/stress.json vocabulary.
	stressClient, stress, err := setupStressSource(proj, autoStress)
	if err != nil {
		return err
	}

	generated := 0
	skipped := 0

	// Pre-scan for Latin words in translations that will be processed.
	// Silero TTS cannot handle Latin characters — they cause crashes or
	// silence. If found, warn the user and offer to transliterate.
	transliterate, err := scanLatinWords(ctx, chs, proj, targetLang, ids, writeAll, force)
	if err != nil {
		return err
	}

	var g, s int
	for _, ch := range chs {
		g, s, err = processSSMLChapter(
			ctx,
			ch,
			proj,
			targetLang,
			ids,
			writeAll,
			force,
			autoStress,
			transliterate,
			overrides,
			stressClient,
			stress,
		)
		if err != nil {
			return err
		}
		generated += g
		skipped += s
	}

	slog.Default().InfoContext(ctx, "ssml run complete", "generated", generated, "skipped", skipped)
	return nil
}

// scanLatinWords pre-scans chapters that will be processed for Latin words.
// If any are found, it lists them and prompts the user: transliterate (proceed)
// or abort. Returns true if the user chose to transliterate, false if no Latin
// words were found.
func scanLatinWords(
	ctx context.Context,
	chs []chapters.Chapter,
	proj *project.Project,
	targetLang string,
	ids map[int]bool,
	writeAll, force bool,
) (bool, error) {
	type chapterLatin struct {
		chID  int
		words []string
	}
	var found []chapterLatin

	for _, ch := range chs {
		if !writeAll && !ids[ch.ID] {
			continue
		}
		// Skip chapters that won't be regenerated.
		ssmlPath := ssmlFilePath(proj.TTSDir(), ch.ID)
		if project.Exists(ssmlPath) && !force {
			continue
		}
		translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
		if !project.Exists(translationPath) {
			continue
		}
		data, err := os.ReadFile(translationPath)
		if err != nil {
			return false, fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
		}
		words := tts.FindLatinWords(string(data))
		if len(words) > 0 {
			found = append(found, chapterLatin{chID: ch.ID, words: words})
		}
	}

	if len(found) == 0 {
		return false, nil
	}

	// Report.
	fmt.Fprintf(os.Stdout,
		"\nFound Latin words in %d chapter(s) — Silero TTS cannot handle Latin characters:\n", len(found))
	for _, c := range found {
		fmt.Fprintf(os.Stdout, "  Chapter %d: %s\n", c.chID, strings.Join(c.words, ", "))
	}
	fmt.Fprintf(os.Stdout, "\nOptions:\n")
	fmt.Fprintf(os.Stdout, "  [1] Transliterate Latin words to Cyrillic and proceed\n")
	fmt.Fprintf(os.Stdout, "  [2] Abort (fix translations first)\n")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprintf(os.Stdout, "Choose: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, fmt.Errorf("read input: %w", err)
		}
		line = strings.TrimSpace(line)
		switch line {
		case "1":
			slog.Default().InfoContext(ctx, "user chose to transliterate Latin words")
			return true, nil
		case "2":
			return false, fmt.Errorf("aborted by user — fix Latin words in translations before proceeding")
		default:
			fmt.Fprintf(os.Stdout, "  invalid choice: enter 1 or 2\n")
		}
	}
}

// buildStressOverrides converts config pronunciation overrides into a
// tts.Stress store. Returns nil if there are no overrides.
func buildStressOverrides(pronOverrides []config.PronunciationOverride) *tts.Stress {
	if len(pronOverrides) == 0 {
		return nil
	}
	ovs := make([]tts.StressOverride, len(pronOverrides))
	for i, p := range pronOverrides {
		ovs[i] = tts.StressOverride{Term: p.Term, Phonemes: p.Phonemes}
	}
	overrides := tts.NewStressFromOverrides(ovs)
	slog.Default().Info("loaded config pronunciation overrides", "entries", len(overrides.Entries))
	return overrides
}

// setupStressSource initializes either the auto-stress client (silero-stress
// model on TTS server) or loads the global stress.json vocabulary.
func setupStressSource(proj *project.Project, autoStress bool) (*tts.StressClient, *tts.Stress, error) {
	if autoStress {
		if proj.Cfg.TTS.ServerURL == "" {
			return nil, nil, fmt.Errorf("--auto-stress requires tts.server_url to be set in config")
		}
		stressClient := tts.NewStressClient(proj.Cfg.TTS.ServerURL)
		slog.Default().
			Info("using auto-stress (silero-stress model on TTS server)", "server_url", proj.Cfg.TTS.ServerURL)
		return stressClient, nil, nil
	}
	stress, err := tts.LoadStress(proj.AIDir())
	if err != nil {
		return nil, nil, fmt.Errorf("load stress vocabulary: %w", err)
	}
	slog.Default().Info("loaded stress vocabulary", "entries", len(stress.Entries))
	return nil, stress, nil
}

// processSSMLChapter processes a single chapter: reads its translation,
// applies stress marks, generates SSML, and saves the result. Returns the
// generated (1 or 0) and skipped (1 or 0) counts.
func processSSMLChapter(
	ctx context.Context,
	ch chapters.Chapter,
	proj *project.Project,
	targetLang string,
	ids map[int]bool,
	writeAll, force, autoStress, transliterate bool,
	overrides *tts.Stress,
	stressClient *tts.StressClient,
	stress *tts.Stress,
) (int, int, error) {
	if !writeAll && !ids[ch.ID] {
		return 0, 0, nil
	}

	translationPath := translationPath(proj.TranslationDir(), ch.ID, targetLang)
	if !project.Exists(translationPath) {
		slog.Default().DebugContext(ctx, "skip chapter without translation", "chapter", ch.ID)
		return 0, 1, nil
	}

	ssmlPath := ssmlFilePath(proj.TTSDir(), ch.ID)
	if project.Exists(ssmlPath) && !force {
		slog.Default().DebugContext(ctx, "skip existing SSML", "chapter", ch.ID)
		return 0, 1, nil
	}

	text, err := os.ReadFile(translationPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read translation for chapter %d: %w", ch.ID, err)
	}

	// Transliterate Latin words to Cyrillic if the user chose to proceed
	// after the Latin word pre-scan.
	textStr := string(text)
	if transliterate && tts.HasLatinLetters(textStr) {
		textStr = tts.TransliterateLatin(textStr)
		slog.Default().WarnContext(ctx, "transliterated Latin words in translation", "chapter", ch.ID)
	}

	processed, err := applyStressToText(ctx, textStr, ch.ID, autoStress, overrides, stressClient, stress)
	if err != nil {
		return 0, 0, err
	}

	// Generate SSML (wrap in <speak>/<p>/<s> tags).
	ssmlText := tts.GenerateSSML(processed)

	if err = project.SaveBytes(ssmlPath, []byte(ssmlText)); err != nil {
		return 0, 0, fmt.Errorf("write SSML for chapter %d: %w", ch.ID, err)
	}

	// Update chapter status.
	ch.Status = "ssml"
	err = project.SaveJSON(
		filepath.Join(proj.ChaptersDir(), fmt.Sprintf("chapter_%03d.json", ch.ID)),
		ch,
	)
	if err != nil {
		slog.Default().WarnContext(ctx, "failed to update chapter status", "chapter", ch.ID, "error", err)
	}

	slog.Default().InfoContext(ctx, "SSML generated", "chapter", ch.ID, "auto_stress", autoStress)
	return 1, 0, nil
}

// applyStressToText applies stress marks to the chapter text using either the
// auto-stress model or the global stress vocabulary, then applies config
// overrides on top.
func applyStressToText(
	ctx context.Context,
	text string,
	chID int,
	autoStress bool,
	overrides *tts.Stress,
	stressClient *tts.StressClient,
	stress *tts.Stress,
) (string, error) {
	if autoStress {
		// Send text to the silero-stress model on the TTS server.
		stressed, err := stressClient.StressText(ctx, text)
		if err != nil {
			return "", fmt.Errorf("auto-stress chapter %d: %w", chID, err)
		}
		// Apply config overrides on top of the model output.
		if overrides != nil {
			return overrides.Apply(stressed), nil
		}
		return stressed, nil
	}
	// Apply stress marks from stress.json (term → stressed form).
	processed := stress.Apply(text)
	// Apply config overrides on top.
	if overrides != nil {
		processed = overrides.Apply(processed)
	}
	return processed, nil
}

// ssmlFilePath returns the path for a chapter's SSML file.
func ssmlFilePath(ttsDir string, chapterID int) string {
	return filepath.Join(ttsDir, fmt.Sprintf("chapter_%03d.ssml", chapterID))
}
