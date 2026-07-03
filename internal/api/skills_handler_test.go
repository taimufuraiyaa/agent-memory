package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSkillsHandler(t *testing.T) {
	tempDir := t.TempDir()
	skillsDir := filepath.Join(tempDir, ".agents", "skills")
	err := os.MkdirAll(filepath.Join(skillsDir, "test-skill"), 0o755)
	require.NoError(t, err)

	skillContent := `---
name: Test Custom Skill
description: A mock skill for unit testing.
---
# Test Custom Skill
This is the mock skill content.`
	err = os.WriteFile(filepath.Join(skillsDir, "test-skill", "SKILL.md"), []byte(skillContent), 0o644)
	require.NoError(t, err)

	oldCwd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer func() { _ = os.Chdir(oldCwd) }()

	svc := &Service{
		Workspace: "test-workspace",
		BaseDir:   tempDir,
	}

	req := httptest.NewRequest("GET", "/api/v1/skills?workspace=test-workspace", nil)
	rr := httptest.NewRecorder()

	handler := workspaceSkillsHandler(svc)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data struct {
			Workspace string      `json:"workspace"`
			Skills    []SkillInfo `json:"skills"`
		} `json:"data"`
	}
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, "test-workspace", resp.Data.Workspace)
	require.Len(t, resp.Data.Skills, 1)
	assert.Equal(t, "test-skill", resp.Data.Skills[0].Name)
	assert.Equal(t, "Test Custom Skill", resp.Data.Skills[0].DisplayName)
	assert.Equal(t, "A mock skill for unit testing.", resp.Data.Skills[0].Description)
	assert.Contains(t, resp.Data.Skills[0].Content, "This is the mock skill content.")
}
