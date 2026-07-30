// Package translation implements the M2 translation pipeline: an OpenAI
// client wrapper, a persistent glossary, per-chapter memory summaries, and
// the per-chapter translation loop that uses both for consistency.
//
// The client is a thin layer over the official OpenAI Go SDK
// (github.com/openai/openai-go/v3). The SDK handles retries internally via
// option.WithMaxRetries; this wrapper adds our prompt conventions (system +
// user messages, optional JSON object response format) and clean error
// wrapping.
package translation

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"ebook-reader/internal/config"
)

// Client wraps the OpenAI SDK client with our project's model configuration.
// It is safe for concurrent use (the underlying SDK client is).
type Client struct {
	sdk       openai.Client
	translate string // model for translation
	helper    string // model for glossary/summary side-calls
}

// NewClient builds a Client from the project's OpenAI config. The API key is
// read from the OPENAI_API_KEY environment variable; if absent, an error is
// returned. An optional base URL (for OpenAI-compatible endpoints) is taken
// from config.
func NewClient(cfg config.OpenAI) (*Client, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY environment variable is not set")
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(cfg.MaxRetries),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	sdk := openai.NewClient(opts...)
	return &Client{
		sdk:       sdk,
		translate: cfg.TranslationModel,
		helper:    cfg.HelperModel,
	}, nil
}

// ChatRequest is the input to a chat completion call.
type ChatRequest struct {
	System    string // system prompt (translator persona, glossary, context)
	User      string // user message (the text to process)
	Model     string // model to use; if empty, uses the translation model
	JSONMode  bool   // if true, sets response_format to json_object
	MaxTokens int64  // if > 0, sets max_completion_tokens
}

// ChatResponse is the output of a chat completion call.
type ChatResponse struct {
	Content      string // the assistant's message content
	FinishReason string // "stop", "length", etc.
	Usage        Usage
}

// Usage is the token usage breakdown.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Chat performs a chat completion. It selects the model from req.Model, or
// falls back to the translation model if empty. Errors from the API are
// wrapped with the model name and status code for diagnostics.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.translate
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: buildMessages(req.System, req.User),
	}
	if req.JSONMode {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		}
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(req.MaxTokens)
	}

	resp, err := c.sdk.Chat.Completions.New(ctx, params)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("chat completion (model %s): %w", model, err)
	}
	if len(resp.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("chat completion (model %s): no choices in response", model)
	}
	choice := resp.Choices[0]
	return ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}, nil
}

// HelperModel returns the configured helper model name (gpt-4.1-mini).
func (c *Client) HelperModel() string { return c.helper }

// TranslateModel returns the configured translation model name (gpt-4.1).
func (c *Client) TranslateModel() string { return c.translate }

// buildMessages constructs the message slice from a system + user prompt.
func buildMessages(system, user string) []openai.ChatCompletionMessageParamUnion {
	return []openai.ChatCompletionMessageParamUnion{
		{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: openai.String(system),
				},
			},
		},
		{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(user),
				},
			},
		},
	}
}
