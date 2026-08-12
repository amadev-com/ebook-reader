package cli //nolint:testpackage // needs access to unexported CLI internals

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
	"ebook-reader/internal/tts"
)

// buildTestEPUB creates a minimal valid EPUB in a temp file and returns its
// path. It mirrors the synthetic EPUB used in the epub package tests.
func buildTestEPUB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.epub")

	// We reuse the epub test builder by writing the same files directly via
	// a zip writer. To avoid a cross-package test dependency, we reconstruct
	// the minimal EPUB here.
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="book.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`,
		"book.opf": `<?xml version="1.0"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf" unique-identifier="ID">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Test Book</dc:title>
    <dc:language>en</dc:language>
    <dc:identifier id="ID">test-id</dc:identifier>
  </metadata>
  <manifest>
    <item id="cover" href="cover.html" media-type="application/xhtml+xml"/>
    <item id="ch1" href="chapter1.html" media-type="application/xhtml+xml"/>
    <item id="ch2" href="chapter2.html" media-type="application/xhtml+xml"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="cover" linear="yes"/>
    <itemref idref="ch1" linear="yes"/>
    <itemref idref="ch2" linear="yes"/>
  </spine>
</package>`,
		"toc.ncx": `<?xml version="1.0"?>
<ncx version="2005-1" xmlns="http://www.daisy.org/z3986/2005/ncx/">
  <navMap>
    <navPoint id="cover" playOrder="1">
      <navLabel><text>Cover</text></navLabel>
      <content src="cover.html"/>
    </navPoint>
    <navPoint id="ch1" playOrder="2">
      <navLabel><text>Chapter 1: Beginnings</text></navLabel>
      <content src="chapter1.html"/>
    </navPoint>
    <navPoint id="ch2" playOrder="3">
      <navLabel><text>Chapter 2: Endings</text></navLabel>
      <content src="chapter2.html"/>
    </navPoint>
  </navMap>
</ncx>`,
		"cover.html":    `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>Cover</h1><p>Cover text</p></body></html>`,
		"chapter1.html": `<html xmlns="http://www.w3.org/1999/xhtml"><body><h2 id="ch1">Chapter 1: Beginnings</h2><p>It was the best of times.</p></body></html>`,
		"chapter2.html": `<html xmlns="http://www.w3.org/1999/xhtml"><body><h2>Chapter 2: Endings</h2><p>The end.</p></body></html>`,
	}
	if err := writeZip(path, files); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	return path
}

// writeZip creates a zip file at path containing the given files. The
// mimetype entry is added first and stored uncompressed per the EPUB spec.
func writeZip(path string, files map[string]string) error {
	f, err := openOrCreate(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	mw, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		return err
	}
	if _, err = mw.Write([]byte("application/epub+zip")); err != nil {
		return err
	}
	var w io.Writer
	for name, content := range files {
		if w, err = zw.Create(name); err != nil {
			return err
		}
		if _, err = w.Write([]byte(content)); err != nil {
			return err
		}
	}
	return zw.Close()
}

func openOrCreate(path string) (*os.File, error) {
	return os.Create(path)
}

