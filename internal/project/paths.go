// Package project owns the on-disk layout of a bookai project: it knows where
// each artifact lives, ensures directories exist, and provides typed load/save
// helpers for the canonical JSON files. The filesystem IS the database.
package project

import (
	"path/filepath"

	"ebook-reader/internal/config"
)

// Project is a handle to a bookai project on disk. All stage code receives a
// *Project and asks it for absolute paths via the Path* methods.
type Project struct {
	// Root is the absolute path to the project directory.
	Root string
	// Cfg is the loaded (or default) configuration.
	Cfg config.Config
}

// New constructs a Project rooted at root, loading config.yaml if present.
func New(root string) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, err
	}
	return &Project{Root: abs, Cfg: cfg}, nil
}

// path joins the project root with a configured subdirectory name, falling
// back to def when the configured name is empty.
func (p *Project) path(cfgField, def string) string {
	name := cfgField
	if name == "" {
		name = def
	}
	return filepath.Join(p.Root, name)
}

// RootPath returns the absolute project root. Named to avoid clashing with
// the exported Root field; satisfies the cli.statusProject interface.
func (p *Project) RootPath() string { return p.Root }

// SourceDir is where the original EPUB is copied.
func (p *Project) SourceDir() string { return p.path(p.Cfg.Paths.Source, "source") }

// ExtractedDir holds spine.json, toc.json, raw/, blocks/.
func (p *Project) ExtractedDir() string { return p.path(p.Cfg.Paths.Extracted, "extracted") }

// ChaptersDir holds chapter_NNN.json, _skipped.json, _index.json.
func (p *Project) ChaptersDir() string { return p.path(p.Cfg.Paths.Chapters, "chapters") }

// AIDir holds glossary.json, characters.json, pronunciation.json (M2+).
func (p *Project) AIDir() string { return p.path(p.Cfg.Paths.AI, "ai") }

// TranslationDir holds chapter_NNN.<target>.txt (M2+).
func (p *Project) TranslationDir() string { return p.path(p.Cfg.Paths.Translation, "translation") }

// MemoryDir holds per-chapter summaries (M2+).
func (p *Project) MemoryDir() string { return p.path(p.Cfg.Paths.Memory, "memory") }

// TTSDir holds chapter_NNN.txt (preprocessed TTS text, M3+).
func (p *Project) TTSDir() string { return p.path(p.Cfg.Paths.TTS, "tts") }

// AudioDir holds chapter_NNN.wav (M3+).
func (p *Project) AudioDir() string { return p.path(p.Cfg.Paths.Audio, "audio") }

// SourceEpub returns the path to the canonical source EPUB inside the project.
func (p *Project) SourceEpub() string { return filepath.Join(p.SourceDir(), "original.epub") }

// ConfigPath returns the path to config.yaml.
func (p *Project) ConfigPath() string { return filepath.Join(p.Root, "config.yaml") }
