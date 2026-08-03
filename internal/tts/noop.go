package tts

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"time"
)

// NoopEngine is a default Engine that writes a minimal valid WAV file
// containing a short tone. It requires no external dependencies and is used
// when no real TTS engine is configured, allowing the full pipeline
// (preprocess → audio) to be tested end-to-end without a speech synthesis
// backend.
type NoopEngine struct {
	cfg EngineConfig
}

// NewNoopEngine constructs a NoopEngine from the given config. The config is
// accepted but largely ignored — the noop engine needs no model or runtime.
func NewNoopEngine(cfg EngineConfig) (Engine, error) {
	return &NoopEngine{cfg: cfg}, nil
}

// Name returns "noop".
func (e *NoopEngine) Name() string { return "noop" }

// Synthesize writes a short sine-tone WAV to outPath. The tone duration scales
// mildly with input length so that downstream merge logic has varied file
// sizes to work with. The text is not parsed — this engine exists for
// pipeline testing only.
func (e *NoopEngine) Synthesize(_ context.Context, text string, outPath string) error {
	duration := noopDuration(len(text))
	samples := noopSineWave(220.0, duration, 16000)
	return writeWAV(outPath, samples, 16000)
}

// noopDuration returns a duration between 0.5s and 3s based on text length,
// so noop audio files vary in size for merge testing.
func noopDuration(textLen int) time.Duration {
	if textLen < 0 {
		textLen = 0
	}
	secs := 0.5 + float64(textLen)/2000.0
	if secs > 3.0 {
		secs = 3.0
	}
	return time.Duration(secs * float64(time.Second))
}

// noopSineWave generates a mono 16-bit PCM sine wave.
func noopSineWave(freq float64, duration time.Duration, sampleRate int) []int16 {
	numSamples := int(duration.Seconds() * float64(sampleRate))
	samples := make([]int16, numSamples)
	for i := range samples {
		t := float64(i) / float64(sampleRate)
		val := math.Sin(2*math.Pi*freq*t) * 0.2 * float64(math.MaxInt16)
		samples[i] = int16(val)
	}
	return samples
}

// writeWAV writes a minimal 16-bit mono PCM WAV file.
func writeWAV(path string, samples []int16, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	dataSize := len(samples) * 2 // 16-bit = 2 bytes per sample
	// RIFF header
	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(36+dataSize)); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}
	// fmt chunk
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil { // chunk size
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // PCM
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // mono
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate*2)); err != nil { // byte rate
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(2)); err != nil { // block align
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(16)); err != nil { // bits per sample
		return err
	}
	// data chunk
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(dataSize)); err != nil {
		return err
	}
	for _, s := range samples {
		if err := binary.Write(f, binary.LittleEndian, s); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	Register("noop", NewNoopEngine)
}