func TestRunImportAndAnalyzeChapters(t *testing.T) {
	t.Parallel()
	epubPath := buildTestEPUB(t)
	projDir := t.TempDir()
	proj, err := project.New(projDir)
	if err != nil {
		t.Fatalf("New project: %v", err)
	}
	if err = proj.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	// Import.
	if err = runImport(t.Context(), proj, epubPath, false); err != nil {
		t.Fatalf("runImport: %v", err)
	}
	// Verify expected artifacts.
	for _, p := range []string{
		filepath.Join(proj.ExtractedDir(), "metadata.json"),
		filepath.Join(proj.ExtractedDir(), "spine.json"),
		filepath.Join(proj.ExtractedDir(), "toc.json"),
		filepath.Join(proj.ExtractedDir(), "blocks", "item000.json"),
		filepath.Join(proj.ExtractedDir(), "blocks", "item001.json"),
		filepath.Join(proj.ExtractedDir(), "blocks", "item002.json"),
	} {
		if !project.Exists(p) {
			t.Errorf("expected artifact %s not found", p)
		}
	}

	// Analyze chapters.
	if err = runAnalyzeChapters(t.Context(), proj, true, 0, "", "", nil); err != nil {
		t.Fatalf("runAnalyzeChapters: %v", err)
	}
	// Expect 2 chapters (cover skipped).
	for _, p := range []string{
		filepath.Join(proj.ChaptersDir(), "chapter_001.json"),
		filepath.Join(proj.ChaptersDir(), "chapter_002.json"),
		filepath.Join(proj.ChaptersDir(), "_index.json"),
		filepath.Join(proj.ChaptersDir(), "_skipped.json"),
	} {
		if !project.Exists(p) {
			t.Errorf("expected chapter artifact %s not found", p)
		}
	}
	// Cover should NOT have a chapter file.
	if project.Exists(filepath.Join(proj.ChaptersDir(), "chapter_000.json")) {
		t.Error("chapter_000.json should not exist (cover is skipped)")
	}

	// Verify index.
	var idx struct {
		ChapterCount int    `json:"chapter_count"`
		Strategy     string `json:"strategy"`
	}
	if err = project.LoadJSON(filepath.Join(proj.ChaptersDir(), "_index.json"), &idx); err != nil {
		t.Fatalf("load index: %v", err)
	}
	if idx.ChapterCount != 2 {
		t.Errorf("index chapter_count = %d, want 2", idx.ChapterCount)
	}
	if idx.Strategy != "toc" {
		t.Errorf("index strategy = %q, want toc", idx.Strategy)
	}
}

