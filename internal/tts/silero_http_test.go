package tts

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// makeWAV creates a minimal valid WAV file with the given PCM data size.
func makeWAV(dataSize int) []byte {
	wav := make([]byte, 44+dataSize)
	copy(wav[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(wav[4:8], uint32(36+dataSize))
	copy(wav[8:12], []byte("WAVE"))
	copy(wav[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(wav[22:24], 1) // mono
	binary.LittleEndian.PutUint32(wav[24:28], 48000)
	binary.LittleEndian.PutUint32(wav[28:32], 48000)
	binary.LittleEndian.PutUint16(wav[32:34], 2)  // block align
	binary.LittleEndian.PutUint16(wav[34:36], 16) // bits per sample
	copy(wav[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(wav[40:44], uint32(dataSize))
	return wav
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
		_, _ = w.Write(makeWAV(100))
	}))
	defer ts.Close()

	engine, err := NewSileroEngine(EngineConfig{
		Engine:     "silero-http",
		ServerURL:  ts.URL,
		Voice:      "silero:v5_5_ru#xenia",
		Language:   "ru",
		Speed:      1.0,
		Pitch:      1.0,
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
		_, _ = w.Write(makeWAV(50))
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

	if !strings.HasPrefix(receivedText, "<speak>") {
		t.Errorf("server received non-SSML: %q", receivedText)
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
			_, _ = w.Write([]byte(`{"status":"ok"}`))
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

// --- Chunking tests ---

func TestSplitSSML_ShortText(t *testing.T) {
	ssml := "<speak><p><s>Привет мир.</s></p></speak>"
	chunks := splitSSML(ssml, 900)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d: %v", len(chunks), chunks)
	}
}

func TestSplitSSML_LongText(t *testing.T) {
	// Build SSML with many sentences to exceed 900 chars.
	var sentences []string
	for i := 0; i < 50; i++ {
		sentences = append(sentences, "<s>Это тестовое предложение номер "+string(rune('A'+i%26))+".</s>")
	}
	ssml := "<speak><p>" + strings.Join(sentences, "") + "</p></speak>"
	chunks := splitSSML(ssml, 900)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if len(chunk) > 900 {
			t.Errorf("chunk %d exceeds maxLen: %d bytes", i, len(chunk))
		}
		if !strings.HasPrefix(chunk, "<speak>") {
			t.Errorf("chunk %d missing <speak> prefix: %q", i, chunk[:20])
		}
		if !strings.HasSuffix(chunk, "</speak>") {
			t.Errorf("chunk %d missing </speak> suffix", i)
		}
	}
}

func TestSplitSSML_PreservesSentenceTags(t *testing.T) {
	ssml := "<speak><p><s>Первое.</s><s>Второе.</s></p></speak>"
	chunks := splitSSML(ssml, 900)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0], "<s>Первое.</s>") {
		t.Errorf("sentence tag lost: %s", chunks[0])
	}
	if !strings.Contains(chunks[0], "<s>Второе.</s>") {
		t.Errorf("sentence tag lost: %s", chunks[0])
	}
}

func TestSplitSSML_SingleSentenceTooLong(t *testing.T) {
	// Build a single very long sentence.
	words := make([]string, 200)
	for i := range words {
		words[i] = "слово"
	}
	longInner := strings.Join(words, " ")
	ssml := "<speak><p><s>" + longInner + "</s></p></speak>"
	chunks := splitSSML(ssml, 900)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long sentence, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) > 900 {
			t.Errorf("chunk %d exceeds maxLen: %d bytes", i, len(chunk))
		}
	}
}

func TestSplitSSML_NoSentenceTags(t *testing.T) {
	ssml := "<speak>Просто текст без тегов предложений.</speak>"
	chunks := splitSSML(ssml, 900)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for text without <s> tags, got %d", len(chunks))
	}
}

// --- WAV concatenation tests ---

func TestConcatWAVs_Single(t *testing.T) {
	wav := makeWAV(100)
	combined, err := concatWAVs([][]byte{wav})
	if err != nil {
		t.Fatalf("concatWAVs: %v", err)
	}
	if len(combined) != len(wav) {
		t.Errorf("single WAV should pass through: got %d bytes, want %d", len(combined), len(wav))
	}
}

