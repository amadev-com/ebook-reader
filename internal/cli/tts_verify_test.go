package cli //nolint:testpackage // needs access to unexported CLI internals

import (
	"strings"
	"testing"
)

func TestParseSileroWarnings(t *testing.T) {
	t.Parallel()
	logOutput := "[INFO]  silero-stress accentor loaded\n" +
		"[WARN]  SSML parsing failed for text '<speak><s>Вопр+ос в т+ом, кт+о +это сд+елал?</s></speak>', " +
		"returning 10ms silence: Failed to parse SSML: 'NoneType' object has no attribute 'keys'\n" +
		"[INFO]  loading silero-stress accentor\n" +
		"[WARN]  SSML parsing failed for text '<speak>\n<p><s>Тр+ое парн+ей — Ку+инн, Б+орден +и Дж+азз — " +
		"т+оже忙но б+егали п+о дж+унглям, гд+е т+оль...', " +
		"returning 10ms silence: Failed to parse SSML: 'NoneType' object has no attribute 'keys'\n" +
		"INFO:     127.0.0.1:5555 - \"POST /api/tts HTTP/1.1\" 200 OK\n" +
		"[ERROR] some other error\n"

	warnings := parseSileroWarnings(logOutput)
	if len(warnings) != 3 {
		t.Fatalf("parseSileroWarnings: got %d warnings, want 3", len(warnings))
	}

	// First warning: SSML parsing failure with snippet.
	if warnings[0].snippet == "" {
		t.Error("first warning: expected non-empty snippet")
	}
	if !strings.Contains(warnings[0].snippet, "Вопр") {
		t.Errorf("first warning snippet = %q, expected to contain 'Вопр'", warnings[0].snippet)
	}

	// Second warning: CJK character in snippet.
	if !strings.Contains(warnings[1].snippet, "Тр") {
		t.Errorf("second warning snippet = %q, expected to contain 'Тр'", warnings[1].snippet)
	}

	// Third warning: generic error, no snippet.
	if warnings[2].snippet != "" {
		t.Errorf("third warning snippet = %q, expected empty", warnings[2].snippet)
	}
}

func TestParseSileroWarnings_NoWarnings(t *testing.T) {
	t.Parallel()
	logOutput := "[INFO]  silero-stress accentor loaded\n" +
		"INFO:     127.0.0.1:5555 - \"POST /api/tts HTTP/1.1\" 200 OK\n"

	warnings := parseSileroWarnings(logOutput)
	if len(warnings) != 0 {
		t.Fatalf("parseSileroWarnings: got %d warnings, want 0", len(warnings))
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "green info",
			input: "\x1b[32m[INFO]\x1b[0m  loading model",
			want:  "[INFO]  loading model",
		},
		{
			name:  "yellow warn",
			input: "\x1b[33m[WARN]\x1b[0m  SSML parsing failed",
			want:  "[WARN]  SSML parsing failed",
		},
		{
			name:  "no ANSI codes",
			input: "plain text",
			want:  "plain text",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripANSI(tt.input)
			if got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractSnippet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "SSML parsing failure",
			line: "SSML parsing failed for text '<speak><s>Привет мир.</s></speak>', returning 10ms silence",
			want: "Привет мир.",
		},
		{
			name: "no text marker",
			line: "some other warning without text",
			want: "",
		},
		{
			name: "short text",
			line: "SSML parsing failed for text '<speak><p><s>Короткий текст.</s></p></speak>', returning",
			want: "Короткий текст.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSnippet(tt.line)
			if got != tt.want {
				t.Errorf("extractSnippet(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}
