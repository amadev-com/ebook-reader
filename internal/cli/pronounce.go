package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
	"ebook-reader/internal/tts"
)

// newPronounceCmd implements `bookai pronounce`: reads ai/glossary.json and
// ai/characters.json, asks the helper model for IPA phonetic transcriptions
// of the Russian terms, and writes ai/pronunciation.json. This file is
// consumed by `bookai ssml` to insert <phoneme> tags into SSML output.
func newPronounceCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "pronounce",
		Short: "Generate IPA pronunciation hints for glossary terms (ai/pronunciation.json)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			setupLogger()
			ctx, cancel := rootContext()
			defer cancel()
			proj, err := openProject()
			if err != nil {
				return err
			}
			return runPronounce(ctx, proj, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-generate even if pronunciation.json already exists")
	return cmd
}

// runPronounce loads the glossary and characters, builds a list of Russian
// terms, asks the model for IPA phonemes, and writes ai/pronunciation.json.
func runPronounce(ctx context.Context, proj *project.Project, force bool) error {
	pronPath := proj.AIDir() + "/pronunciation.json"
	if project.Exists(pronPath) && !force {
		slog.Info("pronunciation.json already exists, skipping (use --force to re-generate)", "path", pronPath)
		return nil
	}

	// Load glossary.
	glossary, err := translation.LoadGlossary(proj.AIDir())
	if err != nil {
		return fmt.Errorf("load glossary: %w (run `bookai analyze` first)", err)
	}
	if len(glossary.Terms) == 0 {
		return fmt.Errorf("glossary is empty — run `bookai analyze` first")
	}
	slog.Info("loaded glossary", "terms", len(glossary.Terms))

	// Load characters (optional — may be empty).
	characters, err := translation.LoadCharacters(proj.AIDir())
	if err != nil {
		slog.Warn("failed to load characters, continuing with glossary only", "error", err)
		characters = &translation.Characters{}
	}
	slog.Info("loaded characters", "count", len(characters.Characters))

	// Build the list of Russian terms to pronounce. Characters are included
	// as type "character"; glossary terms use their stored type.
	var inputs []translation.PronunciationInput
	seen := make(map[string]bool) // dedupe by Russian text (case-insensitive)
	for _, c := range characters.Characters {
		if c.Translation == "" {
			continue
		}
		key := strings.ToLower(c.Translation)
		if seen[key] {
			continue
		}
		seen[key] = true
		inputs = append(inputs, translation.PronunciationInput{
			Russian: c.Translation,
			Source:  c.Name,
			Type:    "character",
		})
	}
	for _, t := range glossary.Terms {
		if t.Target == "" {
			continue
		}
		key := strings.ToLower(t.Target)
		if seen[key] {
			continue
		}
		seen[key] = true
		inputs = append(inputs, translation.PronunciationInput{
			Russian: t.Target,
			Source:  t.Source,
			Type:    t.Type,
		})
	}
	slog.Info("terms to pronounce", "count", len(inputs), "deduped_from", len(glossary.Terms)+len(characters.Characters))

	// Create the OpenAI client.
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}
	slog.Info("openai client ready", "helper_model", client.HelperModel())

	// Call the model for pronunciation extraction.
	slog.Info("calling model for pronunciation extraction", "model", client.HelperModel())
	resp, err := client.Chat(ctx, translation.ChatRequest{
		System:   translation.PronunciationSystem,
		User:     translation.PronunciationUser(inputs),
		Model:    client.HelperModel(),
		JSONMode: true,
	})
	if err != nil {
		return err
	}
	slog.Info("pronunciation extraction complete",
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens)

	// Parse the JSON response.
	var result pronunciationResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return fmt.Errorf("parse pronunciation JSON: %w (content: %s)", err, truncate(resp.Content, 200))
	}
	slog.Info("extracted pronunciation hints", "entries", len(result.Entries))

	// Build and save the pronunciation store.
	pron := &tts.Pronunciation{Entries: result.Entries}
	if err := pron.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved pronunciation hints", "path", pronPath)

	return nil
}

// pronunciationResult is the JSON shape we expect from the model.
type pronunciationResult struct {
	Entries []tts.PronunciationEntry `json:"entries"`
}
