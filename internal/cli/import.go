package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"ebook-reader/internal/config"
	"ebook-reader/internal/epub"
	"ebook-reader/internal/project"
)

// newImportCmd implements `bookai import <epub> [name]`. It creates a project
// directory (named after the EPUB filename or the optional name argument),
// copies the EPUB into it, writes a default config.yaml, and extracts the
// spine, TOC, and per-item blocks.
//
// If --project is explicitly set, that directory is used instead of
// auto-creating one from the book name.
func newImportCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "import <epub> [name]",
		Short: "Create a project, copy the EPUB into it, and extract spine, TOC, and per-item blocks",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()

			epubPath := args[0]
			bookName := ""
			if len(args) >= 2 {
				bookName = args[1]
			}

			projDir, err := resolveProjectDir(cmd, epubPath, bookName)
			if err != nil {
				return err
			}

			proj, err := project.New(projDir)
			if err != nil {
				return err
			}
			return runImport(ctx, proj, epubPath, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing source/original.epub")
	return cmd
}

// resolveProjectDir determines the project directory for the import command.
// If --project was explicitly set (non-default), use it as-is. Otherwise,
// derive a directory name from the EPUB filename (or the optional bookName
// argument) and create it in the current working directory.
func resolveProjectDir(cmd *cobra.Command, epubPath, bookName string) (string, error) {
	// Check if --project was explicitly set by the user.
	projectFlag := cmd.Flag("project")
	if projectFlag != nil && projectFlag.Changed {
		return projectFlag.Value.String(), nil
	}

	// Derive the project name from the book name or EPUB filename.
	if bookName == "" {
		bookName = strings.TrimSuffix(filepath.Base(epubPath), filepath.Ext(epubPath))
	}
	dirName := slugify(bookName)

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	projDir := filepath.Join(cwd, dirName)

	if err := os.MkdirAll(projDir, 0o755); err != nil {
		return "", fmt.Errorf("create project directory %s: %w", projDir, err)
	}

	// Write a default config.yaml if one doesn't exist.
	cfgPath := filepath.Join(projDir, "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfg := config.Default(dirName)
		if err := config.Save(projDir, cfg); err != nil {
			return "", fmt.Errorf("write config: %w", err)
		}
		slog.Info("created project", "dir", projDir, "config", cfgPath)
	}

	return projDir, nil
}

