package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

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

func runStatus(proj statusProject) error {
	fmt.Printf("project: %s\n", proj.RootPath())
	fmt.Printf("config:  %s\n", proj.ConfigPath())

	type stage struct {
		name string
		dir  string
		ext  string // file extension to count, "" = count any file
	}
	stages := []stage{
		{"source (epub)", proj.SourceDir(), ".epub"},
		{"extracted", proj.ExtractedDir(), ".json"},
		{"chapters", proj.ChaptersDir(), ".json"},
		{"ai", proj.AIDir(), ".json"},
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
		fmt.Printf("[%s] %-16s %3d file(s)  %s\n", mark, s.name, count, s.dir)
	}
	return nil
}

// countFiles returns the number of files (recursively) under dir whose name
// ends with ext (if ext != ""). The boolean reports whether the directory
// exists at all.
func countFiles(dir, ext string) (int, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0, false
	}
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ext == "" || filepath.Ext(path) == ext {
			n++
		}
		return nil
	})
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
