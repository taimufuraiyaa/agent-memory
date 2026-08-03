package validation

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxWorkspaceNameLength is the maximum allowed workspace name length
	MaxWorkspaceNameLength = 64

	// MaxContentLength is the maximum allowed memory content length (500KB)
	MaxContentLength = 500 * 1024

	// MaxDiagramCodeLength is the maximum allowed diagram code length (100KB)
	MaxDiagramCodeLength = 100 * 1024
)

var (
	// workspaceNameRegex allows alphanumeric, dashes, underscores, and dots
	workspaceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

	// pathTraversalPatterns detects common path traversal attempts
	pathTraversalPatterns = []string{
		"..",
		"./",
		"../",
		"..\\",
		".\\",
		"~",
	}
)

// ValidateWorkspaceName checks if a workspace name is valid.
//
// Rules:
//   - Non-empty
//   - Length <= MaxWorkspaceNameLength (64 characters)
//   - Contains only: alphanumeric, dashes, underscores, dots
//   - No path traversal patterns
func ValidateWorkspaceName(name string) error {
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf("workspace name cannot be empty")
	}

	if len(name) > MaxWorkspaceNameLength {
		return fmt.Errorf("workspace name too long: %d characters (max %d)", len(name), MaxWorkspaceNameLength)
	}

	if !workspaceNameRegex.MatchString(name) {
		return fmt.Errorf("workspace name contains invalid characters: must be alphanumeric, dashes, underscores, or dots")
	}

	// Check for path traversal attempts
	for _, pattern := range pathTraversalPatterns {
		if strings.Contains(name, pattern) {
			return fmt.Errorf("workspace name contains invalid pattern: %s", pattern)
		}
	}

	return nil
}

// ValidateFilePath checks if a file path is safe for use.
//
// Rules:
//   - Non-empty
//   - No path traversal (.. or ~ at the start)
//   - Uses filepath.Clean to normalize
func ValidateFilePath(path string) error {
	path = strings.TrimSpace(path)

	if path == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Check for path traversal at the start
	if strings.HasPrefix(path, "..") || strings.HasPrefix(path, "~") {
		return fmt.Errorf("file path cannot start with '..' or '~': %s", path)
	}

	// Normalize the path
	cleaned := filepath.Clean(path)

	// Check if cleaning changed the path significantly (indicates traversal)
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("file path contains traversal patterns: %s", path)
	}

	return nil
}

// ValidateContentLength checks if content length is within limits.
//
// Rules:
//   - Length <= MaxContentLength (500KB)
//   - Valid UTF-8 encoding
func ValidateContentLength(content string) error {
	if len(content) > MaxContentLength {
		return fmt.Errorf("content too large: %d bytes (max %d bytes / ~500KB)", len(content), MaxContentLength)
	}

	if !utf8.ValidString(content) {
		return fmt.Errorf("content contains invalid UTF-8 encoding")
	}

	return nil
}

// ValidateDiagramCode checks if diagram code length is within limits.
func ValidateDiagramCode(code string) error {
	if len(code) > MaxDiagramCodeLength {
		return fmt.Errorf("diagram code too large: %d bytes (max %d bytes / ~100KB)", len(code), MaxDiagramCodeLength)
	}

	if !utf8.ValidString(code) {
		return fmt.Errorf("diagram code contains invalid UTF-8 encoding")
	}

	return nil
}

// SanitizeWorkspaceName sanitizes a workspace name by:
//   - Trimming whitespace
//   - Converting to lowercase
//   - Replacing invalid characters with dashes
//   - Removing path traversal patterns
//
// This is useful for generating workspace names from user input.
func SanitizeWorkspaceName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)

	// Replace any sequence of invalid characters with a single dash
	sanitized := make([]rune, 0, len(name))
	lastWasDash := false

	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			sanitized = append(sanitized, r)
			lastWasDash = (r == '-')
		} else if !lastWasDash {
			sanitized = append(sanitized, '-')
			lastWasDash = true
		}
	}

	// Trim leading/trailing dashes
	result := strings.Trim(string(sanitized), "-")

	// Remove any remaining path traversal patterns
	for _, pattern := range pathTraversalPatterns {
		result = strings.ReplaceAll(result, pattern, "-")
	}

	// Remove any double dashes created by replacements
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	// Trim again after pattern removal
	result = strings.Trim(result, "-")

	// Ensure max length
	if len(result) > MaxWorkspaceNameLength {
		result = result[:MaxWorkspaceNameLength]
	}

	return result
}
