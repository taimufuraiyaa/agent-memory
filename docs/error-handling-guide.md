# Error Handling Guide

This document provides guidelines for consistent error handling across the agent-memory codebase.

## Principles

1. **Add Context**: Always add context when returning errors
2. **Wrap, Don't Replace**: Use `%w` to preserve the error chain
3. **Use Custom Types**: Use custom error types for domain-specific errors
4. **Be Specific**: Error messages should help debugging

## Quick Reference

### ❌ Bad - No Context
```go
if err != nil {
    return err
}
```

### ✅ Good - Add Context
```go
if err != nil {
    return fmt.Errorf("failed to load workspace %s: %w", name, err)
}
```

### ❌ Bad - Breaks Error Chain
```go
if err != nil {
    return fmt.Errorf("failed: %v", err)  // %v doesn't wrap!
}
```

### ✅ Good - Preserves Chain
```go
if err != nil {
    return fmt.Errorf("failed to parse config: %w", err)
}
```

## Custom Error Types

Use custom error types from `internal/core/errors.go` for domain-specific errors:

### Sentinel Errors

For well-known error conditions:

```go
import "github.com/time/timebooks/agent-memory/internal/core"

// Return sentinel error
if workspace == "" {
    return core.ErrInvalidInput
}

// Check for sentinel error
if core.IsNotFound(err) {
    // Handle not found case
}
```

Available sentinel errors:
- `core.ErrNotFound` - Resource doesn't exist
- `core.ErrAlreadyExists` - Resource already exists
- `core.ErrInvalidInput` - Invalid input parameters
- `core.ErrStorageUnavailable` - Storage backend unavailable
- `core.ErrEmbeddingUnavailable` - Embedding service unavailable

### Typed Errors

For errors with additional context:

```go
// Workspace operations
err := core.NewWorkspaceError("my-workspace", "init", underlyingErr)
// Returns: workspace "my-workspace": init: underlying error

// Storage operations
err := core.NewStorageError("sqlite", "query", underlyingErr)
// Returns: storage[sqlite]: query: underlying error

// Embedding operations
err := core.NewEmbeddingError("onnx", "embed", underlyingErr)
// Returns: embedding[onnx]: embed: underlying error

// Retrieval operations
err := core.NewRetrievalError("my query", "search", underlyingErr)
// Returns: retrieval: search (query="my query"): underlying error

// Validation errors
err := core.NewValidationError("workspace", "", underlyingErr)
// Returns: validation: field "workspace": underlying error
```

## Patterns by Package

### Storage Layer (`internal/storage/`)

```go
func (s *Store) Get(key string) ([]byte, error) {
    data, err := s.db.Query(...)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, core.ErrNotFound
        }
        return nil, core.NewStorageError("sqlite", "get", err)
    }
    return data, nil
}
```

### Engine Layer (`internal/engine/`)

```go
func (e *Engine) Search(query string) ([]Result, error) {
    results, err := e.retrieve(query)
    if err != nil {
        return nil, core.NewRetrievalError(query, "search", err)
    }
    return results, nil
}
```

### Workspace Layer (`internal/workspace/`)

```go
func (m *Manager) Init(opt InitOptions) error {
    if err := validateName(opt.Name); err != nil {
        return core.NewValidationError("name", opt.Name, err)
    }
    
    if err := m.createDB(opt.Name); err != nil {
        return core.NewWorkspaceError(opt.Name, "init", err)
    }
    return nil
}
```

### CLI Layer (`internal/cli/`)

```go
func runCommand(cmd *cobra.Command, args []string) error {
    workspace, err := getWorkspace()
    if err != nil {
        return fmt.Errorf("get workspace: %w", err)
    }
    
    if err := engine.Process(workspace); err != nil {
        // Check for specific errors
        if core.IsNotFound(err) {
            return fmt.Errorf("workspace %q not found - run 'agent-memory init' first", workspace)
        }
        return fmt.Errorf("process workspace %q: %w", workspace, err)
    }
    return nil
}
```

## Error Context Guidelines

### What to Include

1. **Operation**: What were you trying to do?
2. **Resource**: What were you operating on?
3. **Parameters**: Relevant identifiers (workspace name, file path, etc.)

### Examples

```go
// ✅ Good - Clear what failed and why
return fmt.Errorf("failed to write registry to %s: %w", path, err)

// ✅ Good - Includes workspace context
return fmt.Errorf("workspace %q: failed to initialize database: %w", name, err)

// ✅ Good - Includes operation and resource
return fmt.Errorf("search memories for query %q: %w", query, err)

// ❌ Bad - Too vague
return fmt.Errorf("operation failed: %w", err)

// ❌ Bad - Missing context
return fmt.Errorf("error: %w", err)
```

