package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Parallel()
	cfg := Default("my-book")
	if cfg.Project != "my-book" {
		t.Errorf("Project = %q, want %q", cfg.Project, "my-book")
	}
	if cfg.Languages.Source != "en" || cfg.Languages.Target != "ru" {
		t.Errorf("Languages = %+v, want en->ru", cfg.Languages)
	}
	if cfg.OpenAI.TranslationModel != "gpt-5.6-luna" {
		t.Errorf("TranslationModel = %q, want gpt-5.6-luna", cfg.OpenAI.TranslationModel)
	}
	if cfg.OpenAI.HelperModel != "gpt-5.6-luna" {
		t.Errorf("HelperModel = %q, want gpt-5.6-luna", cfg.OpenAI.HelperModel)
	}
}

func TestDefaultEmptyName(t *testing.T) {
	t.Parallel()
	cfg := Default("")
	if cfg.Project != "book" {
		t.Errorf("Project = %q, want %q", cfg.Project, "book")
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	// Default project name is the base name of the temp dir.
	want := filepath.Base(dir)
	if cfg.Project != want {
		t.Errorf("Project = %q, want %q", cfg.Project, want)
	}
}

func TestLoadAndSaveRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := Default("round-trip")
	cfg.OpenAI.TranslationModel = "custom-model"
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// File must exist.
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("config.yaml not written: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.OpenAI.TranslationModel != "custom-model" {
		t.Errorf("TranslationModel = %q, want custom-model", loaded.OpenAI.TranslationModel)
	}
	if loaded.Project != "round-trip" {
		t.Errorf("Project = %q, want round-trip", loaded.Project)
	}
}

func TestLoadInvalidRejectsEmptySource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	invalid := []byte("languages:\n  source: \"\"\n  target: ru\n")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), invalid, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted empty source language, want error")
	}
}
