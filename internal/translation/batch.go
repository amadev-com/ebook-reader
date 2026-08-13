package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// Batch metadata keys used when submitting batches via the OpenAI API.
const (
	BatchMetadataKeyType    = "type"
	BatchMetadataKeyProject = "project"
)

// BatchEndpoint is the OpenAI API endpoint used for batch processing.
const BatchEndpoint = "/v1/responses"

// Batch status strings returned by the OpenAI Batch API.
const (
	BatchStatusValidating = "validating"
	BatchStatusCompleted  = "completed"
	BatchStatusFailed     = "failed"
	BatchStatusExpired    = "expired"
	BatchStatusCancelled  = "cancelled"
)

// Batch type strings identifying which pipeline stage a batch belongs to.
const (
	BatchTypeAnalyze      = "analyze"
	BatchTypeAnalyzeMerge = "analyze-merge"
	BatchTypePronounce    = "pronounce"
	BatchTypeTranslate    = "translate"
)

// BatchRequest is a single request within a batch. Each request corresponds to
// one independent API call (e.g. one chapter's analysis or translation). The
// fields map directly to responses.ResponseNewParams — BuildResponseParams
// converts this into the SDK type.
type BatchRequest struct {
	CustomID     string // user-defined ID, e.g. "analyze-chapter-300"
	Instructions string // system prompt (Responses API "instructions" param)
	Input        string // user message text
	Model        string // model to use
	JSONMode     bool   // if true, sets response format to json_object
	MaxTokens    int64  // if > 0, sets max_output_tokens
}

// BatchRequestResult is the parsed result of a single batch request from the
// output file. It contains the custom ID, the extracted output text (if
// successful), or an error message (if the individual request failed).
type BatchRequestResult struct {
	CustomID string
	Content  string // output text from the response (empty if error)
	Error    string // error message if the individual request failed
}

// BatchClient wraps the OpenAI SDK for Batch API operations. It handles file
// upload, batch creation, polling, and result download. It is safe for
// sequential use (polling loop).
type BatchClient struct {
	sdk openai.Client
}

// NewBatchClient creates a BatchClient using the same credentials as a regular
// Client. The API key is read from the OPENAI_API_KEY environment variable.
func NewBatchClient(baseURL string, maxRetries int) (*BatchClient, error) {
	apiKey := apiKeyFromEnv()
	if apiKey == "" {
		return nil, errNoAPIKey
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(maxRetries),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &BatchClient{sdk: openai.NewClient(opts...)}, nil
}

// BuildResponseParams converts a BatchRequest (or ChatRequest) into the SDK's
// responses.ResponseNewParams. This is the single source of truth for how we
// construct Responses API parameters — used by both Client.Chat (live calls)
// and BuildJSONL (batch input file).
//
// Reasoning effort is set to "none" by default — our tasks (translation,
// glossary extraction, summaries, pronunciation) are straightforward and don't
// BuildResponseParams creates Responses API parameters for a user request.
// It disables reasoning, optionally requests a JSON-object response format, and
// applies a maximum output token limit when maxTokens is greater than zero.
func BuildResponseParams(
	model, instructions, userInput string,
	jsonMode bool,
	maxTokens int64,
) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model:        model,
		Instructions: param.NewOpt(instructions),
		Reasoning:    shared.ReasoningParam{Effort: shared.ReasoningEffortNone},
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: param.NewOpt(userInput),
						},
					},
				},
			},
		},
	}
	if jsonMode {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
			},
		}
	}
	if maxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(maxTokens)
	}
	return params
}

// batchInputLine is one line in the JSONL input file for the Batch API. Each
// line represents a single request to the /v1/responses endpoint. The body is
// a marshaled responses.ResponseNewParams (via [json.RawMessage] so the SDK's own
// MarshalJSON is used).
type batchInputLine struct {
	CustomID string          `json:"custom_id"`
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Body     json.RawMessage `json:"body"`
}

// BuildJSONL serializes a list of BatchRequests into the JSONL format expected
// by the OpenAI Batch API. Each request becomes one line targeting
// /v1/responses. The body of each line is a marshaled
// BuildJSONL serializes batch requests as newline-delimited JSON for the Responses API.
// It returns an error if a request cannot be marshaled or encoded.
func BuildJSONL(reqs []BatchRequest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, req := range reqs {
		params := BuildResponseParams(req.Model, req.Instructions, req.Input, req.JSONMode, req.MaxTokens)
		bodyJSON, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal ResponseNewParams for %s: %w", req.CustomID, err)
		}
		line := batchInputLine{
			CustomID: req.CustomID,
			Method:   "POST",
			URL:      BatchEndpoint,
			Body:     bodyJSON,
		}
		if err = enc.Encode(line); err != nil {
			return nil, fmt.Errorf("encode batch line %s: %w", req.CustomID, err)
		}
	}
	return buf.Bytes(), nil
}

// SubmitBatch uploads the JSONL input file and creates a batch. Returns the
// batch ID and input file ID. The batch processes via the /v1/responses
// endpoint with a 24h completion window.
func (bc *BatchClient) SubmitBatch(
	ctx context.Context,
	jsonlData []byte,
	metadata map[string]string,
) (string, string, error) {
	// Upload the JSONL file with purpose "batch".
	fileResp, err := bc.sdk.Files.New(ctx, openai.FileNewParams{
		File:    bytes.NewReader(jsonlData),
		Purpose: openai.FilePurposeBatch,
	})
	if err != nil {
		return "", "", fmt.Errorf("upload batch input file: %w", err)
	}

	// Create the batch.
	params := openai.BatchNewParams{
		InputFileID:      fileResp.ID,
		Endpoint:         openai.BatchNewParamsEndpointV1Responses,
		CompletionWindow: openai.BatchNewParamsCompletionWindow24h,
	}
	if len(metadata) > 0 {
		params.Metadata = shared.Metadata(metadata)
	}

	batch, err := bc.sdk.Batches.New(ctx, params)
	if err != nil {
		return "", "", fmt.Errorf("create batch: %w", err)
	}
	return batch.ID, fileResp.ID, nil
}

