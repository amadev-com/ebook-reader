// Package version holds build-time version metadata for the bookai CLI.
package version

// Version is the semantic version of the bookai binary. It is overridden at
// build time via -ldflags "-X ebook-reader/internal/version.Version=...".
var Version = "0.0.0-dev" //nolint:gochecknoglobals // set via -ldflags at build time

// Milestone describes the current feature milestone (used in `bookai version`).
const Milestone = "M1: EPUB import + chapter detection"