func TestRunImportIdempotentWithoutForce(t *testing.T) {
	t.Parallel()
	epubPath := buildTestEPUB(t)
	projDir := t.TempDir()
	proj, err := project.New(projDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = proj.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err = runImport(t.Context(), proj, epubPath, false); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Second import without --force must fail.
	if err = runImport(t.Context(), proj, epubPath, false); err == nil {
		t.Fatal("second import without --force succeeded, want error")
	}
	// Second import with --force must succeed.
	if err = runImport(t.Context(), proj, epubPath, true); err != nil {
		t.Fatalf("second import with --force: %v", err)
	}
}

func TestRunAnalyzeChaptersRequiresImport(t *testing.T) {
	t.Parallel()
	projDir := t.TempDir()
	proj, err := project.New(projDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = proj.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// No import done yet -> analyze-chapters should fail with a clear message.
	err = runAnalyzeChapters(t.Context(), proj, true, 0, "", "", nil)
	if err == nil {
		t.Fatal("analyze-chapters without import succeeded, want error")
	}
}

func TestParseChapterFilter(t *testing.T) {
	t.Parallel()
	// No filter -> errNoChapterFilter (write all).
	got, err := parseChapterFilter(0, "", 10)
	if !errors.Is(err, errNoChapterFilter) || got != nil {
		t.Errorf("no filter: got %v, %v; want nil, errNoChapterFilter", got, err)
	}
	// Single chapter.
	if got, err = parseChapterFilter(3, "", 10); err != nil {
		t.Fatalf("chapter 3: %v", err)
	}
	if len(got) != 1 || !got[3] {
		t.Errorf("chapter 3: got %v, want {3:true}", got)
	}
	// Range.
	if got, err = parseChapterFilter(0, "2-5", 10); err != nil {
		t.Fatalf("range 2-5: %v", err)
	}
	for i := 2; i <= 5; i++ {
		if !got[i] {
			t.Errorf("range 2-5: id %d not set", i)
		}
	}
	if got[1] || got[6] {
		t.Errorf("range 2-5: ids outside range set: %v", got)
	}
	// Out of range.
	if _, err = parseChapterFilter(11, "", 10); err == nil {
		t.Error("chapter 11 with max 10 should error")
	}
	if _, err = parseChapterFilter(0, "1-11", 10); err == nil {
		t.Error("range 1-11 with max 10 should error")
	}
	// Bad range format.
	if _, err = parseChapterFilter(0, "abc", 10); err == nil {
		t.Error("range 'abc' should error")
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"My Vampire System", "my-vampire-system"},
		{"9kafe.com-my-vampire-system-c1-700", "9kafe-com-my-vampire-system-c1-700"},
		{"Book_Title_With_Underscores", "book-title-with-underscores"},
		{"  Multiple   Spaces  ", "multiple-spaces"},
		{"Already-dashed", "already-dashed"},
		{"UPPERCASE", "uppercase"},
		{"Special!@#Characters", "special-characters"},
		{"---leading-trailing---", "leading-trailing"},
		{"", ""},
	}
	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestImportCreatesProjectDir(t *testing.T) { //nolint:paralleltest // mutates global flagProject and os.Chdir
	// Uses global flagProject and os.Chdir — not safe to parallelize.
	epubPath := buildTestEPUB(t)
	parentDir := t.TempDir()

	// Change to parent dir so the project is created there.
	t.Chdir(parentDir)

	// Reset the global flagProject to default for this test.
	flagProject = "."
	defer func() { flagProject = "." }()

	// Build the import command and run it with a custom book name.
	cmd := newImportCmd()
	cmd.SetArgs([]string{epubPath, "My Test Book"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}

	// The project dir should be "my-test-book" under parentDir.
	projDir := filepath.Join(parentDir, "my-test-book")
	if !project.Exists(filepath.Join(projDir, "config.yaml")) {
		t.Errorf("config.yaml not created in %s", projDir)
	}
	if !project.Exists(filepath.Join(projDir, "source", "original.epub")) {
		t.Errorf("EPUB not copied to %s", projDir)
	}
}

func TestImportWithExplicitProjectFlag(t *testing.T) { //nolint:paralleltest // mutates global flagProject
	// Uses global flagProject — not safe to parallelize.
	epubPath := buildTestEPUB(t)
	projDir := t.TempDir()

	flagProject = "."
	defer func() { flagProject = "." }()

	// Use the root command so persistent flags are available.
	root := NewRoot()
	root.SetArgs([]string{"import", epubPath, "--project", projDir})
	root.SilenceUsage = true
	root.SilenceErrors = true
	if err := root.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Should use the explicit project dir, not auto-create one.
	if !project.Exists(filepath.Join(projDir, "source", "original.epub")) {
		t.Errorf("EPUB not in explicit project dir %s", projDir)
	}
}

func TestDeduplicateTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "duplicate title",
			input: "Глава 384: Глупый поступок!\n\nГлава 384: Глупый поступок!\n\nБыл полдень.",
			want:  "Глава 384: Глупый поступок!\n\nБыл полдень.",
		},
		{
			name:  "no duplicate",
			input: "Глава 385: Скованные\n\nТело текста здесь.",
			want:  "Глава 385: Скованные\n\nТело текста здесь.",
		},
		{
			name:  "single line",
			input: "Только одна строка.",
			want:  "Только одна строка.",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "duplicate with extra blank lines",
			input: "Title\n\n\n\nTitle\n\nBody",
			want:  "Title\n\nBody",
		},
		{
			name:  "different first and third lines",
			input: "Title one\n\nTitle two\n\nBody",
			want:  "Title one\n\nTitle two\n\nBody",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := deduplicateTitle(tc.input)
			if got != tc.want {
				t.Errorf("deduplicateTitle:\ninput: %q\ngot:   %q\nwant:  %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestDeduplicateConflicts(t *testing.T) {
	t.Parallel()
	conflicts := []tts.StressConflict{
		{Term: "вендиго", Variants: []string{"Венд+иго", "в+ендиго"}},
		{Term: "Куинна", Variants: []string{"К+уинна", "Ку+инна"}},
		{Term: "вендиго", Variants: []string{"Венд+иго", "в+ендиго"}},
		{Term: "вендиго", Variants: []string{"Венд+иго", "в+ендиго"}},
		{Term: "Куинна", Variants: []string{"К+уинна", "Ку+инна"}},
		{Term: "Логан", Variants: []string{"Л+оган", "Лог+ан"}},
	}

	deduped := deduplicateConflicts(conflicts)
	if len(deduped) != 3 {
		t.Fatalf("deduped count = %d, want 3", len(deduped))
	}

	// Each term should appear exactly once with all unique variants.
	for _, c := range deduped {
		switch c.Term {
		case "вендиго":
			if len(c.Variants) != 2 {
				t.Errorf("вендиго variants = %d, want 2", len(c.Variants))
			}
		case "Куинна":
			if len(c.Variants) != 2 {
				t.Errorf("Куинна variants = %d, want 2", len(c.Variants))
			}
		case "Логан":
			if len(c.Variants) != 2 {
				t.Errorf("Логан variants = %d, want 2", len(c.Variants))
			}
		default:
			t.Errorf("unexpected term: %s", c.Term)
		}
	}
}

func TestDeduplicateConflicts_MergesNewVariants(t *testing.T) {
	t.Parallel()
	conflicts := []tts.StressConflict{
		{Term: "Эрин", Variants: []string{"Э+рин"}},
		{Term: "Эрин", Variants: []string{"+Эрин"}},
		{Term: "Эрин", Variants: []string{"Э+рин", "+Эрин", "Эр+ин"}},
	}

	deduped := deduplicateConflicts(conflicts)
	if len(deduped) != 1 {
		t.Fatalf("deduped count = %d, want 1", len(deduped))
	}
	if len(deduped[0].Variants) != 3 {
		t.Errorf("variants = %v, want 3 unique", deduped[0].Variants)
	}
}

func TestDeduplicateConflicts_CaseInsensitive(t *testing.T) {
	t.Parallel()
	conflicts := []tts.StressConflict{
		{Term: "Вендиго", Variants: []string{"Венд+иго"}},
		{Term: "вендиго", Variants: []string{"в+ендиго"}},
	}

	deduped := deduplicateConflicts(conflicts)
	if len(deduped) != 1 {
		t.Fatalf("deduped count = %d, want 1 (case-insensitive)", len(deduped))
	}
	if len(deduped[0].Variants) != 2 {
		t.Errorf("variants = %d, want 2", len(deduped[0].Variants))
	}
}

func TestStripTrailer_SceneBreakNotMistakenForTrailer(t *testing.T) {
	t.Parallel()
	// Chapter with a scene-break "***" in the middle and a trailer "******"
	// at the end. The strip should only cut the trailer, not the scene break.
	body := strings.Repeat("A", 3000) // chapter body
	text := body + "\n\n*****\n\n" + strings.Repeat("B", 500) + "\n\n******\n\nFor MVS artwork and updates"

	result := stripTrailer(text, "***")
	// Should keep the body + scene break + B content, only removing the trailer.
	if len(result) <= 3000 {
		t.Errorf("result too short: %d, scene break was mistaken for trailer", len(result))
	}
	if strings.Contains(result, "For MVS artwork") {
		t.Errorf("trailer text should have been removed")
	}
	if !strings.Contains(result, "*****") {
		t.Errorf("scene break should have been preserved")
	}
}

func TestStripTrailer_NoSceneBreak_CutsAtTrigger(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("A", 3000)
	text := body + "\n\n******\n\nFor MVS artwork and updates"

	result := stripTrailer(text, "***")
	if strings.Contains(result, "For MVS artwork") {
		t.Errorf("trailer should have been removed")
	}
	if len(result) < 3000 {
		t.Errorf("body should be preserved, got len=%d", len(result))
	}
}

func TestStripTrailer_TriggerNotInTail(t *testing.T) {
	t.Parallel()
	// Trigger not in last 400 chars — no stripping.
	text := strings.Repeat("A", 5000) + "\n\n******\n\n" + strings.Repeat("B", 500)
	result := stripTrailer(text, "PROMO")
	if result != text {
		t.Errorf("text should be unchanged when trigger not in tail")
	}
}

func TestSaveBatchState_UnwritableDir(t *testing.T) {
	t.Parallel()
	// Verify that SaveBatchState returns an error when the ai directory
	// is unwritable. pollBatchUntilTerminal now checks this error and
	// propagates it instead of discarding it.
	dir := t.TempDir()
	aiDir := filepath.Join(dir, "ai")
	if err := os.Mkdir(aiDir, 0o500); err != nil { // read+execute, no write
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(aiDir, 0o700) })

	state := &translation.BatchState{
		Type:    translation.BatchTypeAnalyze,
		BatchID: "batch_test",
		Status:  "in_progress",
	}
	err := translation.SaveBatchState(aiDir, state)
	if err == nil {
		t.Fatal("expected error from SaveBatchState with unwritable dir, got nil")
	}
}
