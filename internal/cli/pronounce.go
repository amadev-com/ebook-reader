package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"ebook-reader/internal/config"
	"ebook-reader/internal/project"
	"ebook-reader/internal/translation"
	"ebook-reader/internal/tts"
)

// newPronounceCmd implements `bookai pronounce`: reads ai/glossary.json and
// ai/characters.json, asks the helper model for XTTS v2-compatible phonetic
// respellings of the Russian terms, and writes ai/respelling.json. This file
// is consumed by `bookai preprocess` to replace terms in translation text
// before TTS synthesis.
func newPronounceCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "pronounce",
		Short: "Generate XTTS v2 phonetic respellings for glossary terms (ai/respelling.json)",
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
	cmd.Flags().BoolVar(&force, "force", false, "re-generate even if respelling.json already exists")
	return cmd
}

// runPronounce loads the glossary and characters, asks the model for XTTS v2
// phonetic respellings, and writes ai/respelling.json.
func runPronounce(ctx context.Context, proj *project.Project, force bool) error {
	respellingPath := proj.AIDir() + "/respelling.json"
	if project.Exists(respellingPath) && !force {
		slog.Info("respelling.json already exists, skipping (use --force to re-generate)", "path", respellingPath)
		return nil
	}

	// Load glossary (terms only — no characters, they're stored separately).
	glossary, err := translation.LoadGlossary(proj.AIDir())
	if err != nil {
		return fmt.Errorf("load glossary: %w (run `bookai analyze` first)", err)
	}
	slog.Info("loaded glossary", "terms", len(glossary.Terms))

	// Load characters (separate file, no duplication with glossary).
	characters, err := translation.LoadCharacters(proj.AIDir())
	if err != nil {
		slog.Warn("failed to load characters, continuing with glossary only", "error", err)
		characters = &translation.Characters{}
	}
	slog.Info("loaded characters", "count", len(characters.Characters))

	// Merge characters into glossary at runtime.
	merged := glossary.WithCharacters(characters)
	if len(merged.Terms) == 0 {
		return fmt.Errorf("glossary and characters are empty — run `bookai analyze` first")
	}

	// Build the list of Russian terms to respell.
	var inputs []translation.PronunciationInput
	for _, t := range merged.Terms {
		if t.Target == "" {
			continue
		}
		inputs = append(inputs, translation.PronunciationInput{
			Russian: t.Target,
			Source:  t.Source,
			Type:    t.Type,
		})
	}
	slog.Info("terms to respell", "count", len(inputs))

	// Create the OpenAI client.
	client, err := translation.NewClient(proj.Cfg.OpenAI)
	if err != nil {
		return err
	}
	slog.Info("openai client ready", "helper_model", client.HelperModel())

	// Call the model for respelling generation.
	slog.Info("calling model for respelling generation", "model", client.HelperModel())
	resp, err := client.Chat(ctx, translation.ChatRequest{
		System:   translation.RespellingSystem,
		User:     translation.RespellingUser(inputs),
		Model:    client.HelperModel(),
		JSONMode: true,
	})
	if err != nil {
		return err
	}
	slog.Info("respelling generation complete",
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens)

	// Parse the JSON response.
	var result respellingResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return fmt.Errorf("parse respelling JSON: %w (content: %s)", err, truncate(resp.Content, 200))
	}
	slog.Info("extracted respellings", "entries", len(result.Entries))

	// Apply config overrides.
	applyRespellingOverrides(&result, proj.Cfg.Pronunciation)

	// Build and save the respelling store.
	re := &tts.Respelling{Entries: result.Entries}
	if err := re.Save(proj.AIDir()); err != nil {
		return err
	}
	slog.Info("saved respellings", "path", respellingPath, "entries", len(re.Entries))

	return nil
}

// respellingResult is the JSON shape we expect from the model.
type respellingResult struct {
	Entries []tts.RespellingEntry `json:"entries"`
}

// applyRespellingOverrides forces the respelling of any entry that matches a
// config pronunciation override. Config entries not found by the model are
// added as new entries. The config PronunciationOverride struct is reused —
// the Phonemes field is used as the respelled text.
func applyRespellingOverrides(result *respellingResult, overrides []config.PronunciationOverride) {
	if len(overrides) == 0 {
		return
	}
	overridden := 0
	for i := range result.Entries {
		for _, ov := range overrides {
			if strings.EqualFold(result.Entries[i].Term, ov.Term) {
				result.Entries[i].Respelled = ov.Phonemes
				overridden++
				break
			}
		}
	}
	for _, ov := range overrides {
		found := false
		for _, e := range result.Entries {
			if strings.EqualFold(e.Term, ov.Term) {
				found = true
				break
			}
		}
		if !found && ov.Term != "" && ov.Phonemes != "" {
			result.Entries = append(result.Entries, tts.RespellingEntry{
				Term:      ov.Term,
				Respelled: ov.Phonemes,
			})
			overridden++
		}
	}
	if overridden > 0 {
		slog.Info("applied respelling overrides", "overridden", overridden, "config_entries", len(overrides))
	}
}
