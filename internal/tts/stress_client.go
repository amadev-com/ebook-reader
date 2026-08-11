package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StressClient calls the /api/stress endpoint on the Silero TTS server
// to apply automatic stress placement using the silero-stress model.
// The endpoint accepts a batch of sentences and returns each sentence
// with + marks before stressed vowels.
type StressClient struct {
	serverURL string
	client    *http.Client
}

// NewStressClient creates a StressClient pointing at the given server URL
// (e.g. "http://localhost:5555"). Uses a 5-minute timeout to accommodate
// large chapters.
func NewStressClient(serverURL string) *StressClient {
	return &StressClient{
		serverURL: strings.TrimRight(serverURL, "/"),
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// stressRequest is the JSON body for POST /api/stress.
type stressRequest struct {
	Sentences []string `json:"sentences"`
}

// stressResponse is the JSON response from POST /api/stress.
type stressResponse struct {
	Results []string `json:"results"`
}

// StressText splits text into sentences, sends them to the /api/stress
// endpoint, and returns the full text with stress marks applied. The
// text is split on sentence boundaries (., !, ?, followed by whitespace
// or end of text) so the accentor gets sentence-level context for
// homograph disambiguation.
func (c *StressClient) StressText(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	sentences := splitSentencesForStress(text)
	if len(sentences) == 0 {
		return text, nil
	}

	result, err := c.StressSentences(ctx, sentences)
	if err != nil {
		return "", err
	}
	return strings.Join(result, "\n"), nil
}

// StressSentences sends a batch of sentences to the /api/stress endpoint
// and returns the stressed versions in the same order.
func (c *StressClient) StressSentences(ctx context.Context, sentences []string) ([]string, error) {
	if len(sentences) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(stressRequest{Sentences: sentences})
	if err != nil {
		return nil, fmt.Errorf("marshal stress request: %w", err)
	}

	url := c.serverURL + "/api/stress"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create stress request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stress request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("stress request failed: %s: %s", resp.Status, string(respBody))
	}

	var sr stressResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode stress response: %w", err)
	}

	if len(sr.Results) != len(sentences) {
		return nil, fmt.Errorf("stress result count mismatch: sent %d, got %d", len(sentences), len(sr.Results))
	}

	return sr.Results, nil
}

// splitSentencesForStress splits text into sentences for the stress API.
// Splits on sentence-ending punctuation (., !, ?) followed by whitespace
// or end of text. Newlines are preserved as sentence boundaries too.
// This gives the accentor sentence-level context for homograph
// disambiguation while keeping the text structure intact.
func splitSentencesForStress(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var sentences []string
	var current strings.Builder

	for _, r := range text {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' || r == '\n' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}

	if current.Len() > 0 {
		s := strings.TrimSpace(current.String())
		if s != "" {
			sentences = append(sentences, s)
		}
	}

	return sentences
}
