package translation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Memory manages per-chapter summary files (memory/chapter_NNN.summary.txt).
// After each chapter is translated, a short summary is generated and stored;
// the next chapter's translation prompt includes the previous 1-2 summaries
// to maintain continuity of characters, references, and plot.

// LoadSummary reads the summary for a chapter. Returns "" if no summary file
// exists (e.g. for chapter 1 or chapters not yet translated).
func LoadSummary(memoryDir string, chapterID int) (string, error) {
	path := summaryPath(memoryDir, chapterID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read summary for chapter %d: %w", chapterID, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveSummary writes a chapter's summary to memory/chapter_NNN.summary.txt.
func SaveSummary(memoryDir string, chapterID int, summary string) error {
	path := summaryPath(memoryDir, chapterID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(summary)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write summary for chapter %d: %w", chapterID, err)
	}
	return nil
}

// PreviousSummaries returns the summaries for the N chapters before
// chapterID, joined into a single context block for the translation prompt.
// Returns "" if there are no previous summaries.
func PreviousSummaries(memoryDir string, chapterID, count int) (string, error) {
	if chapterID <= 1 || count <= 0 {
		return "", nil
	}
	var parts []string
	start := max(chapterID-count, 1)
	for i := start; i < chapterID; i++ {
		s, err := LoadSummary(memoryDir, i)
		if err != nil {
			return "", err
		}
		if s != "" {
			parts = append(parts, fmt.Sprintf("[Chapter %d summary]\n%s", i, s))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "=== Previous chapter context ===\n\n" + strings.Join(parts, "\n\n"), nil
}

func summaryPath(memoryDir string, chapterID int) string {
	return filepath.Join(memoryDir, fmt.Sprintf("chapter_%03d.summary.txt", chapterID))
}
