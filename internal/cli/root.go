// Package cli wires the cobra command tree for the bookai binary.
//
// The CLI is intentionally a thin layer: each command parses flags, opens a
// project.Project, and delegates to the relevant internal/* package. All
// commands share the --project persistent flag (defaults to the current
// directory) and the --verbose flag for debug logging.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/version"
)

var (
	flagProject string
	flagVerbose bool
)

// NewRoot builds the cobra root command with all subcommands attached.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "bookai",
		Short:         "Local EPUB → translate → TTS batch pipeline",
		Long:          "bookai turns an EPUB into a consistently-translated, glossary-backed audiobook via a file-based, rerunnable pipeline.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&flagProject, "project", "p", ".", "path to the bookai project directory (default: current dir)")
	root.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "enable debug logging")

	root.AddCommand(
		newVersionCmd(),
		newImportCmd(),
		newAnalyzeChaptersCmd(),
		newAnalyzeCmd(),
		newStatusCmd(),
		newTranslateCmd(),
		newVerifyGlossaryCmd(),
		newPronounceCmd(),
		newSSMLCmd(),
		newTTSCmd(),
	)
	return root
}

// openProject loads the project at flagProject, applying defaults.
func openProject() (*project.Project, error) {
	return project.New(flagProject)
}

// rootContext returns a context cancelled on SIGINT/SIGTERM.
func rootContext() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx, cancel
}

// setupLogger configures slog to stderr at the requested level.
func setupLogger() {
	level := slog.LevelInfo
	if flagVerbose {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// Fail prints err to stderr in a single human-readable line and returns the
// exit code 1. With --verbose the full error chain is printed. Exported so
// cmd/bookai/main.go can use the same verbose-aware formatting.
func Fail(err error) int {
	if flagVerbose {
		fmt.Fprintf(os.Stderr, "error: %+v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	return 1
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print bookai version and current milestone",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Printf("bookai %s (%s)\n", version.Version, version.Milestone)
			return nil
		},
	}
}
