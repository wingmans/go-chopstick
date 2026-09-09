// Package xerr provides structured, wrap-friendly errors for process boundaries.
package xerr

/*
Mental model

Error meaning lives at the surface.
Error causality lives underneath.

The core idea (one sentence)

Wrap errors freely for causality while you are “inside” the system;
assign meaning exactly once, at the boundary.

*/

import (
	"errors"
	"maps"
)

// Code identifies the stable category of an Error.
type Code int32

const (
	// OK indicates no error.
	OK Code = iota
	// NotFound indicates a missing resource.
	NotFound
	// InvalidInput indicates invalid caller input.
	InvalidInput
	// Conflict indicates a state conflict.
	Conflict
	// Internal indicates an unexpected internal failure.
	Internal
	// Unavailable indicates a dependency that cannot be reached or used.
	Unavailable
	// DeadlineExceeded indicates an operation exceeded its deadline.
	DeadlineExceeded
	// Canceled indicates an operation was canceled.
	Canceled
)

// Error is a structured error with a stable code and an optional cause.
type Error struct {
	Code    Code
	Reason  string
	Message string
	Details map[string]any

	Cause error
}

// New creates a structured error without an underlying cause.
func New(code Code, reason, message string) *Error {
	return &Error{
		Code:    code,
		Reason:  reason,
		Message: message,
		Details: nil,
		Cause:   nil,
	}
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return e.Reason
}

// Wrap creates a structured error that preserves cause for errors.Is/errors.As.
func Wrap(code Code, reason, message string, cause error) *Error {
	return &Error{
		Code:    code,
		Reason:  reason,
		Message: message,
		Details: nil,
		Cause:   cause,
	}
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// WithDetails attaches operator-facing details to an Error.
func WithDetails(err *Error, details map[string]any) *Error {
	err.Details = details

	return err
}

// Clone returns a copy of the error and its details.
func (e *Error) Clone() *Error {
	cp := *e
	if e.Details != nil {
		cp.Details = maps.Clone(e.Details)
	}

	return &cp
}

// IsCode reports whether err contains an Error with the requested code.
func IsCode(err error, code Code) bool {
	var e *Error

	return errors.As(err, &e) && e.Code == code
}
