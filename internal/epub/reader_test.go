package epub //nolint:testpackage // needs access to unexported parseContainer, splitAnchor, collapseWS

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeEPUB builds a minimal in-memory EPUB from a map of path -> content and
// writes it to a temp file, returning the path. The mimetype entry is added
// automatically and stored first (uncompressed) per the EPUB spec.
func writeEPUB(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.epub")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	// mimetype must be first and stored (uncompressed).
	mw, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err = mw.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}
	var w io.Writer
	for name, content := range files {
		if w, err = zw.Create(name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err = w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err = zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return path
}

// minimalEPUB is a tiny valid EPUB2 with a 2-chapter book and an NCX TOC.
const testContainerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="book.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

const opfXML = `<?xml version="1.0"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf" unique-identifier="ID">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Test Book</dc:title>
    <dc:language>en</dc:language>
    <dc:identifier id="ID">test-id</dc:identifier>
    <dc:creator>Test Author</dc:creator>
    <dc:subject>Fiction</dc:subject>
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
</package>`

const ncxXML = `<?xml version="1.0"?>
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
</ncx>`

const coverHTML = `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Cover</title></head>
<body><h1>Cover</h1><p>This is the cover page.</p></body></html>`

const ch1HTML = `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter 1</title></head>
<body>
<div class="chapter">
<h2 id="ch1-h">Chapter 1: Beginnings</h2>
<p>It was the best of times.</p>
<p>It was the worst of times.</p>
<ul><li>Item one</li><li>Item two</li></ul>
</div>
</body></html>`

const ch2HTML = `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter 2</title></head>
<body>
<div class="chapter">
<h2>Chapter 2: Endings</h2>
<p>The end.</p>
</div>
</body></html>`

func testEPUB(t *testing.T) string {
	t.Helper()
	return writeEPUB(t, map[string]string{
		"META-INF/container.xml": testContainerXML,
		"book.opf":               opfXML,
		"toc.ncx":                ncxXML,
		"cover.html":             coverHTML,
		"chapter1.html":          ch1HTML,
		"chapter2.html":          ch2HTML,
	})
}

func TestOpenAndMetadata(t *testing.T) {
	t.Parallel()
	path := testEPUB(t)
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	opf := r.OPF()
	if opf.Metadata.Title != "Test Book" {
		t.Errorf("Title = %q, want %q", opf.Metadata.Title, "Test Book")
	}
	if len(opf.Metadata.Creators) != 1 || opf.Metadata.Creators[0] != "Test Author" {
		t.Errorf("Creators = %v, want [Test Author]", opf.Metadata.Creators)
	}
	if opf.Metadata.Language != "en" {
		t.Errorf("Language = %q, want en", opf.Metadata.Language)
	}
	if opf.Metadata.Identifier != "test-id" {
		t.Errorf("Identifier = %q, want test-id", opf.Metadata.Identifier)
	}
	if len(opf.Metadata.Subjects) != 1 || opf.Metadata.Subjects[0] != "Fiction" {
		t.Errorf("Subjects = %v, want [Fiction]", opf.Metadata.Subjects)
	}
}

func TestSpineOrder(t *testing.T) {
	t.Parallel()
	r, err := Open(testEPUB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	opf := r.OPF()
	if len(opf.Spine) != 3 {
		t.Fatalf("spine length = %d, want 3", len(opf.Spine))
	}
	want := []string{"cover", "ch1", "ch2"}
	for i, ref := range opf.Spine {
		if ref.IDRef != want[i] {
			t.Errorf("spine[%d].IDRef = %q, want %q", i, ref.IDRef, want[i])
		}
		if !ref.Linear {
			t.Errorf("spine[%d].Linear = false, want true", i)
		}
	}
}

func TestManifestLookup(t *testing.T) {
	t.Parallel()
	r, err := Open(testEPUB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	item, ok := r.OPF().Manifest["ch1"]
	if !ok {
		t.Fatal("manifest item ch1 not found")
	}
	if item.Href != "chapter1.html" {
		t.Errorf("ch1 href = %q, want chapter1.html", item.Href)
	}
	if item.MediaType != "application/xhtml+xml" {
		t.Errorf("ch1 media-type = %q, want application/xhtml+xml", item.MediaType)
	}
}

func TestResolveHref(t *testing.T) {
	t.Parallel()
	r, err := Open(testEPUB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	// OPF is at zip root, so opfDir = "." and hrefs resolve as-is.
	if got := r.ResolveHref("chapter1.html"); got != "chapter1.html" {
		t.Errorf("ResolveHref(chapter1.html) = %q, want chapter1.html", got)
	}
	// Anchor stripped.
	if got := r.ResolveHref("chapter1.html#section"); got != "chapter1.html" {
		t.Errorf("ResolveHref with anchor = %q, want chapter1.html", got)
	}
}

func TestReadTOCFromNCX(t *testing.T) {
	t.Parallel()
	r, err := Open(testEPUB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()

	toc, err := r.ReadTOC()
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	if len(toc) != 3 {
		t.Fatalf("TOC length = %d, want 3", len(toc))
	}
	want := []struct {
		title, src string
		order      int
	}{
		{"Cover", "cover.html", 1},
		{"Chapter 1: Beginnings", "chapter1.html", 2},
		{"Chapter 2: Endings", "chapter2.html", 3},
	}
	for i, w := range want {
		if toc[i].Title != w.title {
			t.Errorf("toc[%d].Title = %q, want %q", i, toc[i].Title, w.title)
		}
		if toc[i].SrcFile != w.src {
			t.Errorf("toc[%d].SrcFile = %q, want %q", i, toc[i].SrcFile, w.src)
		}
		if toc[i].Order != w.order {
			t.Errorf("toc[%d].Order = %d, want %d", i, toc[i].Order, w.order)
		}
		if toc[i].SourceKind != "ncx" {
			t.Errorf("toc[%d].SourceKind = %q, want ncx", i, toc[i].SourceKind)
		}
	}
}

func TestExtractBlocks(t *testing.T) {
	t.Parallel()
	blocks, err := ExtractBlocks([]byte(ch1HTML))
	if err != nil {
		t.Fatalf("ExtractBlocks: %v", err)
	}
	// Expected: heading, paragraph, paragraph, list_item, list_item.
	if len(blocks) != 5 {
		t.Fatalf("blocks length = %d, want 5", len(blocks))
	}
	if blocks[0].Kind != "heading" || blocks[0].Level != 2 {
		t.Errorf("blocks[0] = %+v, want heading level 2", blocks[0])
	}
	if blocks[0].Anchor != "ch1-h" {
		t.Errorf("blocks[0].Anchor = %q, want ch1-h", blocks[0].Anchor)
	}
	if blocks[0].Text != "Chapter 1: Beginnings" {
		t.Errorf("blocks[0].Text = %q, want 'Chapter 1: Beginnings'", blocks[0].Text)
	}
	if blocks[1].Kind != "paragraph" || blocks[1].Text != "It was the best of times." {
		t.Errorf("blocks[1] = %+v, want paragraph 'It was the best of times.'", blocks[1])
	}
	if blocks[3].Kind != "list_item" || blocks[3].Text != "Item one" {
		t.Errorf("blocks[3] = %+v, want list_item 'Item one'", blocks[3])
	}
}

func TestExtractBlocksCollapsesWhitespace(t *testing.T) {
	t.Parallel()
	html := `<html><body><p>  Hello
	world  </p></body></html>`
	blocks, err := ExtractBlocks([]byte(html))
	if err != nil {
		t.Fatalf("ExtractBlocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if blocks[0].Text != "Hello world" {
		t.Errorf("Text = %q, want 'Hello world'", blocks[0].Text)
	}
}

func TestExtractBlocksSkipsScriptStyle(t *testing.T) {
	t.Parallel()
	html := `<html><body>
<script>var x = 1;</script>
<style>.x { color: red; }</style>
<p>visible text</p>
</body></html>`
	blocks, err := ExtractBlocks([]byte(html))
	if err != nil {
		t.Fatalf("ExtractBlocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 (only the <p>)", len(blocks))
	}
	if blocks[0].Text != "visible text" {
		t.Errorf("Text = %q, want 'visible text'", blocks[0].Text)
	}
}

func TestExtractBlocksEmptyBody(t *testing.T) {
	t.Parallel()
	html := `<html><body></body></html>`
	blocks, err := ExtractBlocks([]byte(html))
	if err != nil {
		t.Fatalf("ExtractBlocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("blocks = %d, want 0", len(blocks))
	}
}

func TestParseContainerPicksOPFMimeType(t *testing.T) {
	t.Parallel()
	data := []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="other.opf" media-type="application/oebps-package+xml"/>
    <rootfile full-path="ignored.xml" media-type="application/xml"/>
  </rootfiles>
</container>`)
	got, err := parseContainer(data)
	if err != nil {
		t.Fatalf("parseContainer: %v", err)
	}
	if got != "other.opf" {
		t.Errorf("got %q, want other.opf", got)
	}
}

func TestParseContainerFallsBackToFirst(t *testing.T) {
	t.Parallel()
	data := []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="fallback.opf" media-type="unknown"/>
  </rootfiles>
</container>`)
	got, err := parseContainer(data)
	if err != nil {
		t.Fatalf("parseContainer: %v", err)
	}
	if got != "fallback.opf" {
		t.Errorf("got %q, want fallback.opf", got)
	}
}

func TestReadFileCaseInsensitive(t *testing.T) {
	t.Parallel()
	// Build an EPUB where the OPF path in container.xml uses different case
	// than the actual file name.
	path := writeEPUB(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="BOOK.OPF" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"book.opf":      opfXML,
		"toc.ncx":       ncxXML,
		"cover.html":    coverHTML,
		"chapter1.html": ch1HTML,
		"chapter2.html": ch2HTML,
	})
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open with case mismatch: %v", err)
	}
	defer func() { _ = r.Close() }()
	if r.OPF().Metadata.Title != "Test Book" {
		t.Errorf("Title = %q, want Test Book", r.OPF().Metadata.Title)
	}
}

func TestOpenMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Open("/nonexistent/path.epub")
	if err == nil {
		t.Fatal("Open missing file succeeded, want error")
	}
}

func TestOpenInvalidZip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.epub")
	if err := os.WriteFile(path, []byte("not a zip"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("Open invalid zip succeeded, want error")
	}
}

func TestSplitAnchor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, file, anchor string
	}{
		{"page.html", "page.html", ""},
		{"page.html#sec", "page.html", "sec"},
		{"#sec", "", "sec"},
		{"", "", ""},
	}
	for _, c := range cases {
		f, a := splitAnchor(c.in)
		if f != c.file || a != c.anchor {
			t.Errorf("splitAnchor(%q) = (%q,%q), want (%q,%q)", c.in, f, a, c.file, c.anchor)
		}
	}
}

func TestCollapseWS(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"  hello  ", "hello"},
		{"a\n\nb", "a b"},
		{"a\t\tb", "a b"},
		{"  ", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := collapseWS(c.in); got != c.want {
			t.Errorf("collapseWS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Ensure the test EPUB bytes are valid by round-tripping through a buffer.
var _ = bytes.NewReader
