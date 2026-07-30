package translation

import (
	"strings"
	"testing"
)

func TestSaveAndLoadSummary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	summary := "Quinn discovers the book and gains a system."
	if err := SaveSummary(dir, 1, summary); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	loaded, err := LoadSummary(dir, 1)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if loaded != summary {
		t.Errorf("loaded = %q, want %q", loaded, summary)
	}
}

func TestLoadSummaryMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := LoadSummary(dir, 99)
	if err != nil {
		t.Fatalf("LoadSummary missing: %v", err)
	}
	if s != "" {
		t.Errorf("missing summary = %q, want empty", s)
	}
}

func TestPreviousSummaries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Save summaries for chapters 1-3.
	for i, s := range []string{"summary 1", "summary 2", "summary 3"} {
		if err := SaveSummary(dir, i+1, s); err != nil {
			t.Fatalf("SaveSummary %d: %v", i+1, err)
		}
	}

	// Chapter 4 with count 2 should get summaries for chapters 2 and 3.
	prev, err := PreviousSummaries(dir, 4, 2)
	if err != nil {
		t.Fatalf("PreviousSummaries: %v", err)
	}
	if !strings.Contains(prev, "summary 2") {
		t.Errorf("prev missing summary 2: %s", prev)
	}
	if !strings.Contains(prev, "summary 3") {
		t.Errorf("prev missing summary 3: %s", prev)
	}
	if strings.Contains(prev, "summary 1") {
		t.Errorf("prev should NOT contain summary 1 (only 2 previous): %s", prev)
	}
}

func TestPreviousSummariesChapter1(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Chapter 1 has no previous summaries.
	prev, err := PreviousSummaries(dir, 1, 2)
	if err != nil {
		t.Fatalf("PreviousSummaries: %v", err)
	}
	if prev != "" {
		t.Errorf("chapter 1 prev = %q, want empty", prev)
	}
}

func TestPreviousSummariesNoSummaries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No summaries saved at all.
	prev, err := PreviousSummaries(dir, 5, 2)
	if err != nil {
		t.Fatalf("PreviousSummaries: %v", err)
	}
	if prev != "" {
		t.Errorf("prev with no saved summaries = %q, want empty", prev)
	}
}

func TestPreviousSummariesCountZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = SaveSummary(dir, 1, "summary 1")
	prev, err := PreviousSummaries(dir, 2, 0)
	if err != nil {
		t.Fatalf("PreviousSummaries: %v", err)
	}
	if prev != "" {
		t.Errorf("count=0 prev = %q, want empty", prev)
	}
}
