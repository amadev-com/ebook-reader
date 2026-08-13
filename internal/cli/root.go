// Package cli wires the cobra command tree for the bookai binary.
//
// The CLI is intentionally a thin layer: each command parses flags, opens a
// project.Project, and delegates to the relevant internal/* package. All
// commands share the --project persistent flag (defaults to the current
// directory) and the --verbose flag for debug logging.
package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/version"
)

var (
	flagProject string //nolint:gochecknoglobals // cobra flag binding requires package-level var
	flagVerbose bool   //nolint:gochecknoglobals // cobra flag binding requires package-level var
)

// Shared CLI constants used by multiple batch-based commands (analyze,
// pronounce, translate).

// defaultPollInterval is the default seconds between batch status polls.
const defaultPollInterval = 60

// minPollInterval is the minimum allowed poll interval; smaller values are
// clamped up to defaultPollInterval.
const minPollInterval = 10

// truncateLength is the maximum number of characters shown when truncating
// content in log/warning messages.
const truncateLength = 200

// NewRoot builds and configures the root command with its persistent flags and subcommands.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "bookai",
		Short:         "Local EPUB → translate → TTS batch pipeline",
		Long:          "bookai turns an EPUB into a consistently-translated, glossary-backed audiobook via a file-based, rerunnable pipeline.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().
		StringVarP(&flagProject, "project", "p", ".", "path to the bookai project directory (default: current dir)")
	root.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "enable debug logging")

	root.AddCommand(
		newVersionCmd(),
		newImportCmd(),
		newAnalyzeChaptersCmd(),
		newAnalyzeCmd(),
		newStatusCmd(),
		newChaptersCmd(),
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

// setupLogger configures the default logger to write text-formatted messages to standard error at info level, or debug level when verbose mode is enabled.
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

// newVersionCmd creates the command that prints the application version and current milestone.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print bookai version and current milestone",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintf(os.Stdout, "bookai %s (%s)\n", version.Version, version.Milestone)
			return nil
		},
	}
}
