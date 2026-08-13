package epub

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/net/html"
)

// navElement is the HTML tag name for the EPUB nav element, also used as the
// EPUB3 manifest "nav" property and as a SourceKind value in TOC entries.
const navElement = "nav"

// BlockKindHeading is the Block.Kind value for h1-h6 headings.
const BlockKindHeading = "heading"

// Block is a semantic unit of a spine item's body: a heading, paragraph, list
// item, blockquote, etc. Headings carry their Level (1-6) and an Anchor (the
// id attribute of the heading or its nearest ancestor with an id) so TOC
// anchors can be matched to in-document positions.
type Block struct {
	Kind   string `json:"kind"`             // "heading" | "paragraph" | "list_item" | "blockquote" | "pre" | "hr" | "image"
	Level  int    `json:"level,omitempty"`  // heading level 1-6 (Kind=="heading" only)
	Anchor string `json:"anchor,omitempty"` // id of the heading or nearest ancestor with id
	Text   string `json:"text"`             // trimmed text content (empty for hr/image)
	Alt    string `json:"alt,omitempty"`    // image alt text (Kind=="image" only)
}

// ExtractBlocks parses an XHTML spine item and returns its body as a sequence
// of Blocks. The HTML parser's error recovery is used for malformed input, so
// this never fails on bad HTML — only on I/O errors from the caller.
func ExtractBlocks(xhtml []byte) ([]Block, error) {
	doc, err := html.Parse(bytes.NewReader(xhtml))
	if err != nil {
		return nil, fmt.Errorf("parse xhtml: %w", err)
	}
	body := findFirst(doc, "body")
	if body == nil {
		return nil, nil
	}
	var blocks []Block
	walkBody(body, "", &blocks)
	return blocks, nil
}

// walkBody recursively walks the DOM, emitting a Block for each block-level
// element. currentAnchor is the id of the nearest ancestor element that had an
// walkBody recursively traverses a body node and extracts semantic blocks into out,
// propagating the current anchor through nested elements.
func walkBody(n *html.Node, currentAnchor string, out *[]Block) {
	if n.Type == html.ElementNode {
		walkElement(n, currentAnchor, out)
		return
	}
	// Text nodes outside any block element: only emit if non-trivial, to
	// avoid capturing inter-element whitespace. This is a safety net for
	// poorly-structured HTML where text sits directly in <body>.
	if n.Type == html.TextNode {
		text := collapseWS(n.Data)
		if text != "" && len(text) > 2 {
			slog.Default().Debug("epub: stray text outside block element", "text", text)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkBody(c, currentAnchor, out)
	}
}

// walkElement handles a single element node, dispatching on the tag name to
// walkElement emits a block for supported content elements or recursively processes their children.
func walkElement(n *html.Node, currentAnchor string, out *[]Block) {
	switch n.Data {
	case "script", "style", "head", navElement, "svg":
		// Skip non-content elements. (nav is skipped because TOC nav
		// documents are handled separately; in-content nav is rare.)
		return
	case "h1", "h2", "h3", "h4", "h5", "h6":
		handleHeadingBlock(n, currentAnchor, out)
		return
	case "p":
		handleTextBlock(n, currentAnchor, out, "paragraph")
		return
	case "li":
		handleTextBlock(n, currentAnchor, out, "list_item")
		return
	case "blockquote":
		// Recurse into children so nested <p> become paragraphs, but
		// record the blockquote's id as the anchor context.
		handleContainerBlock(n, currentAnchor, out)
		return
	case "pre":
		handleTextBlock(n, currentAnchor, out, "pre")
		return
	case "hr":
		*out = append(*out, Block{Kind: "hr"})
		return
	case "img":
		alt := getAttr(n, "alt")
		*out = append(*out, Block{Kind: "image", Alt: alt})
		return
	case "div", "section", "article", "main", "header", "footer", "aside", "figure", "figcaption":
		// Container elements: descend, propagating any id as the anchor.
		handleContainerBlock(n, currentAnchor, out)
		return
	case "a", "span", "em", "strong", "b", "i", "u", "small", "sub", "sup",
		"code", "abbr", "cite", "q", "mark", "del", "ins", "s", "time":
		// Inline elements: their text is captured by the enclosing
		// block-level element's textOf call, so we don't emit a block
		// here. We must NOT descend, or text would be duplicated.
		return
	default:
		// Unknown element: descend conservatively, propagating anchor.
		handleContainerBlock(n, currentAnchor, out)
		return
	}
}

// handleHeadingBlock emits a heading Block for h1-h6 elements, using the
// handleHeadingBlock appends a heading block with collapsed text and its own anchor ID,
// falling back to the inherited anchor when the heading has no ID.
func handleHeadingBlock(n *html.Node, currentAnchor string, out *[]Block) {
	level := int(n.Data[1] - '0')
	anchor := getAttr(n, "id")
	if anchor == "" {
		anchor = currentAnchor
	}
	*out = append(*out, Block{
		Kind:   BlockKindHeading,
		Level:  level,
		Anchor: anchor,
		Text:   collapseWS(textOf(n)),
	})
}

// handleTextBlock emits a text Block (paragraph, list_item, or pre) if the
// handleTextBlock appends a text block with collapsed whitespace when the node contains text.
// currentAnchor identifies the block's anchor, and kind specifies its semantic block type.
func handleTextBlock(n *html.Node, currentAnchor string, out *[]Block, kind string) {
	text := collapseWS(textOf(n))
	if text != "" {
		*out = append(*out, Block{Kind: kind, Anchor: currentAnchor, Text: text})
	}
}

// handleContainerBlock descends into an element's children, propagating any id
// as the anchor context. Used for container elements (div, section, blockquote,
// handleContainerBlock processes a container's children while propagating its ID as the active anchor.
func handleContainerBlock(n *html.Node, currentAnchor string, out *[]Block) {
	anchor := getAttr(n, "id")
	if anchor == "" {
		anchor = currentAnchor
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkBody(c, anchor, out)
	}
}

// collapseWS collapses runs of whitespace (including newlines) into single
// spaces and trims the result. EPUB XHTML commonly has multi-line indented
// text that should become a single line.
func collapseWS(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
