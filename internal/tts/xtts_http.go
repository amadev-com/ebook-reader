package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HTTPEngine is an Engine that synthesizes speech via a remote HTTP API.
// It is designed to work with the XTTS v2 FastAPI server in tts-server/
// (docker-compose.yml), but the protocol is simple enough to work with
// any server that exposes the same /tts endpoint.
//
// The engine sends SSML text, but first extracts plain text via
// ExtractPlainText since the XTTS v2 server does not parse SSML natively.
// Pronunciation hints are therefore not passed to the TTS engine — they
// are used only for SSML generation (which may be consumed by other engines
// or tools). A future enhancement could send phoneme hints to the server.
type HTTPEngine struct {
	cfg      EngineConfig
	client   *http.Client
	serverURL string
}

// NewHTTPEngine constructs an HTTPEngine from the given config. The
// ServerURL field is required. The engine uses a 10-minute timeout to
// accommodate long chapter synthesis on first model load.
func NewHTTPEngine(cfg EngineConfig) (Engine, error) {
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("tts.http: server_url is required")
	}
	serverURL := strings.TrimRight(cfg.ServerURL, "/")
	return &HTTPEngine{
		cfg:       cfg,
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}, nil
}

// Name returns "xtts-http".
func (e *HTTPEngine) Name() string { return "xtts-http" }

// ttsRequestBody is the JSON body sent to the /tts endpoint.
type ttsRequestBody struct {
	Text         string  `json:"text"`
	Language     string  `json:"language"`
	SpeakerWav   string  `json:"speaker_wav,omitempty"`
	SpeakerWavPath string `json:"speaker_wav_path,omitempty"`
	Speed        float64 `json:"speed"`
}

// Synthesize sends the SSML text (as plain text) to the HTTP TTS server
// and writes the returned WAV audio to outPath.
func (e *HTTPEngine) Synthesize(ctx context.Context, ssmlText string, outPath string) error {
	// XTTS v2 doesn't parse SSML — extract plain text.
	text := ExtractPlainText(ssmlText)
	if text == "" {
		return fmt.Errorf("tts.http: no text to synthesize after SSML extraction")
	}

	// Determine the speaker. Prefer the Speaker config field (name in the
	// server's speakers dir). Fall back to VoiceSample (absolute path).
	body := ttsRequestBody{
		Text:     text,
		Language: e.cfg.Language,
		Speed:    e.cfg.Speed,
	}
	if e.cfg.Speaker != "" {
		body.SpeakerWav = e.cfg.Speaker
	} else if e.cfg.VoiceSample != "" {
		body.SpeakerWavPath = e.cfg.VoiceSample
	} else {
		return fmt.Errorf("tts.http: either speaker or voice_sample must be configured")
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("tts.http: marshal request: %w", err)
	}

	url := e.serverURL + "/tts"
	slog.Debug("tts.http: sending synthesis request",
		"url", url, "text_len", len(text), "language", body.Language)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("tts.http: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("tts.http: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tts.http: server returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Ensure the output directory exists.
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("tts.http: create output dir: %w", err)
	}

	// Write the response body (WAV audio) to the output file.
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("tts.http: create output file: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("tts.http: write audio: %w", err)
	}

	slog.Debug("tts.http: synthesis complete", "output", outPath)
	return nil
}

// CheckHealth pings the server's /health endpoint. Returns nil if the
// server is reachable and responsive. Useful for startup diagnostics.
func (e *HTTPEngine) CheckHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.serverURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("tts.http: create health request: %w", err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("tts.http: health check failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tts.http: health check returned %d", resp.StatusCode)
	}
	return nil
}

func init() {
	Register("xtts-http", NewHTTPEngine)
}
