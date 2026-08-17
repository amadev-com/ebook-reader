package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"ebook-reader/internal/chapters"
	"ebook-reader/internal/project"
)

// sileroContainerName is the Docker container name for the Silero TTS server,
// as defined in tts-server/docker-compose.yml.
const sileroContainerName = "bookai-silero"

// snippetMaxRunes is the maximum number of runes to extract from a log warning
// for chapter matching. 40 runes is enough to uniquely identify a chapter.
const snippetMaxRunes = 40

// ttsLogWarning represents a warning line parsed from the Silero Docker logs.
type ttsLogWarning struct {
	line    string
	snippet string // extracted text snippet for chapter matching
}

// verifyDockerLogs queries the Silero Docker container logs for warnings
// generated since the given timestamp, matches them to chapters, and reports
// any problematic chapters. This is a best-effort check — if Docker is not
// available or the container doesn't exist, it logs a debug message and
// returns nil.
func verifyDockerLogs(
	ctx context.Context,
	proj *project.Project,
	chs []chapters.Chapter,
	since time.Time,
	synthesized int,
) {
	// Only verify if we actually synthesized something.
	if synthesized == 0 {
		return
	}

	warnings, err := querySileroLogs(ctx, since)
	if err != nil {
		slog.Default().DebugContext(ctx, "skipped Docker log verification", "error", err)
		return
	}
	if len(warnings) == 0 {
		return
	}

	// Match warnings to chapters by searching SSML files for the text snippets.
	matched := matchWarningsToChapters(warnings, chs, proj)

	if len(matched) == 0 {
		slog.Default().WarnContext(ctx, "Silero Docker logs contain warnings but could not match them to chapters",
			"warning_count", len(warnings))
		return
	}

	// Report.
	slog.Default().WarnContext(ctx, "Silero TTS server reported warnings during synthesis",
		"warning_count", len(warnings), "chapter_count", len(matched))
	for chID, chWarnings := range matched {
		slog.Default().WarnContext(ctx, "Silero TTS warnings for chapter",
			"chapter", chID, "warning_count", len(chWarnings))
		for _, w := range chWarnings {
			slog.Default().WarnContext(ctx, "Silero TTS warning", "chapter", chID, "message", w.line)
		}
	}
	slog.Default().WarnContext(ctx, "chapters with Silero TTS warnings may have audio issues (silence or crashes); "+
		"check the SSML files and re-run `bookai ssml --force --chapter N` if needed")
}

// querySileroLogs queries Docker logs for the Silero container since the given
// timestamp and returns parsed warning lines.
func querySileroLogs(ctx context.Context, since time.Time) ([]ttsLogWarning, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	cmd := exec.CommandContext(ctx, "docker", "logs", "--since", sinceStr, sileroContainerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker logs: %w", err)
	}
	return parseSileroWarnings(string(output)), nil
}

// parseSileroWarnings extracts warning lines from Docker log output. It looks
// for lines containing [WARN] or [ERROR] and extracts text snippets from SSML
// parsing failure messages for chapter matching. Handles multi-line log
// entries (SSML text may contain newlines).
func parseSileroWarnings(logOutput string) []ttsLogWarning {
	cleanOutput := stripANSI(logOutput)
	var warnings []ttsLogWarning

	// Split into log entries by looking for [WARN], [ERROR], [INFO], or INFO:
	// markers at the start of lines.
	lines := strings.Split(cleanOutput, "\n")
	var currentEntry strings.Builder
	var currentIsWarning bool

	flush := func() {
		if currentIsWarning && currentEntry.Len() > 0 {
			entry := strings.TrimSpace(currentEntry.String())
			warning := ttsLogWarning{line: entry, snippet: extractSnippet(entry)}
			warnings = append(warnings, warning)
		}
		currentEntry.Reset()
		currentIsWarning = false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isMarker := strings.HasPrefix(trimmed, "[WARN]") ||
			strings.HasPrefix(trimmed, "[ERROR]") ||
			strings.HasPrefix(trimmed, "[INFO]") ||
			strings.HasPrefix(trimmed, "INFO:") ||
			strings.HasPrefix(trimmed, "ERROR:")

		if isMarker && currentEntry.Len() > 0 {
			flush()
		}

		if strings.HasPrefix(trimmed, "[WARN]") || strings.HasPrefix(trimmed, "[ERROR]") {
			currentIsWarning = true
		}

		if currentIsWarning || (currentEntry.Len() > 0 && !isMarker) {
			if currentEntry.Len() > 0 {
				currentEntry.WriteString("\n")
			}
			currentEntry.WriteString(line)
		}
	}
	flush()

	return warnings
}

// extractSnippet pulls a text snippet from an SSML parsing failure log line.
// The log format is: SSML parsing failed for text '<speak>...text...', returning ...
// We extract the text between the first <s> tag and the truncation marker.
func extractSnippet(line string) string {
	// Find the text after "for text '".
	idx := strings.Index(line, "for text '")
	if idx < 0 {
		return ""
	}
	start := idx + len("for text '")
	// Find the closing quote (text is truncated with ...').
	end := strings.Index(line[start:], "'")
	if end < 0 {
		return ""
	}
	text := line[start : start+end]
	// Strip SSML tags to get plain text for matching.
	text = stripSSMLTagsFromSnippet(text)
	// Take a distinctive snippet (first snippetMaxRunes runes of text content).
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > snippetMaxRunes {
		text = string(runes[:snippetMaxRunes])
	}
	return text
}

// stripSSMLTagsFromSnippet removes XML tags from a log snippet for matching.
func stripSSMLTagsFromSnippet(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// matchWarningsToChapters matches warning snippets to chapter SSML files by
// searching for the snippet text in each chapter's SSML content.
func matchWarningsToChapters(
	warnings []ttsLogWarning,
	chs []chapters.Chapter,
	proj *project.Project,
) map[int][]ttsLogWarning {
	matched := make(map[int][]ttsLogWarning)
	for _, w := range warnings {
		if w.snippet == "" {
			continue
		}
		for _, ch := range chs {
			ssmlPath := ssmlFilePath(proj.TTSDir(), ch.ID)
			if !project.Exists(ssmlPath) {
				continue
			}
			data, err := os.ReadFile(ssmlPath)
			if err != nil {
				continue
			}
			// Search for the snippet in the SSML content (case-insensitive).
			if containsCI(string(data), w.snippet) {
				matched[ch.ID] = append(matched[ch.ID], w)
				break
			}
		}
	}
	return matched
}

// ansiEscapeLen is the length of the ANSI escape sequence prefix "\x1b[".
const ansiEscapeLen = 2

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until 'm' (color codes end with 'm').
			j := i + ansiEscapeLen
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
