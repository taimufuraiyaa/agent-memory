package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type SkillInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Path        string `json:"path"`
}

func workspaceSkillsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if ws == "" {
			ws = svc.Workspace
		}

		// Find the project in registry to get WorkspaceRoot
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		projects, err := mgr.List(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}

		var projectRoot string
		for _, p := range projects {
			if p.Name == ws {
				projectRoot = p.WorkspaceRoot
				break
			}
		}

		// Fallbacks:
		// 1. If project name matches active workspace and projectRoot is empty, use current server CWD
		if projectRoot == "" && ws == svc.Workspace {
			if cwd, err := os.Getwd(); err == nil {
				projectRoot = workspace.FindProjectRoot(cwd)
			}
		}
		// 2. If it's still empty, try to fallback to the CWD's project root as a guess
		if projectRoot == "" {
			if cwd, err := os.Getwd(); err == nil {
				projectRoot = workspace.FindProjectRoot(cwd)
			}
		}

		skillsDir := filepath.Join(projectRoot, ".agents", "skills")
		var skills []SkillInfo

		if entries, err := os.ReadDir(skillsDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				skillName := entry.Name()
				skillFilePath := filepath.Join(skillsDir, skillName, "SKILL.md")
				if _, err := os.Stat(skillFilePath); err == nil {
					displayName, desc, content, err := parseSkillFile(skillFilePath)
					if err == nil {
						if displayName == "" {
							displayName = skillName
						}
						skills = append(skills, SkillInfo{
							Name:        skillName,
							DisplayName: displayName,
							Description: desc,
							Content:     content,
							Path:        skillFilePath,
						})
					}
				}
			}
		}

		writeOK(w, http.StatusOK, map[string]any{
			"workspace": ws,
			"skills":    skills,
		})
	}
}

func parseSkillFile(path string) (name, desc, content string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", err
	}
	raw := string(b)
	content = raw

	// Find frontmatter
	if strings.HasPrefix(raw, "---\n") || strings.HasPrefix(raw, "---\r\n") {
		parts := strings.SplitN(raw, "---", 3)
		if len(parts) >= 3 {
			frontmatter := parts[1]
			content = strings.TrimSpace(parts[2])
			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "name:") {
					name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
					name = strings.Trim(name, `"'`)
				} else if strings.HasPrefix(line, "description:") {
					desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
					desc = strings.Trim(desc, `"'`)
				}
			}
		}
	}
	return name, desc, content, nil
}
