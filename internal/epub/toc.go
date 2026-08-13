package epub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// TOCEntry is one row of the table of contents, normalized across EPUB2 NCX
// and EPUB3 nav. SrcFile is the manifest href (file only, anchor stripped);
// SrcAnchor is the fragment after '#' (may be empty).
type TOCEntry struct {
	Title      string
	SrcFile    string
	SrcAnchor  string
	Order      int    // 1-based, in document order
	Depth      int    // nesting depth, 1 = top level
	SourceKind string // "ncx" or "nav"
}

// ReadTOC reads the table of contents. It first looks for an EPUB3 nav
// document (manifest item with property "nav"); if found, it parses that.
// Otherwise it falls back to the NCX referenced by the spine's toc attribute
// (located via the manifest). If neither is present, it returns nil, nil.
func (r *Reader) ReadTOC() ([]TOCEntry, error) {
	if entries, err := r.readNavTOC(); err != nil {
		return nil, err
	} else if entries != nil {
		return entries, nil
	}
	return r.readNCXTOC()
}

// readNavTOC finds and parses the EPUB3 nav document, if any.
func (r *Reader) readNavTOC() ([]TOCEntry, error) {
	var navItem *ManifestItem
	for id, it := range r.opf.Manifest {
		if hasProperty(it.Properties, navElement) {
			navItem = new(r.opf.Manifest[id])
			break
		}
	}
	if navItem == nil {
		return nil, nil
	}
	data, err := r.ReadFile(r.ResolveHref(navItem.Href))
	if err != nil {
		return nil, fmt.Errorf("read nav %s: %w", navItem.Href, err)
	}
	entries, err := parseNav(data)
	if err != nil {
		return nil, fmt.Errorf("parse nav %s: %w", navItem.Href, err)
	}
	for i := range entries {
		entries[i].SrcFile = r.ResolveHref(entries[i].SrcFile)
	}
	return entries, nil
}

// readNCXTOC parses toc.ncx. The NCX id is referenced by the spine's toc
// attribute; we don't capture that attribute, so we look for a manifest item
// whose media-type is application/x-dtbncx+xml.
func (r *Reader) readNCXTOC() ([]TOCEntry, error) {
	var ncxItem *ManifestItem
	for _, it := range r.opf.Manifest {
		if it.MediaType == "application/x-dtbncx+xml" {
			x := it
			ncxItem = &x
			break
		}
	}
	if ncxItem == nil {
		return nil, nil
	}
	data, err := r.ReadFile(r.ResolveHref(ncxItem.Href))
	if err != nil {
		return nil, fmt.Errorf("read ncx %s: %w", ncxItem.Href, err)
	}
	entries, err := parseNCX(data)
	if err != nil {
		return nil, fmt.Errorf("parse ncx %s: %w", ncxItem.Href, err)
	}
	for i := range entries {
		entries[i].SrcFile = r.ResolveHref(entries[i].SrcFile)
	}
	return entries, nil
}

// hasProperty reports whether the specified property is present in a list of properties.
func hasProperty(props []string, want string) bool {
	return slices.Contains(props, want)
}

// --- NCX parsing ---

type ncxContent struct {
	Src string `xml:"src,attr"`
}

type ncxNavPoint struct {
	ID        string        `xml:"id,attr"`
	PlayOrder string        `xml:"playOrder,attr"`
	Label     string        `xml:"navLabel>text"`
	Content   ncxContent    `xml:"content"`
	Children  []ncxNavPoint `xml:"navPoint"`
}

type ncxDoc struct {
	XMLName xml.Name      `xml:"ncx"`
	NavMap  []ncxNavPoint `xml:"navMap>navPoint"`
}

// parseNCX parses toc.ncx into a flat, ordered list of TOCEntry. Nested
// navPoints are flattened with their Depth recorded.
func parseNCX(data []byte) ([]TOCEntry, error) {
	var ncx ncxDoc
	if err := xml.Unmarshal(data, &ncx); err != nil {
		return nil, fmt.Errorf("unmarshal ncx: %w", err)
	}
	var out []TOCEntry
	order := 0
	var walk func(points []ncxNavPoint, depth int)
	walk = func(points []ncxNavPoint, depth int) {
		for _, p := range points {
			order++
			file, anchor := splitAnchor(p.Content.Src)
			out = append(out, TOCEntry{
				Title:      strings.TrimSpace(p.Label),
				SrcFile:    file,
				SrcAnchor:  anchor,
				Order:      order,
				Depth:      depth,
				SourceKind: "ncx",
			})
			walk(p.Children, depth+1)
		}
	}
	walk(ncx.NavMap, 1)
	return out, nil
}

// splitAnchor splits "file.html#anchor" into ("file.html", "anchor").
func splitAnchor(src string) (string, string) {
	if before, after, ok := strings.Cut(src, "#"); ok {
		return before, after
	}
	return src, ""
}

// --- nav.xhtml parsing ---

// parseNav parses an EPUB3 nav document. The nav element with epub:type="toc"
// (or the first nav element if none is typed) is walked. Anchors are taken
// from <a href="..."> inside <li>.
func parseNav(data []byte) ([]TOCEntry, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse nav html: %w", err)
	}
	var out []TOCEntry
	order := 0
	var walkList func(*html.Node, int)
	walkList = func(ol *html.Node, depth int) {
		for li := ol.FirstChild; li != nil; li = li.NextSibling {
			if li.Type != html.ElementNode || li.Data != "li" {
				continue
			}
			// The first <a> inside this <li> is the entry's link.
			a := findFirst(li, "a")
			if a == nil {
				// Could be a nested <ol> with no link (label-only group).
				if nested := findFirst(li, "ol"); nested != nil {
					walkList(nested, depth)
				}
				continue
			}
			href := getAttr(a, "href")
			title := strings.TrimSpace(textOf(a))
			file, anchor := splitAnchor(href)
			order++
			out = append(out, TOCEntry{
				Title:      title,
				SrcFile:    file,
				SrcAnchor:  anchor,
				Order:      order,
				Depth:      depth,
				SourceKind: navElement,
			})
			// A nested <ol> inside this <li> represents children.
			if nested := findFirst(li, "ol"); nested != nil {
				walkList(nested, depth+1)
			}
		}
	}
	// Find the toc nav: prefer epub:type="toc", else the first <nav>.
	tocNav := findTOCNav(doc)
	if tocNav == nil {
		return out, nil
	}
	if ol := findFirst(tocNav, "ol"); ol != nil {
		walkList(ol, 1)
	}
	return out, nil
}

// findTOCNav locates the <nav> element representing the table of contents.
func findTOCNav(root *html.Node) *html.Node {
	var first, typed *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == navElement {
			if first == nil {
				first = n
			}
			if getAttr(n, "epub:type") == "toc" && typed == nil {
				typed = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if typed != nil {
		return typed
	}
	return first
}

func findFirst(n *html.Node, tag string) *html.Node {
	var found *html.Node
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == tag {
			found = n
			return true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(n)
	return found
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
