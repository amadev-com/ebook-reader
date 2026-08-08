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

func TestNewSileroEngine_RequiresServerURL(t *testing.T) {
	_, err := NewSileroEngine(EngineConfig{
		Engine: "silero-http",
		Voice:  "silero:v5_5_ru#xenia",
	})
	if err == nil {
		t.Fatal("expected error when server_url is empty")
	}
}

func TestNewSileroEngine_RequiresVoice(t *testing.T) {
	_, err := NewSileroEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: "http://localhost:5555",
	})
	if err == nil {
		t.Fatal("expected error when voice is empty")
	}
}

func TestNewSileroEngine_Success(t *testing.T) {
	engine, err := NewSileroEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: "http://localhost:5555",
		Voice:     "silero:v5_5_ru#xenia",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	if engine.Name() != "silero-http" {
		t.Errorf("name: got %q", engine.Name())
	}
}

func TestSileroEngine_Synthesize(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tts" {
			http.NotFound(w, r)
			return
		}
		var body sileroRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Text == "" {
			http.Error(w, "empty text", http.StatusBadRequest)
			return
		}
		if !body.SSML {
			http.Error(w, "ssml flag should be true", http.StatusBadRequest)
			return
		}
		if body.Voice == "" {
			http.Error(w, "voice required", http.StatusBadRequest)
			return
		}
		// Return a minimal WAV header (44 bytes).
		wav := make([]byte, 44)
		copy(wav[0:4], []byte("RIFF"))
		wav[4] = 36
		copy(wav[8:12], []byte("WAVE"))
		_, _ = w.Write(wav)
	}))
	defer ts.Close()

	engine, err := NewSileroEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: ts.URL,
		Voice:     "silero:v5_5_ru#xenia",
		Language:  "ru",
		Speed:     1.0,
		Pitch:     1.0,
		SampleRate: 48000,
	})
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
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

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data[:4]) != "RIFF" {
		t.Errorf("not a WAV: got %q", data[:4])
	}
}

func TestSileroEngine_PassesSSMLThrough(t *testing.T) {
	var receivedText string
	var receivedSSML bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sileroRequestBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		receivedText = body.Text
		receivedSSML = body.SSML
		wav := make([]byte, 44)
		copy(wav[0:4], []byte("RIFF"))
		copy(wav[8:12], []byte("WAVE"))
		_, _ = w.Write(wav)
	}))
	defer ts.Close()

	engine, err := NewSileroEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: ts.URL,
		Voice:     "silero:v5_5_ru#xenia",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}

	tmpDir := t.TempDir()
	text := "<speak><p><s>Привет мир.</s></p></speak>"
	err = engine.Synthesize(context.Background(), text, filepath.Join(tmpDir, "out.wav"))
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if receivedText != text {
		t.Errorf("server received %q, want %q", receivedText, text)
	}
	if !receivedSSML {
		t.Error("ssml flag should be true")
	}
}

func TestSileroEngine_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	defer ts.Close()

	engine, err := NewSileroEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: ts.URL,
		Voice:     "silero:v5_5_ru#xenia",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}

	err = engine.Synthesize(context.Background(), "test text", "/tmp/out.wav")
	if err == nil {
		t.Fatal("expected error on server 500")
	}
}

func TestSileroEngine_CheckHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	engine, err := NewSileroEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: ts.URL,
		Voice:     "silero:v5_5_ru#xenia",
		Language:  "ru",
	})
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}

	sileroEngine, ok := engine.(*SileroEngine)
	if !ok {
		t.Fatalf("expected *SileroEngine, got %T", engine)
	}
	if err := sileroEngine.CheckHealth(context.Background()); err != nil {
		t.Errorf("CheckHealth: %v", err)
	}
}

func TestNewEngine_Silero(t *testing.T) {
	engine, err := NewEngine(EngineConfig{
		Engine:    "silero-http",
		ServerURL: "http://localhost:9999",
		Voice:     "silero:v5_5_ru#xenia",
	})
	if err != nil {
		t.Fatalf("NewEngine silero-http: %v", err)
	}
	if engine.Name() != "silero-http" {
		t.Errorf("engine name: got %q", engine.Name())
	}
}
