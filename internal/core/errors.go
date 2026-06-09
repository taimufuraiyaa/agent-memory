package core

import (
	"errors"
	"fmt"
)

// Common sentinel errors that can be checked with errors.Is
var (
	// ErrNotFound indicates a requested resource does not exist.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists indicates a resource already exists.
	ErrAlreadyExists = errors.New("already exists")

	// ErrInvalidInput indicates invalid input parameters.
	ErrInvalidInput = errors.New("invalid input")

	// ErrStorageUnavailable indicates the storage backend is unavailable.
	ErrStorageUnavailable = errors.New("storage unavailable")

	// ErrEmbeddingUnavailable indicates the embedding service is unavailable.
	ErrEmbeddingUnavailable = errors.New("embedding unavailable")
)

// WorkspaceError represents an error related to workspace operations.
type WorkspaceError struct {
	Workspace string
	Op        string // Operation being performed
	Err       error
}

func (e *WorkspaceError) Error() string {
	if e.Workspace != "" {
		return fmt.Sprintf("workspace %q: %s: %v", e.Workspace, e.Op, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *WorkspaceError) Unwrap() error {
	return e.Err
}

// NewWorkspaceError creates a new WorkspaceError.
func NewWorkspaceError(workspace, op string, err error) error {
	return &WorkspaceError{
		Workspace: workspace,
		Op:        op,
		Err:       err,
	}
}

// StorageError represents an error from the storage layer.
type StorageError struct {
	Store string // Storage backend (sqlite, markdown, etc.)
	Op    string // Operation being performed
	Err   error
}

func (e *StorageError) Error() string {
	if e.Store != "" {
		return fmt.Sprintf("storage[%s]: %s: %v", e.Store, e.Op, e.Err)
	}
	return fmt.Sprintf("storage: %s: %v", e.Op, e.Err)
}

func (e *StorageError) Unwrap() error {
	return e.Err
}

// NewStorageError creates a new StorageError.
func NewStorageError(store, op string, err error) error {
	return &StorageError{
		Store: store,
		Op:    op,
		Err:   err,
	}
}

// EmbeddingError represents an error from the embedding service.
type EmbeddingError struct {
	Provider string // Provider name (local, onnx, openai, etc.)
	Op       string // Operation being performed
	Err      error
}

func (e *EmbeddingError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("embedding[%s]: %s: %v", e.Provider, e.Op, e.Err)
	}
	return fmt.Sprintf("embedding: %s: %v", e.Op, e.Err)
}

func (e *EmbeddingError) Unwrap() error {
	return e.Err
}

// NewEmbeddingError creates a new EmbeddingError.
func NewEmbeddingError(provider, op string, err error) error {
	return &EmbeddingError{
		Provider: provider,
		Op:       op,
		Err:      err,
	}
}

// RetrievalError represents an error during memory retrieval.
type RetrievalError struct {
	Query string // Query being executed
	Op    string // Operation (search, recall, etc.)
	Err   error
}

func (e *RetrievalError) Error() string {
	if e.Query != "" {
		return fmt.Sprintf("retrieval: %s (query=%q): %v", e.Op, e.Query, e.Err)
	}
	return fmt.Sprintf("retrieval: %s: %v", e.Op, e.Err)
}

func (e *RetrievalError) Unwrap() error {
	return e.Err
}

// NewRetrievalError creates a new RetrievalError.
func NewRetrievalError(query, op string, err error) error {
	return &RetrievalError{
		Query: query,
		Op:    op,
		Err:   err,
	}
}

// ValidationError represents an input validation error.
type ValidationError struct {
	Field string // Field name
	Value string // Invalid value (sanitized)
	Err   error
}

func (e *ValidationError) Error() string {
	if e.Field != "" && e.Value != "" {
		return fmt.Sprintf("validation: field %q with value %q: %v", e.Field, e.Value, e.Err)
	}
	if e.Field != "" {
		return fmt.Sprintf("validation: field %q: %v", e.Field, e.Err)
	}
	return fmt.Sprintf("validation: %v", e.Err)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

// NewValidationError creates a new ValidationError.
func NewValidationError(field, value string, err error) error {
	return &ValidationError{
		Field: field,
		Value: value,
		Err:   err,
	}
}

// IsNotFound checks if an error is or wraps ErrNotFound.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsAlreadyExists checks if an error is or wraps ErrAlreadyExists.
func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
}

// IsInvalidInput checks if an error is or wraps ErrInvalidInput.
func IsInvalidInput(err error) bool {
	return errors.Is(err, ErrInvalidInput)
}

// IsStorageUnavailable checks if an error is or wraps ErrStorageUnavailable.
func IsStorageUnavailable(err error) bool {
	return errors.Is(err, ErrStorageUnavailable)
}

// IsEmbeddingUnavailable checks if an error is or wraps ErrEmbeddingUnavailable.
func IsEmbeddingUnavailable(err error) bool {
	return errors.Is(err, ErrEmbeddingUnavailable)
}