// slugify converts a string to a lowercase, dash-separated slug suitable for
// a directory name. Spaces, underscores, and other non-alphanumeric runes
// become dashes; consecutive dashes are collapsed; leading/trailing dashes
// are trimmed.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// runImport copies the EPUB into source/original.epub, parses it, and writes:
//   - extracted/metadata.json  (book title, author, language, identifier)
//   - extracted/spine.json     (ordered list of spine item ids)
//   - extracted/toc.json       (normalized TOC entries)
//   - extracted/raw/itemNNN.*  (raw bytes of each spine item, for debugging)
//   - extracted/blocks/itemNNN.json (structured Block list per spine item)
//
// It is idempotent: if source/original.epub already exists and --force is not
// set, it returns an error pointing the user at --force.
func runImport(_ context.Context, proj *project.Project, epubPath string, force bool) error {
	if err := proj.EnsureDirs(); err != nil {
		return err
	}
	dst := proj.SourceEpub()
	if project.Exists(dst) && !force {
		return fmt.Errorf("source epub already exists at %s (use --force to overwrite)", dst)
	}

	abs, err := filepath.Abs(epubPath)
	if err != nil {
		return fmt.Errorf("resolve epub path: %w", err)
	}
	if err := copyFile(abs, dst); err != nil {
		return err
	}
	slog.Info("imported epub", "source", abs, "dest", dst)

	r, err := epub.Open(dst)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	opf := r.OPF()

	// metadata.json
	if err := project.SaveJSON(
		filepath.Join(proj.ExtractedDir(), "metadata.json"),
		buildExtractedMetadata(opf),
	); err != nil {
		return err
	}

	// spine.json: ordered list of {id, href, media_type, linear}.
	spine := make([]spineEntry, 0, len(opf.Spine))
	for _, ref := range opf.Spine {
		item, ok := opf.Manifest[ref.IDRef]
		if !ok {
			slog.Warn("spine itemref references missing manifest item", "idref", ref.IDRef)
			continue
		}
		spine = append(spine, spineEntry{
			ID:        item.ID,
			Href:      item.Href,
			MediaType: item.MediaType,
			Linear:    ref.Linear,
		})
	}
	if err := project.SaveJSON(filepath.Join(proj.ExtractedDir(), "spine.json"), spine); err != nil {
		return err
	}
	slog.Info("wrote spine", "items", len(spine))

	// toc.json
	toc, err := r.ReadTOC()
	if err != nil {
		return err
	}
	if toc == nil {
		toc = []epub.TOCEntry{}
	}
	if err := project.SaveJSON(filepath.Join(proj.ExtractedDir(), "toc.json"), toc); err != nil {
		return err
	}
	slog.Info("wrote toc", "entries", len(toc))

	// Per-spine-item raw + blocks. Only xhtml/html items are extracted; other
	// media types (images, css) are skipped.
	for i, entry := range spine {
		if !isHTMLMediaType(entry.MediaType) {
			slog.Debug("skip non-html spine item", "id", entry.ID, "media_type", entry.MediaType)
			continue
		}
		zipPath := r.ResolveHref(entry.Href)
		raw, err := r.ReadFile(zipPath)
		if err != nil {
			return fmt.Errorf("read spine item %s (%s): %w", entry.ID, zipPath, err)
		}
		// raw with stable filename based on spine position.
		rawName := fmt.Sprintf("item%03d%s", i, filepath.Ext(entry.Href))
		if err := project.SaveBytes(filepath.Join(proj.ExtractedDir(), "raw", rawName), raw); err != nil {
			return err
		}
		blocks, err := epub.ExtractBlocks(raw)
		if err != nil {
			return fmt.Errorf("extract blocks for %s: %w", entry.ID, err)
		}
		blockDoc := itemBlocks{
			SpineIndex: i,
			ItemID:     entry.ID,
			Href:       entry.Href,
			ZipPath:    zipPath,
			RawFile:    rawName,
			Blocks:     blocks,
		}
		blockName := fmt.Sprintf("item%03d.json", i)
		if err := project.SaveJSON(filepath.Join(proj.ExtractedDir(), "blocks", blockName), blockDoc); err != nil {
			return err
		}
		slog.Info("extracted spine item", "index", i, "id", entry.ID, "blocks", len(blocks))
	}
	return nil
}

// --- artifact types ---

type extractedMetadata struct {
	Title       string   `json:"title"`
	Creators    []string `json:"creators"`
	Language    string   `json:"language"`
	Identifier  string   `json:"identifier"`
	Description string   `json:"description,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Subjects    []string `json:"subjects,omitempty"`
	Date        string   `json:"date,omitempty"`
}

type spineEntry struct {
	ID        string `json:"id"`
	Href      string `json:"href"`
	MediaType string `json:"media_type"`
	Linear    bool   `json:"linear"`
}

type itemBlocks struct {
	SpineIndex int          `json:"spine_index"`
	ItemID     string       `json:"item_id"`
	Href       string       `json:"href"`
	ZipPath    string       `json:"zip_path"`
	RawFile    string       `json:"raw_file"`
	Blocks     []epub.Block `json:"blocks"`
}

func buildExtractedMetadata(opf *epub.OPF) extractedMetadata {
	m := opf.Metadata
	return extractedMetadata{
		Title:       m.Title,
		Creators:    m.Creators,
		Language:    m.Language,
		Identifier:  m.Identifier,
		Description: m.Description,
		Publisher:   m.Publisher,
		Subjects:    m.Subjects,
		Date:        m.Date,
	}
}

func isHTMLMediaType(mt string) bool {
	switch mt {
	case "application/xhtml+xml", "application/x-dtbxhtml", "text/html":
		return true
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dest %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}