// BatchStatusInfo is a snapshot of a batch's status, returned by PollBatch.
type BatchStatusInfo struct {
	Status       string // validating, in_progress, finalizing, completed, failed, expired, cancelled
	OutputFileID string // populated when completed
	ErrorFileID  string // populated when there are errors
	Total        int64
	Completed    int64
	Failed       int64
	InputTokens  int64
	OutputTokens int64
}

// PollBatch retrieves the current status of a batch by ID.
func (bc *BatchClient) PollBatch(ctx context.Context, batchID string) (BatchStatusInfo, error) {
	batch, err := bc.sdk.Batches.Get(ctx, batchID)
	if err != nil {
		return BatchStatusInfo{}, fmt.Errorf("get batch %s: %w", batchID, err)
	}
	return BatchStatusInfo{
		Status:       string(batch.Status),
		OutputFileID: batch.OutputFileID,
		ErrorFileID:  batch.ErrorFileID,
		Total:        batch.RequestCounts.Total,
		Completed:    batch.RequestCounts.Completed,
		Failed:       batch.RequestCounts.Failed,
		InputTokens:  batch.Usage.InputTokens,
		OutputTokens: batch.Usage.OutputTokens,
	}, nil
}

// DownloadResults downloads the output file (JSONL) and parses each line into a
// BatchRequestResult. The outputFileID is obtained from PollBatch when the
// batch status is "completed". Results are keyed by custom_id.
func (bc *BatchClient) DownloadResults(ctx context.Context, outputFileID string) ([]BatchRequestResult, error) {
	if outputFileID == "" {
		return nil, fmt.Errorf("no output file ID — batch may not be completed yet")
	}

	resp, err := bc.sdk.Files.Content(ctx, outputFileID)
	if err != nil {
		return nil, fmt.Errorf("download output file %s: %w", outputFileID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read output file: %w", err)
	}

	return ParseBatchOutput(data)
}

// batchOutputLine is one line from the batch output JSONL file. The response
// body is a [json.RawMessage] that gets unmarshaled into responses.Response (the
// SDK type) to extract output text via Response.OutputText().
type batchOutputLine struct {
	CustomID string `json:"custom_id"`
	Response *struct {
		StatusCode int             `json:"status_code"`
		Body       json.RawMessage `json:"body"`
	} `json:"response"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ParseBatchOutput parses the JSONL output file from a completed batch. Each
// line contains the custom_id, the response (with the full API response body),
// or an error. The output text is extracted from the response body by
// ParseBatchOutput parses newline-delimited batch responses into request results.
// It records API and response errors on individual results and returns an error when
// an output line cannot be parsed. Successful results contain the extracted output text.
func ParseBatchOutput(data []byte) ([]BatchRequestResult, error) {
	var results []BatchRequestResult
	for lineNum, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var out batchOutputLine
		if err := json.Unmarshal(line, &out); err != nil {
			return nil, fmt.Errorf("parse output line %d: %w", lineNum+1, err)
		}
		result := BatchRequestResult{CustomID: out.CustomID}
		if out.Error != nil {
			result.Error = fmt.Sprintf("%s: %s", out.Error.Code, out.Error.Message)
			results = append(results, result)
			continue
		}
		if out.Response == nil {
			result.Error = "no response in output line"
			results = append(results, result)
			continue
		}
		if out.Response.StatusCode != http.StatusOK {
			result.Error = fmt.Sprintf("HTTP %d: %s", out.Response.StatusCode, string(out.Response.Body))
			results = append(results, result)
			continue
		}
		// Parse the response body as the SDK's responses.Response type and
		// extract output text via the SDK's own OutputText() method.
		var resp responses.Response
		if err := json.Unmarshal(out.Response.Body, &resp); err != nil {
			result.Error = fmt.Sprintf("parse response body: %v", err)
			results = append(results, result)
			continue
		}
		result.Content = resp.OutputText()
		if result.Content == "" {
			result.Error = "empty output text"
		}
		results = append(results, result)
	}
	return results, nil
}

// CancelBatch cancels an in-progress batch.
func (bc *BatchClient) CancelBatch(ctx context.Context, batchID string) error {
	_, err := bc.sdk.Batches.Cancel(ctx, batchID)
	if err != nil {
		return fmt.Errorf("cancel batch %s: %w", batchID, err)
	}
	return nil
}

// IsTerminalStatus returns true if the batch status is terminal (no further
// IsTerminalStatus reports whether a batch status indicates that processing is complete and no further polling is needed.
func IsTerminalStatus(status string) bool {
	switch status {
	case BatchStatusCompleted, BatchStatusFailed, BatchStatusExpired, BatchStatusCancelled:
		return true
	default:
		return false
	}
}

// SplitCustomID extracts the chapter ID from a custom_id like
// "analyze-chapter-300" or "translate-chapter-300". Returns 0 if the format
// doesn't match.
func SplitCustomID(customID, prefix string) int {
	s := prefix + "-chapter-"
	if !strings.HasPrefix(customID, s) {
		return 0
	}
	var id int
	if _, err := fmt.Sscanf(customID[len(s):], "%d", &id); err != nil {
		return 0
	}
	return id
}