func TestConcatWAVs_Multiple(t *testing.T) {
	wav1 := makeWAV(100)
	wav2 := makeWAV(200)
	wav3 := makeWAV(50)

	combined, err := concatWAVs([][]byte{wav1, wav2, wav3})
	if err != nil {
		t.Fatalf("concatWAVs: %v", err)
	}

	// Expected: header (44 bytes) + 100 + 200 + 50 = 394 bytes
	expectedDataSize := 100 + 200 + 50
	expectedTotal := 44 + expectedDataSize
	if len(combined) != expectedTotal {
		t.Errorf("combined size: got %d, want %d", len(combined), expectedTotal)
	}

	// Check RIFF size.
	riffSize := binary.LittleEndian.Uint32(combined[4:8])
	if riffSize != uint32(expectedTotal-8) {
		t.Errorf("RIFF size: got %d, want %d", riffSize, expectedTotal-8)
	}

	// Check data size.
	dataChunkIdx := strings.Index(string(combined), "data")
	dataSize := binary.LittleEndian.Uint32(combined[dataChunkIdx+4 : dataChunkIdx+8])
	if dataSize != uint32(expectedDataSize) {
		t.Errorf("data size: got %d, want %d", dataSize, expectedDataSize)
	}

	// Check it's a valid WAV.
	if string(combined[0:4]) != "RIFF" {
		t.Error("missing RIFF header")
	}
	if string(combined[8:12]) != "WAVE" {
		t.Error("missing WAVE")
	}
}

func TestConcatWAVs_Empty(t *testing.T) {
	_, err := concatWAVs(nil)
	if err == nil {
		t.Fatal("expected error for no WAV data")
	}
}

func TestConcatWAVs_InvalidWAV(t *testing.T) {
	_, err := concatWAVs([][]byte{[]byte("not a wav"), makeWAV(10)})
	if err == nil {
		t.Fatal("expected error for invalid WAV")
	}
}

func TestExtractSentences(t *testing.T) {
	ssml := "<speak><p><s>Первое.</s><s>Второе.</s></p><p><s>Третье.</s></p></speak>"
	sentences := extractSentences(ssml)
	if len(sentences) != 3 {
		t.Fatalf("expected 3 sentences, got %d", len(sentences))
	}
	if sentences[0] != "<s>Первое.</s>" {
		t.Errorf("sentence 0: got %q", sentences[0])
	}
	if sentences[2] != "<s>Третье.</s>" {
		t.Errorf("sentence 2: got %q", sentences[2])
	}
}

func TestExtractSentences_None(t *testing.T) {
	ssml := "<speak>Просто текст.</speak>"
	sentences := extractSentences(ssml)
	if len(sentences) != 0 {
		t.Errorf("expected 0 sentences, got %d", len(sentences))
	}
}

// --- Integration: chunked synthesis ---

func TestSileroEngine_ChunkedSynthesis(t *testing.T) {
	var requestCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var body sileroRequestBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Return different-sized WAVs to verify concatenation.
		_, _ = w.Write(makeWAV(100 + requestCount*50))
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

	// Build SSML that exceeds 900 chars.
	var sentences []string
	for i := 0; i < 50; i++ {
		sentences = append(sentences, "<s>Это тестовое предложение для проверки.</s>")
	}
	ssml := "<speak><p>" + strings.Join(sentences, "") + "</p></speak>"

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "chunked.wav")
	err = engine.Synthesize(context.Background(), ssml, outPath)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if requestCount < 2 {
		t.Errorf("expected multiple server requests, got %d", requestCount)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data[:4]) != "RIFF" {
		t.Error("output is not a valid WAV")
	}

	// Verify data size is the sum of all chunk data sizes.
	dataChunkIdx := strings.Index(string(data), "data")
	dataSize := binary.LittleEndian.Uint32(data[dataChunkIdx+4 : dataChunkIdx+8])
	expectedSize := uint32(0)
	for i := 1; i <= requestCount; i++ {
		expectedSize += uint32(100 + i*50)
	}
	if dataSize != expectedSize {
		t.Errorf("data size: got %d, want %d", dataSize, expectedSize)
	}
}
