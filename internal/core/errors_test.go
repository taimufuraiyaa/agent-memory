package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestWorkspaceError(t *testing.T) {
	baseErr := errors.New("base error")
	err := NewWorkspaceError("test-workspace", "init", baseErr)

	if !errors.Is(err, baseErr) {
		t.Error("WorkspaceError should wrap base error")
	}

	want := `workspace "test-workspace": init: base error`
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	var wsErr *WorkspaceError
	if !errors.As(err, &wsErr) {
		t.Error("Should be able to unwrap to WorkspaceError")
	}
	if wsErr.Workspace != "test-workspace" {
		t.Errorf("Workspace = %q, want %q", wsErr.Workspace, "test-workspace")
	}
}

func TestStorageError(t *testing.T) {
	baseErr := fmt.Errorf("connection failed")
	err := NewStorageError("sqlite", "query", baseErr)

	want := "storage[sqlite]: query: connection failed"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	var stErr *StorageError
	if !errors.As(err, &stErr) {
		t.Error("Should be able to unwrap to StorageError")
	}
}

func TestEmbeddingError(t *testing.T) {
	baseErr := errors.New("model not found")
	err := NewEmbeddingError("onnx", "embed", baseErr)

	want := "embedding[onnx]: embed: model not found"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestRetrievalError(t *testing.T) {
	baseErr := errors.New("timeout")
	err := NewRetrievalError("test query", "search", baseErr)

	want := `retrieval: search (query="test query"): timeout`
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestValidationError(t *testing.T) {
	baseErr := errors.New("must not be empty")
	err := NewValidationError("workspace", "", baseErr)

	want := `validation: field "workspace": must not be empty`
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		checkFn  func(error) bool
		wantTrue bool
	}{
		{
			name:     "NotFound direct",
			err:      ErrNotFound,
			checkFn:  IsNotFound,
			wantTrue: true,
		},
		{
			name:     "NotFound wrapped",
			err:      fmt.Errorf("failed: %w", ErrNotFound),
			checkFn:  IsNotFound,
			wantTrue: true,
		},
		{
			name:     "NotFound different error",
			err:      ErrAlreadyExists,
			checkFn:  IsNotFound,
			wantTrue: false,
		},
		{
			name:     "AlreadyExists direct",
			err:      ErrAlreadyExists,
			checkFn:  IsAlreadyExists,
			wantTrue: true,
		},
		{
			name:     "InvalidInput direct",
			err:      ErrInvalidInput,
			checkFn:  IsInvalidInput,
			wantTrue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.checkFn(tt.err)
			if got != tt.wantTrue {
				t.Errorf("check function = %v, want %v", got, tt.wantTrue)
			}
		})
	}
}

func TestErrorWrapping(t *testing.T) {
	// Test that custom errors properly wrap and unwrap
	baseErr := errors.New("base")

	wsErr := NewWorkspaceError("ws", "op", baseErr)
	if !errors.Is(wsErr, baseErr) {
		t.Error("WorkspaceError should wrap base error")
	}

	stErr := NewStorageError("store", "op", wsErr)
	if !errors.Is(stErr, baseErr) {
		t.Error("StorageError should wrap through WorkspaceError to base")
	}

	var unwrappedWs *WorkspaceError
	if !errors.As(stErr, &unwrappedWs) {
		t.Error("Should be able to unwrap StorageError to WorkspaceError")
	}
}
