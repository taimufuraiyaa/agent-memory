// Package validation provides input validation and sanitization for agent-memory.
//
// # Overview
//
// The validation package ensures that user input is safe and within acceptable
// limits before processing. It prevents common security issues like path traversal,
// injection attacks, and resource exhaustion.
//
// # Validation Functions
//
// The package provides validation for all major input types:
//
//	ValidateWorkspaceName: Ensures workspace names are safe and valid
//	ValidateFilePath:      Prevents path traversal attacks
//	ValidateContentLength:  Enforces memory content size limits
//	ValidateDiagramCode:    Enforces diagram code size limits
//
// # Sanitization
//
// For user-friendly input handling, the package provides sanitization:
//
//	SanitizeWorkspaceName: Converts arbitrary input into valid workspace names
//
// # Validation Rules
//
// Workspace Names:
//   - Non-empty
//   - Max 64 characters
//   - Alphanumeric, dashes, underscores, dots only
//   - No path traversal patterns (.., ~, etc.)
//
// File Paths:
//   - Non-empty
//   - No path traversal at start (.., ~)
//   - Normalized with filepath.Clean
//
// Content:
//   - Max 500KB (512,000 bytes)
//   - Valid UTF-8 encoding
//
// Diagram Code:
//   - Max 100KB (102,400 bytes)
//   - Valid UTF-8 encoding
//
// # Security Considerations
//
// Path Traversal Prevention:
//
// The validators detect and reject common path traversal patterns:
//   - "../" or ".."  (Unix path traversal)
//   - "..\" or ".\"  (Windows path traversal)
//   - "~"            (Home directory expansion)
//
// These patterns are dangerous because they could allow access to files
// outside the intended workspace directory.
//
// Size Limits:
//
// Content size limits prevent:
//   - Memory exhaustion from extremely large inputs
//   - Database bloat from oversized entries
//   - Performance degradation from processing huge strings
//
// The limits are generous (500KB for content, 100KB for diagrams) but
// prevent accidental or malicious resource exhaustion.
//
// UTF-8 Validation:
//
// All text content must be valid UTF-8. Invalid UTF-8 can cause:
//   - Display issues in terminals and UIs
//   - Database encoding errors
//   - String processing bugs
//
// # Usage in Write Pipeline
//
// The write pipeline automatically validates all input:
//
//	func (p *WritePipeline) Write(ctx context.Context, in WriteInput) (*WriteResult, error) {
//	    // Validate workspace name
//	    if err := validation.ValidateWorkspaceName(in.Workspace); err != nil {
//	        return nil, fmt.Errorf("invalid workspace: %w", err)
//	    }
//	    
//	    // Validate content length
//	    if err := validation.ValidateContentLength(in.Content); err != nil {
//	        return nil, fmt.Errorf("invalid content: %w", err)
//	    }
//	    
//	    // Validate diagram code if present
//	    if in.Diagram != nil {
//	        if err := validation.ValidateDiagramCode(in.Diagram.Code); err != nil {
//	            return nil, fmt.Errorf("invalid diagram: %w", err)
//	        }
//	    }
//	    
//	    // ... proceed with write ...
//	}
//
// This ensures that invalid input is rejected early with clear error messages.
//
// # Error Messages
//
// All validation functions return descriptive errors:
//
//	"workspace name cannot be empty"
//	"workspace name too long: 72 characters (max 64)"
//	"workspace name contains invalid characters: must be alphanumeric, dashes, underscores, or dots"
//	"workspace name contains invalid pattern: .."
//	"content too large: 524288 bytes (max 512000 bytes / ~500KB)"
//	"content contains invalid UTF-8 encoding"
//
// These errors are safe to display to users and provide actionable guidance.
//
// # Sanitization Example
//
// For user-friendly workspace creation from arbitrary input:
//
//	userInput := "My Project (2024)"
//	sanitized := validation.SanitizeWorkspaceName(userInput)
//	// Result: "my-project-2024"
//	
//	if err := validation.ValidateWorkspaceName(sanitized); err != nil {
//	    // This should never happen - sanitized names are always valid
//	    return err
//	}
//	
//	// Use sanitized name for workspace creation
//	workspace := sanitized
//
// Sanitization is idempotent: sanitizing an already-sanitized name returns
// the same result.
//
// # Testing
//
// The validation package has comprehensive tests covering:
//   - Valid and invalid inputs
//   - Edge cases (empty, max length, special characters)
//   - Security patterns (path traversal, injection)
//   - UTF-8 encoding validation
//   - Sanitization correctness
//
// Run tests: go test ./internal/validation/...
//
// # Performance
//
// Validation functions are fast (< 1μs for typical inputs):
//   - Workspace name: regex match + pattern checks
//   - File path: string operations + filepath.Clean
//   - Content: length check + UTF-8 validation
//   - Diagram: length check + UTF-8 validation
//
// Validation overhead is negligible compared to database operations
// and embedding generation.
package validation
