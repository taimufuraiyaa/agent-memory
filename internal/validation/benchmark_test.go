package validation

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkValidateWorkspaceName benchmarks workspace name validation
func BenchmarkValidateWorkspaceName(b *testing.B) {
	names := []string{
		"valid-workspace",
		"my_workspace_123",
		"workspace.prod",
		"test-workspace-long-name-with-many-parts",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := names[i%len(names)]
		_ = ValidateWorkspaceName(name)
	}
}

// BenchmarkValidateWorkspaceNameInvalid benchmarks validation with invalid names
func BenchmarkValidateWorkspaceNameInvalid(b *testing.B) {
	invalidNames := []string{
		"../../../etc/passwd",
		"workspace with spaces",
		"workspace\nwith\nnewlines",
		strings.Repeat("a", 100), // too long
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := invalidNames[i%len(invalidNames)]
		_ = ValidateWorkspaceName(name)
	}
}

// BenchmarkValidateFilePath benchmarks file path validation
func BenchmarkValidateFilePath(b *testing.B) {
	paths := []string{
		"/valid/file/path.txt",
		"relative/path/file.md",
		"/usr/local/bin/agent-memory",
		"./local/path/data.json",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := paths[i%len(paths)]
		_ = ValidateFilePath(path)
	}
}

// BenchmarkValidateFilePathInvalid benchmarks validation with path traversal attempts
func BenchmarkValidateFilePathInvalid(b *testing.B) {
	maliciousPaths := []string{
		"../../../etc/passwd",
		"~/secret/.ssh/id_rsa",
		"path/with/../traversal",
		"./../../sensitive/data",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := maliciousPaths[i%len(maliciousPaths)]
		_ = ValidateFilePath(path)
	}
}

// BenchmarkValidateContentLength benchmarks content size validation
func BenchmarkValidateContentLength(b *testing.B) {
	contents := []string{
		"Short content",
		strings.Repeat("Medium length content with repeated text. ", 100),
		strings.Repeat("A", 1000),
		strings.Repeat("Long content ", 5000),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		content := contents[i%len(contents)]
		_ = ValidateContentLength(content)
	}
}

// BenchmarkValidateContentLengthLarge benchmarks validation with large content
func BenchmarkValidateContentLengthLarge(b *testing.B) {
	// Create content at various sizes
	sizes := []int{10_000, 50_000, 100_000, 500_000, 1_000_000}

	for _, size := range sizes {
		content := strings.Repeat("x", size)
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ValidateContentLength(content)
			}
		})
	}
}

// BenchmarkValidateDiagramCode benchmarks diagram code validation
func BenchmarkValidateDiagramCode(b *testing.B) {
	diagrams := []string{
		"graph TD\nA-->B",
		"sequenceDiagram\nAlice->>Bob: Hello",
		strings.Repeat("node", 100),
		"flowchart LR\nStart --> End",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		diagram := diagrams[i%len(diagrams)]
		_ = ValidateDiagramCode(diagram)
	}
}

// BenchmarkSanitizeWorkspaceName benchmarks workspace name sanitization
func BenchmarkSanitizeWorkspaceName(b *testing.B) {
	names := []string{
		"My Workspace!",
		"workspace@#$%",
		"test/workspace\\name",
		"UPPERCASE-workspace",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := names[i%len(names)]
		_ = SanitizeWorkspaceName(name)
	}
}

// BenchmarkValidationPipeline benchmarks a full validation pipeline
func BenchmarkValidationPipeline(b *testing.B) {
	workspace := "my-workspace"
	filePath := "/path/to/file.txt"
	content := strings.Repeat("Content with various data. ", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ValidateWorkspaceName(workspace); err != nil {
			b.Fatal(err)
		}
		if err := ValidateFilePath(filePath); err != nil {
			b.Fatal(err)
		}
		if err := ValidateContentLength(content); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConcurrentValidation benchmarks concurrent validation operations
func BenchmarkConcurrentValidation(b *testing.B) {
	workspaces := []string{"workspace-1", "workspace-2", "workspace-3", "workspace-4"}
	contents := []string{
		"Short content",
		strings.Repeat("Medium content. ", 50),
		strings.Repeat("Longer content with data. ", 100),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			workspace := workspaces[i%len(workspaces)]
			content := contents[i%len(contents)]

			_ = ValidateWorkspaceName(workspace)
			_ = ValidateContentLength(content)
			i++
		}
	})
}

// BenchmarkUTF8Validation benchmarks UTF-8 encoding validation
func BenchmarkUTF8Validation(b *testing.B) {
	validUTF8 := []string{
		"Simple ASCII text",
		"Unicode text with émojis 🎉",
		"日本語のテキスト",
		"Смешанный текст with multiple scripts",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		text := validUTF8[i%len(validUTF8)]
		_ = ValidateContentLength(text) // This includes UTF-8 validation
	}
}

// BenchmarkPathTraversalDetection benchmarks path traversal pattern detection
func BenchmarkPathTraversalDetection(b *testing.B) {
	paths := []string{
		"../../../etc/passwd",
		"normal/path/file.txt",
		"path/../traversal/attempt",
		"~/user/home/file",
		"./relative/./path/./with/./dots",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := paths[i%len(paths)]
		_ = ValidateFilePath(path)
	}
}

// BenchmarkLongWorkspaceNames benchmarks validation of varying name lengths
func BenchmarkLongWorkspaceNames(b *testing.B) {
	lengths := []int{5, 10, 20, 40, 64} // 64 is the max

	for _, length := range lengths {
		name := strings.Repeat("a", length)
		b.Run(fmt.Sprintf("len_%d", length), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ValidateWorkspaceName(name)
			}
		})
	}
}
