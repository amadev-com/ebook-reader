package epub

import (
	"encoding/xml"
	"fmt"
)

// OPF is the parsed OPF package document: metadata + manifest + spine.
type OPF struct {
	Metadata Metadata
	Manifest map[string]ManifestItem // keyed by item id
	Spine    []SpineItemRef          // in reading order
}

// Metadata is the Dublin Core metadata subset we extract.
type Metadata struct {
	Title       string
	Creators    []string
	Language    string
	Identifier  string
	Description string
	Publisher   string
	Subjects    []string
	Date        string
}

// ManifestItem is one <item> in the OPF manifest.
type ManifestItem struct {
	ID         string
	Href       string // raw href as in the OPF (relative to OPF dir)
	MediaType  string
	Properties []string // EPUB3 properties (e.g. "nav")
}

// SpineItemRef is one <itemref> in the OPF spine.
type SpineItemRef struct {
	IDRef  string
	Linear bool
}

// rawOPF mirrors the OPF XML structure for unmarshalling.
type rawOPF struct {
	XMLName  xml.Name    `xml:"package"`
	Metadata rawMetadata `xml:"metadata"`
	Manifest rawManifest `xml:"manifest"`
	Spine    rawSpine    `xml:"spine"`
	Version  string      `xml:"version,attr"`
}

type rawMetadata struct {
	Title       string   `xml:"title"`
	Creators    []string `xml:"creator"`
	Language    string   `xml:"language"`
	Identifier  string   `xml:"identifier"`
	Description string   `xml:"description"`
	Publisher   string   `xml:"publisher"`
	Subjects    []string `xml:"subject"`
	Date        string   `xml:"date"`
}

type rawManifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"` // EPUB3, space-separated
}

type rawManifest struct {
	Items []rawManifestItem `xml:"item"`
}

type rawSpineItemRef struct {
	IDRef  string `xml:"idref,attr"`
	Linear string `xml:"linear,attr"`
}

type rawSpine struct {
	ItemRefs []rawSpineItemRef `xml:"itemref"`
}

// parseOPF parses raw OPF XML into the structured OPF type.
func parseOPF(data []byte) (*OPF, error) {
	var raw rawOPF
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal opf: %w", err)
	}
	opf := &OPF{
		Metadata: Metadata{
			Title:       raw.Metadata.Title,
			Creators:    raw.Metadata.Creators,
			Language:    raw.Metadata.Language,
			Identifier:  raw.Metadata.Identifier,
			Description: raw.Metadata.Description,
			Publisher:   raw.Metadata.Publisher,
			Subjects:    raw.Metadata.Subjects,
			Date:        raw.Metadata.Date,
		},
		Manifest: make(map[string]ManifestItem, len(raw.Manifest.Items)),
	}
	for _, it := range raw.Manifest.Items {
		props := splitSpaces(it.Properties)
		opf.Manifest[it.ID] = ManifestItem{
			ID:         it.ID,
			Href:       it.Href,
			MediaType:  it.MediaType,
			Properties: props,
		}
	}
	for _, ref := range raw.Spine.ItemRefs {
		// Per the OPF spec, linear defaults to "yes" when the attribute is
		// absent. Only an explicit "no" marks a non-linear item.
		linear := ref.Linear != "no"
		opf.Spine = append(opf.Spine, SpineItemRef{IDRef: ref.IDRef, Linear: linear})
	}
	if len(opf.Spine) == 0 {
		return nil, fmt.Errorf("opf spine is empty")
	}
	return opf, nil
}

// splitSpaces splits a space-separated attribute value, dropping empties.
func splitSpaces(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
