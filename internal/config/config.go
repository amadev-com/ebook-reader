// Package config loads and validates the bookai project configuration from
// config.yaml. Configuration is intentionally minimal: secrets (the OpenAI
// API key) are read from the environment, never from the file.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the in-memory representation of config.yaml.
type Config struct {
	// Project is the human-readable project name. Defaults to the project
	// directory's base name.
	Project string `yaml:"project"`

	// Languages configures the translation direction.
	Languages Languages `yaml:"languages"`

	// OpenAI holds model selection and endpoint configuration. The API key
	// itself is NOT stored here; it is read from OPENAI_API_KEY at runtime.
	OpenAI OpenAI `yaml:"openai"`

	// TTS holds text-to-speech configuration. Used in M3+. The engine is
	// swappable: "noop" (default, for pipeline testing), or any registered
	// engine name (e.g. "sherpa-onnx", "piper", "xtts-v2").
	TTS TTS `yaml:"tts"`

	// Paths allows overriding the default project subdirectory layout. Any
	// empty field falls back to the default relative path under the project
	// root.
	Paths Paths `yaml:"paths"`

	// Glossary holds initial/override values for characters and terms. These
	// are user-specified translations that take precedence over AI-extracted
	// ones — useful for locking down how specific names or terms should be
	// translated. The AI still fills in other fields (role, description, type)
	// from context, but the translation is always overridden.
	Glossary GlossaryOverrides `yaml:"glossary"`

	// Pronunciation holds user-specified respelling overrides for the TTS
	// preprocessing stage. These take precedence over AI-generated respellings
	// — useful for fixing mispronunciations without re-running the model.
	// The Phonemes field is used as the respelled text (plain Russian).
	Pronunciation []PronunciationOverride `yaml:"pronunciation"`

	// Chapters holds settings for the analyze-chapters stage.
	Chapters Chapters `yaml:"chapters"`
}

// Chapters holds settings for the analyze-chapters stage.
type Chapters struct {
	// Strip is a list of default --strip trigger strings applied to every
	// analyze-chapters run. Additional --strip flags on the command line are
	// appended to these. Each string is a trigger: if it appears in the last
	// 400 chars of a chapter, everything from the last "***" separator before
	// the trigger to the end is removed.
	Strip []string `yaml:"strip"`
}

// GlossaryOverrides holds user-specified translation overrides.
type GlossaryOverrides struct {
	// Characters is a list of source names with locked Russian translations.
	// The AI extracts role/description from context, but the translation
	// field is always set to the value specified here.
	Characters []GlossaryOverride `yaml:"characters"`

	// Terms is a list of source terms with locked Russian translations.
	// The AI extracts type from context, but the target field is always
	// set to the value specified here.
	Terms []GlossaryOverride `yaml:"terms"`
}

// GlossaryOverride is one user-specified translation override.
type GlossaryOverride struct {
	Source string `yaml:"source"` // English term as it appears in the text
	Target string `yaml:"target"` // locked Russian translation
	Type   string `yaml:"type"`   // optional: character|place|organization|title|term
}

// PronunciationOverride is one user-specified respelling override. The Term
// field matches the Russian text as it appears in the translation; Phonemes
// is the phonetic respelling that XTTS v2 should pronounce (plain Russian
// text, not IPA).
type PronunciationOverride struct {
	Term     string `yaml:"term"`     // Russian text as it appears in translation
	Phonemes string `yaml:"phonemes"` // phonetic respelling for XTTS v2, e.g. "КУинн"
	Alphabet string `yaml:"alphabet"` // unused (kept for backward compat)
}

// Languages is the translation direction.
type Languages struct {
	Source string `yaml:"source"` // e.g. "en"
	Target string `yaml:"target"` // e.g. "ru"
}

// OpenAI is the model/endpoint configuration. API key is in the env.
type OpenAI struct {
	BaseURL          string `yaml:"base_url"`          // empty = OpenAI default
	TranslationModel string `yaml:"translation_model"` // gpt-4.1
	HelperModel      string `yaml:"helper_model"`      // gpt-4.1-mini (glossary, summaries)
	MaxRetries       int    `yaml:"max_retries"`
}

