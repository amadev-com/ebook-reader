package cli

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/chapters"
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

// audioFormatMP3 is the default audio format when none is configured.
const audioFormatMP3 = "mp3"

// tableWidth is the number of '-' characters used as the separator line in
// the chapters table.
const tableWidth = 95

// titleColWidth is the maximum number of characters displayed for a chapter
// title in the chapters table.
const titleColWidth = 40

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
		audioFormat = audioFormatMP3
	}
	audioExt := audioExtension(audioFormat)

	// Print header.
	printChaptersTableHeader()

	stats := &chaptersTableStats{}
	for _, ch := range chs {
		printChapterRow(ch, glossary, chars, stress, proj, targetLang, audioExt, stats)
	}

	printChaptersTableSummary(len(chs), stats)
	return nil
}

// chaptersTableStats accumulates per-chapter counts for the summary line.
type chaptersTableStats struct {
	shortCount      int
	translatedCount int
	ssmlCount       int
	audioCount      int
	strippedCount   int
}

// printChaptersTableHeader prints the column header and separator line.
func printChaptersTableHeader() {
	fmt.Fprintf(os.Stdout, "%-4s  %-8s  %-8s  %-7s  %-6s  %-6s  %-7s  %-9s  %-5s  %-5s  %s\n",
		"ID", "Raw", "Size", "Diff", "Terms", "Chars", "Stress", "Trans", "SSML", "Audio", "Title")
	fmt.Fprintln(os.Stdout, strings.Repeat("-", tableWidth))
}

// printChaptersTableSummary prints the separator line and summary counts.
func printChaptersTableSummary(total int, stats *chaptersTableStats) {
	fmt.Fprintln(os.Stdout, strings.Repeat("-", tableWidth))
	fmt.Fprintf(os.Stdout, "total: %d chapters, %d short(!), %d stripped(!), %d translated, %d ssml, %d audio\n",
		total, stats.shortCount, stats.strippedCount, stats.translatedCount, stats.ssmlCount, stats.audioCount)
}

// printChapterRow renders a single chapter row and updates the stats counters.
func printChapterRow(
	ch chapters.Chapter,
	glossary *translation.Glossary,
	chars *translation.Characters,
	stress *tts.Stress,
	proj *project.Project,
	targetLang, audioExt string,
	stats *chaptersTableStats,
) {
	id := ch.ID
	size := len(ch.Title) + len(ch.Source)
	shortMark := ""
	if size < shortChapterThreshold {
		shortMark = " (!)"
		stats.shortCount++
	}

	// Raw size (before strip). If RawSize is 0, no stripping was done.
	rawStr, diffStr := chapterSizeColumns(ch, size, stats)

	terms := countGlossaryTerms(glossary, id)
	charCount := countCharacters(chars, id)
	stressCount := stress.CountForChapter(id)

	transLen := chapterTransLen(proj, id, targetLang, stats)
	ssmlMark := chapterFileMark(proj, ssmlFilePath(proj.TTSDir(), id), &stats.ssmlCount)
	audioMark := chapterFileMark(proj, audioPath(proj.AudioDir(), id, audioExt), &stats.audioCount)

	title := truncate(ch.Title, titleColWidth)

	fmt.Fprintf(os.Stdout, "%-4d  %-8s  %-8s  %-7s  %-6d  %-6d  %-7d  %-9s  %-5s  %-5s  %s\n",
		id, rawStr, fmt.Sprintf("%d%s", size, shortMark), diffStr,
		terms, charCount, stressCount,
		transLen, ssmlMark, audioMark, title)
}

// chapterSizeColumns returns the raw and diff column strings for a chapter.
func chapterSizeColumns(ch chapters.Chapter, size int, stats *chaptersTableStats) (string, string) {
	rawStr := "-"
	diffStr := "-"
	if ch.RawSize > 0 {
		rawStr = strconv.Itoa(ch.RawSize)
		diff := ch.RawSize - size
		diffMark := ""
		if diff > largeDiffThreshold {
			diffMark = "!"
			stats.strippedCount++
		}
		if diff < 0 {
			diffMark = "" // size grew somehow, no marker
		}
		diffStr = fmt.Sprintf("%d%s", diff, diffMark)
	}
	return rawStr, diffStr
}

// countGlossaryTerms returns the number of glossary terms tagged with the
// given chapter ID.
func countGlossaryTerms(glossary *translation.Glossary, id int) int {
	terms := 0
	for _, t := range glossary.Terms {
		if slices.Contains(t.Chapters, id) {
			terms++
		}
	}
	return terms
}

// countCharacters returns the number of characters tagged with the given
// chapter ID.
func countCharacters(chars *translation.Characters, id int) int {
	charCount := 0
	for _, c := range chars.Characters {
		if slices.Contains(c.Chapters, id) {
			charCount++
		}
	}
	return charCount
}

// chapterTransLen returns the translation file length string ("-" if missing)
// and increments the translated counter if the file exists and is readable.
func chapterTransLen(proj *project.Project, id int, targetLang string, stats *chaptersTableStats) string {
	transPath := translationPath(proj.TranslationDir(), id, targetLang)
	if !project.Exists(transPath) {
		return "-"
	}
	data, err := os.ReadFile(transPath)
	if err != nil {
		return "-"
	}
	stats.translatedCount++
	return strconv.Itoa(len(data))
}

// chapterFileMark returns "yes" if the file exists (incrementing the counter
// via the pointer) or "-" otherwise.
func chapterFileMark(_ *project.Project, path string, count *int) string {
	if project.Exists(path) {
		*count++
		return "yes"
	}
	return "-"
}
