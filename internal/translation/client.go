// Package translation implements the M2 translation pipeline: an OpenAI
// client wrapper, a persistent glossary, per-chapter memory summaries, and
// the per-chapter translation loop that uses both for consistency.
//
// The client is a thin layer over the official OpenAI Go SDK
// (github.com/openai/openai-go/v3). It uses the Responses API
// (client.Responses.New) instead of the deprecated Chat Completions API.
// The SDK handles retries internally via option.WithMaxRetries; this wrapper
// adds our prompt conventions (system instructions + user input, optional
// JSON object response format) and clean error wrapping.
package translation

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
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

// ChatRequest is the input to a Responses API call.
type ChatRequest struct {
	System    string // system prompt (translator persona, glossary, context)
	User      string // user message (the text to process)
	Model     string // model to use; if empty, uses the translation model
	JSONMode  bool   // if true, sets response format to json_object
	MaxTokens int64  // if > 0, sets max_output_tokens
}

// ChatResponse is the output of a Responses API call.
type ChatResponse struct {
	Content      string // the assistant's message content (output_text)
	FinishReason string // "completed", "incomplete", "failed", etc.
	Usage        Usage
}

// Usage is the token usage breakdown.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Chat performs a Responses API call. It selects the model from req.Model, or
// falls back to the translation model if empty. The system prompt is passed
// as the `instructions` parameter; the user message is passed as the input.
// Errors from the API are wrapped with the model name for diagnostics.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.translate
	}

	params := responses.ResponseNewParams{
		Model:        shared.ResponsesModel(model),
		Instructions: param.NewOpt(req.System),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: param.NewOpt(req.User),
						},
					},
				},
			},
		},
	}
	if req.JSONMode {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
			},
		}
	}
	if req.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(req.MaxTokens)
	}

	resp, err := c.sdk.Responses.New(ctx, params)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("responses api (model %s): %w", model, err)
	}

	content := resp.OutputText()
	if content == "" {
		return ChatResponse{}, fmt.Errorf("responses api (model %s): empty output text", model)
	}

	return ChatResponse{
		Content:      content,
		FinishReason: string(resp.Status),
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}, nil
}

// HelperModel returns the configured helper model name (gpt-4.1-mini).
func (c *Client) HelperModel() string { return c.helper }

// TranslateModel returns the configured translation model name (gpt-4.1).
func (c *Client) TranslateModel() string { return c.translate }
