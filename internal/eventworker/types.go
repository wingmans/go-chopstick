// Package eventworker contains the serial download and processing consumers.
package eventworker

import (
	"context"
	"time"
)

const (
	DownloadDurable  = "edgar-download-v2"
	ProcessDurable   = "edgar-process-v1"
	OwnershipDurable = "edgar-ownership-v1"
	defaultAttempts  = 5
	progressEvery    = 5 * time.Second
)

// FailureStore persists the current error state for pipeline work.
type FailureStore interface {
	RecordWorkFailure(ctx context.Context, stage, accession, subject string,
		payload []byte, workErr error) (int, error)
	ResolveWorkFailure(ctx context.Context, stage, accession string) error
}

// DownloadConfig configures the serial filing download consumer.
type DownloadConfig struct {
	Directory   string
	UserAgent   string
	BaseURL     string
	Drain       bool
	MaxAttempts int
	Failures    FailureStore
}

// ProcessConfig configures the serial parse and aggregate consumer.
type ProcessConfig struct {
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

// Ownership consumes discovery events independently of financial-statement
// selection. It acknowledges recognized forms after a durable ownership
// outcome has been stored.
