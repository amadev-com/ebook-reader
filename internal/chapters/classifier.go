// Package chapters turns the extracted EPUB spine + TOC + per-item blocks into
// a clean list of chapters, excluding front/back matter.
//
// Milestone 1 uses a rules-only classifier (no AI). Ambiguous sections default
// to Skip and are recorded in _skipped.json for human review. An AI-fallback
// hook is designed but not wired.
package chapters

import (
	"regexp"
	"strings"
)

// Classifier rule names and decision labels used in Reason.Rule and
// Decision.String(). Extracted as constants for goconst.
const (
	decisionLabelUnknown = "UNKNOWN"

	ruleCopyright        = "copyright"
	ruleContents         = "contents"
	ruleAcknowledgements = "acknowledgements"
	ruleChapterNum       = "chapter-num"
	rulePart             = "part"
)

// Decision is the classifier's verdict for a section.
type Decision int

const (
	// DecisionUnknown is the zero value; treat as Ambiguous.
	DecisionUnknown Decision = iota
	// DecisionKeep means the section is a real chapter and should be translated.
	DecisionKeep
	// DecisionSkip means the section is front/back matter (cover, copyright,
	// TOC, acknowledgements, etc.) and should not be translated.
	DecisionSkip
	// DecisionAmbiguous means no rule matched. In M1 this is treated as Skip
	// and recorded in _skipped.json for review.
	DecisionAmbiguous
)

// String returns a human-readable label for the decision.
func (d Decision) String() string {
	switch d {
	case DecisionUnknown:
		return decisionLabelUnknown
	case DecisionKeep:
		return "KEEP"
	case DecisionSkip:
		return "SKIP"
	case DecisionAmbiguous:
		return "AMBIGUOUS"
	}
	return decisionLabelUnknown
}

// Reason records why a classifier reached its decision.
type Reason struct {
	Decision Decision `json:"decision"`
	Rule     string   `json:"rule"` // name of the rule that fired, "" for default
}

// skipPatterns are case-insensitive substring/regex patterns for front/back
// matter titles. Matched against the trimmed title.
//
//nolint:gochecknoglobals // compiled regex patterns, immutable after init
var skipPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"cover", regexp.MustCompile(`(?i)^cover(\s|$)`)},
	{ruleCopyright, regexp.MustCompile(`(?i)copyright`)},
	{"title-page", regexp.MustCompile(`(?i)^title\s*page`)},
	{ruleContents, regexp.MustCompile(`(?i)^(table\s+of\s+)?contents?$`)},
	{"toc", regexp.MustCompile(`(?i)^toc$`)},
	{ruleAcknowledgements, regexp.MustCompile(`(?i)^acknowledg(e)?ments`)},
	{"about-author", regexp.MustCompile(`(?i)^about\s+the\s+author`)},
	{"about-book", regexp.MustCompile(`(?i)^about\s+(the\s+)?book`)},
	{"dedication", regexp.MustCompile(`(?i)^dedication`)},
	{"also-by", regexp.MustCompile(`(?i)^also\s+by`)},
	{"colophon", regexp.MustCompile(`(?i)^colophon`)},
	{"preview", regexp.MustCompile(`(?i)^preview`)},
	{"excerpt", regexp.MustCompile(`(?i)^excerpt`)},
	{"discussion", regexp.MustCompile(`(?i)^discussion\s+questions`)},
	{"praise", regexp.MustCompile(`(?i)^praise\s+for`)},
	{"information", regexp.MustCompile(`(?i)^information$`)},
	{"notes", regexp.MustCompile(`(?i)^notes$`)},
	{"references", regexp.MustCompile(`(?i)^references$`)},
	{"index", regexp.MustCompile(`(?i)^index$`)},
	{"glossary", regexp.MustCompile(`(?i)^glossary$`)},
	{"map", regexp.MustCompile(`(?i)^map$`)},
	{"illustrations", regexp.MustCompile(`(?i)^list\s+of\s+illustrations`)},
	{"figures", regexp.MustCompile(`(?i)^list\s+of\s+figures`)},
}

// keepPatterns are case-insensitive regex patterns for chapter-like titles.
//
//nolint:gochecknoglobals // compiled regex patterns, immutable after init
var keepPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{ruleChapterNum, regexp.MustCompile(`(?i)^chapter\s+(\d+|[ivxlcdm]+)\b`)},
	{"chapter-word", regexp.MustCompile(`(?i)^chapter\s+([a-z]|[ivxlcdm]+)\b`)},
	{"chapter-any", regexp.MustCompile(`(?i)^chapter\b`)},
	{rulePart, regexp.MustCompile(`(?i)^part\s+(\d+|[ivxlcdm]+)\b`)},
	{"book-num", regexp.MustCompile(`(?i)^book\s+(\d+|[ivxlcdm]+)\b`)},
	{"volume", regexp.MustCompile(`(?i)^volume\s+(\d+|[ivxlcdm]+)\b`)},
	{"prologue", regexp.MustCompile(`(?i)^prologue\b`)},
	{"epilogue", regexp.MustCompile(`(?i)^epilogue\b`)},
	{"preface", regexp.MustCompile(`(?i)^preface\b`)},
	{"introduction", regexp.MustCompile(`(?i)^introduction\b`)},
	{"foreword", regexp.MustCompile(`(?i)^foreword\b`)},
	{"afterword", regexp.MustCompile(`(?i)^afterword\b`)},
	{"appendix", regexp.MustCompile(`(?i)^appendix\b`)},
}

// ClassifyTitle returns the classifier's decision for a section title. The
// title is trimmed and matched against skip patterns first (front matter is
// usually listed first and has distinctive names), then keep patterns. If
// neither matches, the result is Ambiguous.
func ClassifyTitle(title string) Reason {
	t := strings.TrimSpace(title)
	if t == "" {
		return Reason{Decision: DecisionSkip, Rule: "empty-title"}
	}
	for _, p := range skipPatterns {
		if p.re.MatchString(t) {
			return Reason{Decision: DecisionSkip, Rule: p.name}
		}
	}
	for _, p := range keepPatterns {
		if p.re.MatchString(t) {
			return Reason{Decision: DecisionKeep, Rule: p.name}
		}
	}
	return Reason{Decision: DecisionAmbiguous, Rule: ""}
}

// IsKeep reports whether a title classifies as Keep.
func IsKeep(title string) bool {
	return ClassifyTitle(title).Decision == DecisionKeep
}

// IsSkip reports whether a title classifies as Skip.
func IsSkip(title string) bool {
	return ClassifyTitle(title).Decision == DecisionSkip
}
