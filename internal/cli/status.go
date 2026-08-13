package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// jsonExt is the file extension for JSON artifacts counted by runStatus.
const jsonExt = ".json"

// newStatusCmd implements `bookai status`: a quick tree of the project
// directory with per-stage counts so the user can see pipeline progress.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show project directory tree and per-stage artifact counts",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runStatus(proj)
		},
	}
}

// runStatus reports the project and configuration paths and the file counts for each pipeline stage.
func runStatus(proj statusProject) error {
	fmt.Fprintf(os.Stdout, "project: %s\n", proj.RootPath())
	fmt.Fprintf(os.Stdout, "config:  %s\n", proj.ConfigPath())

	type stage struct {
		name string
		dir  string
		ext  string // file extension to count, "" = count any file
	}
	stages := []stage{
		{"source (epub)", proj.SourceDir(), ".epub"},
		{"extracted", proj.ExtractedDir(), jsonExt},
		{"chapters", proj.ChaptersDir(), jsonExt},
		{"ai", proj.AIDir(), jsonExt},
		{"translation", proj.TranslationDir(), ".txt"},
		{"memory", proj.MemoryDir(), ".txt"},
		{"tts (ssml)", proj.TTSDir(), ".ssml"},
		{"audio", proj.AudioDir(), ".mp3"},
	}
	for _, s := range stages {
		count, exists := countFiles(s.dir, s.ext)
		mark := " "
		if exists {
			mark = "x"
		}
		fmt.Fprintf(os.Stdout, "[%s] %-16s %3d file(s)  %s\n", mark, s.name, count, s.dir)
	}
	return nil
}

// countFiles returns the number of visible files (recursively) under dir
// whose name ends with ext (if ext != ""). Hidden files (dot-prefixed, e.g.
// temp files from convertAudio) are skipped. The boolean reports whether
// the directory exists at all.
func countFiles(dir, ext string) (int, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0, false
	}
	n := 0
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Log the unreadable entry but continue traversing the rest
			// of the tree so the count is best-effort, not aborted.
			slog.Default().Warn("status: skipping unreadable entry", "path", path, "error", err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Skip temp/hidden files (e.g. .chapter_001.tmp.mp3 from convertAudio).
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") {
			return nil
		}
		if ext == "" || filepath.Ext(path) == ext {
			n++
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipDir) {
		slog.Default().Warn("status: directory walk failed", "dir", dir, "error", walkErr)
	}
	return n, true
}

// statusProject is a tiny interface so runStatus can be tested with a fake.
type statusProject interface {
	RootPath() string
	ConfigPath() string
	SourceDir() string
	ExtractedDir() string
	ChaptersDir() string
	AIDir() string
	TranslationDir() string
	MemoryDir() string
	TTSDir() string
	AudioDir() string
}
