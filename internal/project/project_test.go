package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCreatesProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	proj, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if proj.Root == "" {
		t.Error("Root is empty")
	}
	if proj.Cfg.Languages.Source != "en" {
		t.Errorf("default source = %q, want en", proj.Cfg.Languages.Source)
	}
}

func TestEnsureDirsCreatesAllStageDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	proj, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = proj.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	want := []string{
		proj.SourceDir(),
		proj.ExtractedDir(),
		filepath.Join(proj.ExtractedDir(), "raw"),
		filepath.Join(proj.ExtractedDir(), "blocks"),
		proj.ChaptersDir(),
		proj.AIDir(),
		proj.TranslationDir(),
		proj.MemoryDir(),
		proj.TTSDir(),
		proj.AudioDir(),
	}
	var info os.FileInfo
	for _, d := range want {
		if info, err = os.Stat(d); err != nil {
			t.Errorf("dir %s not created: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("path %s is not a directory", d)
		}
	}
}

func TestEnsureDirsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	proj, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = proj.EnsureDirs(); err != nil {
		t.Fatalf("first EnsureDirs: %v", err)
	}
	if err = proj.EnsureDirs(); err != nil {
		t.Fatalf("second EnsureDirs: %v", err)
	}
}

func TestSaveAndLoadJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	in := payload{Name: "alpha", Count: 42}
	if err := SaveJSON(path, in); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}
	var out payload
	if err := LoadJSON(path, &out); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if out != in {
		t.Errorf("round-trip = %+v, want %+v", out, in)
	}
}

func TestExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if Exists(p) {
		t.Fatal("Exists reported true for missing file")
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !Exists(p) {
		t.Fatal("Exists reported false for existing file")
	}
}
