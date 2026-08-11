package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
	"ebook-reader/internal/tts"
)

// shortChapterThreshold is the minimum chapter size (title + source text
// length in bytes) below which a chapter is marked as suspiciously short
// with a (!) suffix in the chapters table.
const shortChapterThreshold = 1500

// largeDiffThreshold is the minimum raw-vs-current size difference (in bytes)
// that gets a (!) marker in the diff column, indicating significant stripping.
const largeDiffThreshold = 300

// newChaptersCmd implements `bookai chapters`: a per-chapter overview table
// showing source size, glossary terms, characters, stress marks, translation
// status, SSML, and audio for every chapter in the project.
func newChaptersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chapters",
		Short: "Show per-chapter pipeline status table",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runChapters(proj)
		},
	}
}

func runChapters(proj *project.Project) error {
	chs, err := loadAllChapters(proj)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		return fmt.Errorf("no chapters found — run `bookai analyze-chapters` first")
	}

	// Load glossary, characters, and stress for per-chapter counts.
	glossary, _ := translation.LoadGlossary(proj.AIDir())
	chars, _ := translation.LoadCharacters(proj.AIDir())
	stress, _ := tts.LoadStress(proj.AIDir())

	targetLang := proj.Cfg.Languages.Target
	if targetLang == "" {
		targetLang = "ru"
	}

	audioFormat := proj.Cfg.TTS.AudioFormat
	if audioFormat == "" {
		audioFormat = "mp3"
	}
	audioExt := audioExtension(audioFormat)

	// Print header.
	fmt.Printf("%-4s  %-8s  %-8s  %-7s  %-6s  %-6s  %-7s  %-9s  %-5s  %-5s  %s\n",
		"ID", "Raw", "Size", "Diff", "Terms", "Chars", "Stress", "Trans", "SSML", "Audio", "Title")
	fmt.Println(strings.Repeat("-", 95))

	shortCount := 0
	translatedCount := 0
	ssmlCount := 0
	audioCount := 0
	strippedCount := 0

	for _, ch := range chs {
		id := ch.ID
		size := len(ch.Title) + len(ch.Source)
		shortMark := ""
		if size < shortChapterThreshold {
			shortMark = " (!)"
			shortCount++
		}

		// Raw size (before strip). If RawSize is 0, no stripping was done.
		rawStr := "-"
		diffStr := "-"
		if ch.RawSize > 0 {
			rawStr = fmt.Sprintf("%d", ch.RawSize)
			diff := ch.RawSize - size
			diffMark := ""
			if diff > largeDiffThreshold {
				diffMark = "!"
				strippedCount++
			}
			if diff < 0 {
				diffMark = "" // size grew somehow, no marker
			}
			diffStr = fmt.Sprintf("%d%s", diff, diffMark)
		}

		// Count glossary terms for this chapter.
		terms := 0
		for _, t := range glossary.Terms {
			for _, c := range t.Chapters {
				if c == id {
					terms++
					break
				}
			}
		}

		// Count characters for this chapter.
		charCount := 0
		for _, c := range chars.Characters {
			for _, chID := range c.Chapters {
				if chID == id {
					charCount++
					break
				}
			}
		}

		// Count stress entries for this chapter.
		stressCount := stress.CountForChapter(id)

		// Check translation.
		transPath := translationPath(proj.TranslationDir(), id, targetLang)
		transLen := "-"
		if project.Exists(transPath) {
			if data, err := os.ReadFile(transPath); err == nil {
				transLen = fmt.Sprintf("%d", len(data))
				translatedCount++
			}
		}

		// Check SSML.
		ssmlMark := "-"
		ssmlPath := ssmlFilePath(proj.TTSDir(), id)
		if project.Exists(ssmlPath) {
			ssmlMark = "yes"
			ssmlCount++
		}

		// Check audio.
		audioMark := "-"
		audioPath := audioPath(proj.AudioDir(), id, audioExt)
		if project.Exists(audioPath) {
			audioMark = "yes"
			audioCount++
		}

		// Truncate title for display.
		title := truncate(ch.Title, 40)

		fmt.Printf("%-4d  %-8s  %-8s  %-7s  %-6d  %-6d  %-7d  %-9s  %-5s  %-5s  %s\n",
			id, rawStr, fmt.Sprintf("%d%s", size, shortMark), diffStr,
			terms, charCount, stressCount,
			transLen, ssmlMark, audioMark, title)
	}

	fmt.Println(strings.Repeat("-", 95))
	fmt.Printf("total: %d chapters, %d short(!), %d stripped(!), %d translated, %d ssml, %d audio\n",
		len(chs), shortCount, strippedCount, translatedCount, ssmlCount, audioCount)

	return nil
}
