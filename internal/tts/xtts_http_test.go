package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewHTTPEngine_RequiresServerURL(t *testing.T) {
	_, err := NewHTTPEngine(EngineConfig{Engine: "xtts-http"})
	if err == nil {
		t.Fatal("expected error when server_url is empty")
	}
}

func TestNewHTTPEngine_Success(t *testing.T) {
	engine, err := NewHTTPEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: "http://localhost:8020",
		Speaker:   "test",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewHTTPEngine: %v", err)
	}
	if engine.Name() != "xtts-http" {
		t.Errorf("name: got %q", engine.Name())
	}
}

func TestHTTPEngine_Synthesize(t *testing.T) {
	// Create a test server that returns a minimal WAV.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tts" {
			http.NotFound(w, r)
			return
		}
		// Verify the request body.
		var body ttsRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Text == "" {
			http.Error(w, "empty text", http.StatusBadRequest)
			return
		}
		// Return a minimal WAV header (44 bytes).
		wav := make([]byte, 44)
		copy(wav[0:4], []byte("RIFF"))
		wav[4] = 36 // file size - 8
		copy(wav[8:12], []byte("WAVE"))
		_, _ = w.Write(wav)
	}))
	defer ts.Close()

	engine, err := NewHTTPEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: ts.URL,
		Speaker:   "test",
		Language:  "ru",
		Speed:     1.0,
	})
	if err != nil {
		t.Fatalf("NewHTTPEngine: %v", err)
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "output.wav")

	err = engine.Synthesize(context.Background(),
		"<speak><p><s>Привет мир.</s></p></speak>", outPath)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if info.Size() < 44 {
		t.Errorf("output too small: %d bytes", info.Size())
	}

	// Verify RIFF header.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data[:4]) != "RIFF" {
		t.Errorf("not a WAV: got %q", data[:4])
	}
}

func TestHTTPEngine_ExtractsPlainTextFromSSML(t *testing.T) {
	var receivedText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ttsRequestBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		receivedText = body.Text
		wav := make([]byte, 44)
		copy(wav[0:4], []byte("RIFF"))
		copy(wav[8:12], []byte("WAVE"))
		_, _ = w.Write(wav)
	}))
	defer ts.Close()

	engine, err := NewHTTPEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: ts.URL,
		Speaker:   "test",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewHTTPEngine: %v", err)
	}

	tmpDir := t.TempDir()
	ssml := `<speak><p><s>Привет <phoneme alphabet="ipa" ph="test">мир</phoneme>.</s></p></speak>`
	err = engine.Synthesize(context.Background(), ssml, filepath.Join(tmpDir, "out.wav"))
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if receivedText != "Привет мир." {
		t.Errorf("server received %q, want %q", receivedText, "Привет мир.")
	}
}

func TestHTTPEngine_RequiresSpeaker(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer ts.Close()

	engine, err := NewHTTPEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: ts.URL,
		Language:  "ru",
		// No Speaker or VoiceSample
	})
	if err != nil {
		t.Fatalf("NewHTTPEngine: %v", err)
	}

	err = engine.Synthesize(context.Background(), "test text", "/tmp/out.wav")
	if err == nil {
		t.Fatal("expected error when no speaker configured")
	}
}

func TestHTTPEngine_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	defer ts.Close()

	engine, err := NewHTTPEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: ts.URL,
		Speaker:   "test",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewHTTPEngine: %v", err)
	}

	err = engine.Synthesize(context.Background(), "test text", "/tmp/out.wav")
	if err == nil {
		t.Fatal("expected error on server 500")
	}
}

func TestHTTPEngine_CheckHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	engine, err := NewHTTPEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: ts.URL,
		Speaker:   "test",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewHTTPEngine: %v", err)
	}

	httpEngine, ok := engine.(*HTTPEngine)
	if !ok {
		t.Fatalf("expected *HTTPEngine, got %T", engine)
	}
	if err := httpEngine.CheckHealth(context.Background()); err != nil {
		t.Errorf("CheckHealth: %v", err)
	}
}

func TestNewEngine_HTTP(t *testing.T) {
	engine, err := NewEngine(EngineConfig{
		Engine:    "xtts-http",
		ServerURL: "http://localhost:9999",
		Speaker:   "test",
	})
	if err != nil {
		t.Fatalf("NewEngine xtts-http: %v", err)
	}
	if engine.Name() != "xtts-http" {
		t.Errorf("engine name: got %q", engine.Name())
	}
}

func TestAvailableEngines_IncludesHTTP(t *testing.T) {
	engines := AvailableEngines()
	if !contains(engines, "noop") {
		t.Errorf("noop should be available: %s", engines)
	}
	if !contains(engines, "xtts-http") {
		t.Errorf("xtts-http should be available: %s", engines)
	}
}