// TTS is text-to-speech configuration (M3+). The Engine field selects which
// registered TTS backend to use. Engine-specific paths (model, data dir,
// tokens) are resolved by the CLI layer into an tts.EngineConfig.
type TTS struct {
	Engine       string  `yaml:"engine"`        // "noop" (default), "xtts-http", "sherpa-onnx", "piper", ...
	VoiceSample  string  `yaml:"voice_sample"`  // path to a short reference wav (voice-cloning engines)
	Language     string  `yaml:"language"`      // target language code, e.g. "ru"
	Python       string  `yaml:"python"`        // python interpreter (subprocess engines), default "python3"
	ModelPath    string  `yaml:"model_path"`    // path to ONNX/model file
	DataDir      string  `yaml:"data_dir"`      // path to espeak-ng-data / phoneme data
	TokensPath   string  `yaml:"tokens_path"`   // path to tokens file
	Device       string  `yaml:"device"`        // "cpu" (default), "cuda", etc.
	Speed        float64 `yaml:"speed"`         // playback speed multiplier, 1.0 = normal
	ServerURL    string  `yaml:"server_url"`    // HTTP endpoint for remote TTS engines, e.g. "http://localhost:8020"
	Speaker      string  `yaml:"speaker"`       // speaker name for voice-cloning engines (matches a file in the server's speakers dir)
	AudioFormat  string  `yaml:"audio_format"`  // output format: "mp3" (default) or "wav"
	AudioBitrate string  `yaml:"audio_bitrate"` // MP3 bitrate, e.g. "128k", "192k" (default "128k")
}

// Paths overrides default project subdirectory names.
type Paths struct {
	Source      string `yaml:"source"`
	Extracted   string `yaml:"extracted"`
	Chapters    string `yaml:"chapters"`
	AI          string `yaml:"ai"`
	Translation string `yaml:"translation"`
	Memory      string `yaml:"memory"`
	TTS         string `yaml:"tts"`
	Audio       string `yaml:"audio"`
}

// Default returns a Config populated with sensible defaults for an
// English-to-Russian pipeline using gpt-4.1.
func Default(projectName string) Config {
	if projectName == "" {
		projectName = "book"
	}
	return Config{
		Project: projectName,
		Languages: Languages{
			Source: "en",
			Target: "ru",
		},
		OpenAI: OpenAI{
			BaseURL:          "",
			TranslationModel: "gpt-5.6-luna",
			HelperModel:      "gpt-5.6-luna",
			MaxRetries:       3,
		},
		TTS: TTS{
			Engine:       "noop",
			Language:     "ru",
			Python:       "python3",
			Speed:        1.0,
			AudioFormat:  "mp3",
			AudioBitrate: "128k",
		},
		Paths: Paths{
			Source:      "source",
			Extracted:   "extracted",
			Chapters:    "chapters",
			AI:          "ai",
			Translation: "translation",
			Memory:      "memory",
			TTS:         "tts",
			Audio:       "audio",
		},
	}
}

// Load reads config.yaml from projectRoot. If the file does not exist, a
// Default config is returned (the project is treated as freshly initialized).
// If it exists but is malformed, an error is returned.
func Load(projectRoot string) (Config, error) {
	cfg := Default(filepath.Base(projectRoot))
	path := filepath.Join(projectRoot, "config.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config to <projectRoot>/config.yaml with stable formatting.
func Save(projectRoot string, cfg Config) error {
	path := filepath.Join(projectRoot, "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func (c *Config) validate() error {
	if c.Languages.Source == "" {
		return fmt.Errorf("languages.source must not be empty")
	}
	if c.Languages.Target == "" {
		return fmt.Errorf("languages.target must not be empty")
	}
	if c.OpenAI.TranslationModel == "" {
		return fmt.Errorf("openai.translation_model must not be empty")
	}
	if c.OpenAI.HelperModel == "" {
		return fmt.Errorf("openai.helper_model must not be empty")
	}
	return nil
}
