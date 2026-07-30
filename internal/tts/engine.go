// Package tts implements the text-to-speech stage of the bookai pipeline.
//
// The package is built around the Engine interface, which abstracts the actual
// speech synthesis backend. This lets the pipeline structure (SSML generation,
// audio output, ffmpeg post-processing) be built and tested independently of
// any specific TTS engine. Concrete engines (sherpa-onnx, piper, xtts-v2, etc.)
// are registered via Register and selected by name from config.
package tts

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Engine synthesizes speech from SSML input. Implementations may be local
// (subprocess, ONNX runtime) or remote (HTTP API). The interface is
// intentionally minimal: the caller provides SSML text and an output path; the
// engine writes a WAV file to that path.
type Engine interface {
	// Name returns the engine identifier (e.g. "noop", "sherpa-onnx").
	Name() string

	// Synthesize converts ssmlText into speech and writes a WAV file to
	// outPath. The SSML is a W3C SSML subset (see ssml.go); engines that do
	// not support SSML natively should use ExtractPlainText to get the raw
	// text. outPath's parent directory is guaranteed to exist by the caller.
	Synthesize(ctx context.Context, ssmlText string, outPath string) error
}

// EngineFactory constructs an Engine from the TTS configuration. Factories are
// registered at init time by each engine implementation. Returning an error
// (e.g. missing model path, unavailable runtime) lets the CLI surface a clear
// message before any synthesis is attempted.
type EngineFactory func(cfg EngineConfig) (Engine, error)

// EngineConfig is the configuration passed to an EngineFactory. It is derived
// from config.TTS by the CLI layer, with absolute paths resolved.
type EngineConfig struct {
	// Engine is the engine name (matches the factory key).
	Engine string

	// Language is the target language code (e.g. "ru").
	Language string

	// VoiceSample is an optional path to a reference voice WAV (for
	// voice-cloning engines). May be empty.
	VoiceSample string

	// ModelPath is an optional path to the ONNX/model file. May be empty
	// (engines that require it will error).
	ModelPath string

	// DataDir is an optional path to phoneme/espeak data directory.
	DataDir string

	// TokensPath is an optional path to a tokens file.
	TokensPath string

	// Python is the Python interpreter path (for subprocess-based engines).
	Python string

	// Device selects the compute device: "cpu", "cuda", etc. Empty = engine
	// default.
	Device string

	// Speed is a playback speed multiplier (1.0 = normal). Engines may
	// ignore this.
	Speed float64

	// ServerURL is the HTTP endpoint for remote TTS engines (e.g.
	// "http://localhost:8020"). Used by the xtts-http engine.
	ServerURL string

	// Speaker is the voice name for voice-cloning engines. It matches a
	// file in the server's speakers directory (without the .wav extension).
	Speaker string
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]EngineFactory)
)

// Register adds an EngineFactory under the given name. Called by engine
// implementations in init(). Panics if the name is already registered (a
// programming error, not a runtime condition).
func Register(name string, factory EngineFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("tts: engine %q already registered", name))
	}
	registry[name] = factory
}

// NewEngine looks up the registered factory for cfg.Engine and constructs an
// Engine. Returns a descriptive error if the engine is unknown or
// initialization fails.
func NewEngine(cfg EngineConfig) (Engine, error) {
	registryMu.RLock()
	factory, ok := registry[cfg.Engine]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tts: unknown engine %q (available: %s)", cfg.Engine, AvailableEngines())
	}
	engine, err := factory(cfg)
	if err != nil {
		return nil, fmt.Errorf("tts: init engine %q: %w", cfg.Engine, err)
	}
	return engine, nil
}

// AvailableEngines returns the sorted list of registered engine names.
func AvailableEngines() string {
	registryMu.RLock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	registryMu.RUnlock()
	sort.Strings(names)
	return joinNames(names)
}

// joinNames formats engine names as a comma-separated list for error messages.
func joinNames(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	result := ""
	for i, n := range names {
		if i > 0 {
			result += ", "
		}
		result += n
	}
	return result
}
