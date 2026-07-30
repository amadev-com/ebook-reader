package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The following commands are stubs for Milestone 1. They exist so the CLI
// surface is complete from day one and so users get a clear message instead of
// an "unknown command" error. translate is implemented in M2 (see translate.go);
// ssml and tts are implemented in M3.

func newSSMLCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssml",
		Short: "Generate SSML with pronunciation hints from translations (Milestone 3)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("ssml: not implemented in Milestone 1 (planned for M3)")
		},
	}
}

func newTTSCmd() *cobra.Command {
	var (
		force   bool
		chapter int
		merge   bool
	)
	cmd := &cobra.Command{
		Use:   "tts",
		Short: "Synthesize audio from SSML via XTTS v2 (Milestone 3)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("tts: not implemented in Milestone 1 (planned for M3)")
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-synthesize chapters whose audio already exists")
	cmd.Flags().IntVar(&chapter, "chapter", 0, "synthesize only a single chapter id (1-based)")
	cmd.Flags().BoolVar(&merge, "merge", false, "merge per-chapter wavs into one book.mp3 with chapter markers")
	return cmd
}
