package pipeline

import (
	"context"
	"time"
)

const (
	SubmissionDurable = "edgar-download-v2"
	ExtractionDurable = "edgar-process-v1"
	OwnershipDurable  = "edgar-ownership-v1"
	defaultAttempts   = 5
	progressEvery     = 5 * time.Second
)

// FailureStore persists the current error state for pipeline work.
type FailureStore interface {
	RecordWorkFailure(ctx context.Context, stage, accession, subject string,
		payload []byte, workErr error) (int, error)
	ResolveWorkFailure(ctx context.Context, stage, accession string) error
}

// SubmissionConfig configures the serial submission-acquisition worker.
type SubmissionConfig struct {
	Directory   string
	UserAgent   string
	BaseURL     string
	Drain       bool
	MaxAttempts int
	Failures    FailureStore
}

// ExtractionConfig configures the serial filing-extraction worker.
type ExtractionConfig struct {
	FilingsDirectory string
	ParsedDirectory  string
	AnalysisDBPath   string
	Drain            bool
	MaxAttempts      int
	Failures         FailureStore
}

// OwnershipConfig configures the all-CIK ownership discovery consumer.
type OwnershipConfig struct {
	FilingsDirectory string
	DatabasePath     string
	UserAgent        string
	BaseURL          string
	Drain            bool
	MaxAttempts      int
	Failures         FailureStore
}
