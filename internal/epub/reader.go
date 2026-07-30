// Package epub reads EPUB files using only the standard library plus
// golang.org/x/net/html for XHTML parsing. It does not depend on any
// third-party EPUB library, giving full control over spine/NCX/nav parsing
// and graceful handling of malformed HTML.
//
// The reader supports EPUB 2 (toc.ncx) and EPUB 3 (nav.xhtml). It does NOT
// decrypt DRM-protected files.
package epub

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
)

// Reader is an opened EPUB file. It holds the zip reader and the parsed OPF.
// Call Open to construct one and Close when done.
type Reader struct {
	zip    *zip.ReadCloser
	opf    *OPF
	opfDir string // directory of the OPF file inside the zip, for resolving hrefs
}

// Open opens the EPUB at path and parses its OPF.
func Open(path string) (*Reader, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open epub %s: %w", path, err)
	}
	r := &Reader{zip: zr}
	if err := r.loadOPF(); err != nil {
		_ = zr.Close()
		return nil, err
	}
	return r, nil
}

// Close releases the underlying zip reader.
func (r *Reader) Close() error {
	if r.zip == nil {
		return nil
	}
	return r.zip.Close()
}

// OPF returns the parsed package document.
func (r *Reader) OPF() *OPF { return r.opf }

// loadOPF locates and parses the OPF file referenced by META-INF/container.xml.
func (r *Reader) loadOPF() error {
	container, err := r.readFile("META-INF/container.xml")
	if err != nil {
		return fmt.Errorf("read container.xml: %w", err)
	}
	opfPath, err := parseContainer(container)
	if err != nil {
		return err
	}
	opfData, err := r.readFile(opfPath)
	if err != nil {
		return fmt.Errorf("read opf %s: %w", opfPath, err)
	}
	opf, err := parseOPF(opfData)
	if err != nil {
		return fmt.Errorf("parse opf %s: %w", opfPath, err)
	}
	r.opf = opf
	r.opfDir = path.Dir(opfPath)
	return nil
}

// readFile reads a file from the zip archive by its exact name.
func (r *Reader) readFile(name string) ([]byte, error) {
	rc, err := r.openEntry(name)
	if err != nil {
		return nil, fmt.Errorf("file %q not in archive: %w", name, err)
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// openEntry opens a zip entry by exact name, falling back to a
// case-insensitive match for archives with inconsistent casing.
func (r *Reader) openEntry(name string) (io.ReadCloser, error) {
	for _, zf := range r.zip.File {
		if zf.Name == name {
			return zf.Open()
		}
	}
	for _, zf := range r.zip.File {
		if strings.EqualFold(zf.Name, name) {
			return zf.Open()
		}
	}
	return nil, fmt.Errorf("file %q not in archive", name)
}

// ReadFile is the exported form of readFile, used by the extractor to read
// spine XHTML files by their manifest href (resolved against the OPF dir).
func (r *Reader) ReadFile(name string) ([]byte, error) {
	return r.readFile(name)
}

// ResolveHref resolves a manifest href (relative to the OPF directory) to an
// absolute path inside the zip archive.
func (r *Reader) ResolveHref(href string) string {
	// Strip any fragment (#anchor); the file is what we need to open.
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	cleaned := path.Clean(path.Join(r.opfDir, href))
	// path.Clean on a path inside the archive root may produce a leading
	// "./" or strip it; normalize so it matches zip entry names.
	cleaned = strings.TrimPrefix(cleaned, "./")
	return cleaned
}

// containerRootfile is the subset of META-INF/container.xml we care about.
type containerRootfile struct {
	FullPath  string `xml:"full-path,attr"`
	MediaType string `xml:"media-type,attr"`
}

type containerXML struct {
	XMLName   xml.Name            `xml:"container"`
	Rootfiles []containerRootfile `xml:"rootfiles>rootfile"`
}

// parseContainer extracts the OPF path from container.xml. It picks the
// rootfile with media-type application/oebps-package+xml; if none is tagged,
// it falls back to the first rootfile.
func parseContainer(data []byte) (string, error) {
	var c containerXML
	if err := xml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	if len(c.Rootfiles) == 0 {
		return "", fmt.Errorf("container.xml has no rootfiles")
	}
	for _, rf := range c.Rootfiles {
		if rf.MediaType == "application/oebps-package+xml" {
			return rf.FullPath, nil
		}
	}
	return c.Rootfiles[0].FullPath, nil
}
