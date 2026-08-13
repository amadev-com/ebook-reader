package translation

import (
	"errors"
	"fmt"
	"os"
	"time"

	"ebook-reader/internal/project"
)

// ErrBatchStateNotFound is returned by LoadBatchState when no batch state file
// exists for the given batch type.
var ErrBatchStateNotFound = errors.New("batch state not found")

// BatchState is the locally persisted state of a submitted batch. It is stored
// in the project's batch directory (ai/batch_<type>.json) so that polling can
// be resumed with --continue after an interruption.
type BatchState struct {
	BatchID      string    `json:"batch_id"`
	InputFileID  string    `json:"input_file_id"`
	OutputFileID string    `json:"output_file_id,omitempty"`
	ErrorFileID  string    `json:"error_file_id,omitempty"`
	Type         string    `json:"type"`        // "analyze" or "translate"
	Model        string    `json:"model"`       // model used for this batch
	Endpoint     string    `json:"endpoint"`    // "/v1/responses"
	Status       string    `json:"status"`      // last known batch status
	ChapterIDs   []int     `json:"chapter_ids"` // chapters included in this batch
	CreatedAt    time.Time `json:"created_at"`
	CompletedAt  time.Time `json:"completed_at,omitzero"`
	Total        int64     `json:"total,omitempty"`
	Completed    int64     `json:"completed,omitempty"`
	Failed       int64     `json:"failed,omitempty"`
}

// SaveBatchState writes the batch state to ai/batch_<type>.json.
func SaveBatchState(aiDir string, state *BatchState) error {
	path := fmt.Sprintf("%s/batch_%s.json", aiDir, state.Type)
	return project.SaveJSON(path, state)
}

// LoadBatchState reads the batch state for the given type ("analyze" or
// "translate") from ai/batch_<type>.json. Returns nil, nil if no state file
// exists.
func LoadBatchState(aiDir, batchType string) (*BatchState, error) {
	path := fmt.Sprintf("%s/batch_%s.json", aiDir, batchType)
	if !project.Exists(path) {
		return nil, ErrBatchStateNotFound
	}
	var state BatchState
	if err := project.LoadJSON(path, &state); err != nil {
		return nil, fmt.Errorf("load batch state: %w", err)
	}
	return &state, nil
}

// DeleteBatchState removes the batch state file. Called after the batch is
// fully processed and results are saved.
func DeleteBatchState(aiDir, batchType string) error {
	path := fmt.Sprintf("%s/batch_%s.json", aiDir, batchType)
	if !project.Exists(path) {
		return nil
	}
	return os.Remove(path)
}
