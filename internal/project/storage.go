package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDirs creates every stage directory that the pipeline may write to.
// Existing directories are left untouched. It is safe to call repeatedly.
func (p *Project) EnsureDirs() error {
	dirs := []string{
		p.Root,
		p.SourceDir(),
		p.ExtractedDir(),
		filepath.Join(p.ExtractedDir(), "raw"),
		filepath.Join(p.ExtractedDir(), "blocks"),
		p.ChaptersDir(),
		p.AIDir(),
		p.TranslationDir(),
		p.MemoryDir(),
		p.TTSDir(),
		p.AudioDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}

// Exists reports whether path exists on disk.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SaveJSON writes v as pretty-printed JSON to path. Pretty printing (2-space
// indent + sorted keys) keeps diffs stable so reruns are detectable in git.
func SaveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadJSON reads and unmarshals path into v.
func LoadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// SaveBytes writes raw bytes to path, creating parent directories as needed.
func SaveBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