### Security Considerations

**Don't include sensitive data in error messages:**

```go
// ❌ Bad - Exposes password
return fmt.Errorf("login failed with password %s: %w", password, err)

// ✅ Good - Doesn't expose secrets
return fmt.Errorf("authentication failed: %w", err)

// ✅ Good - Sanitized
return fmt.Errorf("failed to connect to database at %s (credentials masked): %w", 
    sanitizeURL(dbURL), err)
```

## Testing Error Handling

### Test Error Wrapping

```go
func TestErrorWrapping(t *testing.T) {
    baseErr := errors.New("base error")
    wrapped := fmt.Errorf("operation failed: %w", baseErr)
    
    // Verify wrapping works
    if !errors.Is(wrapped, baseErr) {
        t.Error("error should wrap base error")
    }
}
```

### Test Custom Error Types

```go
func TestWorkspaceError(t *testing.T) {
    err := core.NewWorkspaceError("test", "init", errors.New("failed"))
    
    var wsErr *core.WorkspaceError
    if !errors.As(err, &wsErr) {
        t.Error("should unwrap to WorkspaceError")
    }
    
    if wsErr.Workspace != "test" {
        t.Errorf("workspace = %q, want %q", wsErr.Workspace, "test")
    }
}
```

### Test Sentinel Errors

```go
func TestNotFound(t *testing.T) {
    err := someFunction() // Returns core.ErrNotFound
    
    if !core.IsNotFound(err) {
        t.Error("should return ErrNotFound")
    }
}
```

## Migration Checklist

When improving error handling in existing code:

- [ ] Import `fmt` if not already imported
- [ ] Replace `return err` with `return fmt.Errorf("context: %w", err)`
- [ ] Use custom error types for domain-specific errors
- [ ] Update tests to verify error wrapping
- [ ] Check for sensitive data in error messages
- [ ] Verify error messages are helpful for debugging

## Tools

### Audit Script

Run the error handling audit:

```bash
./scripts/audit-errors.sh
```

This reports:
- Functions returning bare `return err`
- Error returns without `%w` wrapping
- Files missing `fmt` import
- Usage of custom error types

### Pre-commit Hook

The pre-commit hook includes basic error handling checks.

## Common Mistakes

### 1. Using %v Instead of %w

```go
// ❌ Wrong - %v doesn't preserve error chain
return fmt.Errorf("failed: %v", err)

// ✅ Correct - %w wraps error
return fmt.Errorf("failed: %w", err)
```

### 2. Adding Context Too Late

```go
// ❌ Bad - Lost context about which step failed
if err := step1(); err != nil {
    return err
}
if err := step2(); err != nil {
    return err
}
if err := step3(); err != nil {
    return fmt.Errorf("process failed: %w", err)
}

// ✅ Good - Context at each step
if err := step1(); err != nil {
    return fmt.Errorf("step1 failed: %w", err)
}
if err := step2(); err != nil {
    return fmt.Errorf("step2 failed: %w", err)
}
if err := step3(); err != nil {
    return fmt.Errorf("step3 failed: %w", err)
}
```

### 3. Wrapping Multiple Times

```go
// ❌ Bad - Duplicate context
func outer() error {
    if err := inner(); err != nil {
        return fmt.Errorf("inner failed: %w", err)
    }
    return nil
}

func inner() error {
    if err := doSomething(); err != nil {
        return fmt.Errorf("doSomething failed: %w", err)
    }
    return nil
}
// Results in: "inner failed: doSomething failed: actual error"

// ✅ Good - Contextual at each level
func outer() error {
    if err := inner(); err != nil {
        return fmt.Errorf("processing user data: %w", err)
    }
    return nil
}

func inner() error {
    if err := doSomething(); err != nil {
        return fmt.Errorf("validate input: %w", err)
    }
    return nil
}
// Results in: "processing user data: validate input: actual error"
```

## References

- [Go Blog: Error Handling](https://go.dev/blog/error-handling-and-go)
- [Go Blog: Working with Errors](https://go.dev/blog/go1.13-errors)
- [Effective Go: Errors](https://go.dev/doc/effective_go#errors)

## Examples from Codebase

See these files for good error handling patterns:
- `internal/workspace/manager.go` - Workspace operations
- `internal/core/errors.go` - Custom error types
- `internal/core/errors_test.go` - Error testing patterns

---

**Last Updated:** 2026-06-08  
**Maintainer:** agent-memory team
