package validation

import (
	"strings"
	"testing"
)

func TestValidateWorkspaceName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names
		{"valid simple", "my-project", false},
		{"valid with underscore", "my_project", false},
		{"valid with dots", "my.project.v1", false},
		{"valid mixed", "My-Project_v1.2", false},
		{"valid numbers", "project123", false},
		{"valid single char", "a", false},
		{"valid max length", strings.Repeat("a", MaxWorkspaceNameLength), false},
		
		// Invalid names
		{"empty", "", true},
		{"spaces", "my project", true},
		{"too long", strings.Repeat("a", MaxWorkspaceNameLength+1), true},
		{"path traversal dots", "../project", true},
		{"path traversal tilde", "~/project", true},
		{"slash", "my/project", true},
		{"backslash", "my\\project", true},
		{"special chars", "my@project", true},
		{"leading dot dot", "..project", true},
		{"contains traversal", "my..project", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorkspaceName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWorkspaceName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateFilePath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid paths
		{"valid relative", "config/settings.yaml", false},
		{"valid absolute", "/usr/local/bin/app", false},
		{"valid with dots in name", "file.config.yaml", false},
		{"valid nested", "a/b/c/d.txt", false},
		
		// Invalid paths
		{"empty", "", true},
		{"traversal start", "../secret", true},
		{"traversal tilde", "~/secret", true},
		{"traversal nested", "a/../../b", true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFilePath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFilePath(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateContentLength(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid content
		{"empty", "", false},
		{"small", "Hello, world!", false},
		{"max allowed", strings.Repeat("a", MaxContentLength), false},
		{"unicode", "Hello 世界 🌍", false},
		
		// Invalid content
		{"too large", strings.Repeat("a", MaxContentLength+1), true},
		{"invalid utf8", string([]byte{0xff, 0xfe, 0xfd}), true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContentLength(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContentLength() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDiagramCode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid diagram code
		{"empty", "", false},
		{"simple mermaid", "graph TD\nA-->B", false},
		{"max allowed", strings.Repeat("a", MaxDiagramCodeLength), false},
		
		// Invalid diagram code
		{"too large", strings.Repeat("a", MaxDiagramCodeLength+1), true},
		{"invalid utf8", string([]byte{0xff, 0xfe, 0xfd}), true},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDiagramCode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDiagramCode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeWorkspaceName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "my-project", "my-project"},
		{"uppercase", "My-Project", "my-project"},
		{"spaces to dashes", "my project name", "my-project-name"},
		{"multiple spaces", "my    project", "my-project"},
		{"special chars", "my@project#2024", "my-project-2024"},
		{"leading trailing spaces", "  project  ", "project"},
		{"leading trailing dashes", "--project--", "project"},
		{"unicode removed", "project世界", "project"},
		{"too long", strings.Repeat("a", 100), strings.Repeat("a", MaxWorkspaceNameLength)},
		{"preserve valid chars", "my_project.v1-2", "my_project.v1-2"},
		{"path traversal cleaned", "../project", "project"},
		{"tilde removed", "~/project", "project"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeWorkspaceName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeWorkspaceName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeWorkspaceNameAlwaysValid(t *testing.T) {
	// Fuzz-like test: sanitized names should always pass validation
	inputs := []string{
		"my project",
		"../../../etc/passwd",
		"~/secret",
		"My@Project#2024!",
		"  spaces  everywhere  ",
		"unicode世界🌍emoji",
		strings.Repeat("x", 200),
	}
	
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			sanitized := SanitizeWorkspaceName(input)
			if sanitized == "" {
				// Empty is ok if input was all invalid chars
				return
			}
			if err := ValidateWorkspaceName(sanitized); err != nil {
				t.Errorf("SanitizeWorkspaceName(%q) = %q, which fails validation: %v", input, sanitized, err)
			}
		})
	}
}
