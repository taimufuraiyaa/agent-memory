package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

type GraphConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Executable         string   `yaml:"executable"`
	JobRoot            string   `yaml:"job_root"`
	TimeoutSeconds     int      `yaml:"timeout_seconds"`
	CancelGraceSeconds int      `yaml:"cancel_grace_seconds"`
	MaxOutputBytes     int64    `yaml:"max_output_bytes"`
	MaxRequestBytes    int64    `yaml:"max_request_bytes"`
	MaxDiskBytes       int64    `yaml:"max_disk_bytes"`
	MaxMemoryBytes     int64    `yaml:"max_memory_bytes"`
	MaxCPUSeconds      int      `yaml:"max_cpu_seconds"`
	CredentialEnv      []string `yaml:"credential_env"`
}

func DefaultGraphConfig(dataDir string) GraphConfig {
	return GraphConfig{
		Enabled: false, JobRoot: filepath.Join(dataDir, "graphrag-jobs"), TimeoutSeconds: 2 * 60 * 60,
		CancelGraceSeconds: 10, MaxOutputBytes: 1 << 20, MaxRequestBytes: 16 << 20,
		MaxDiskBytes: 20 << 30, MaxMemoryBytes: 8 << 30, MaxCPUSeconds: 4 * 60 * 60,
		CredentialEnv: []string{"INDEX_COMPLETION_API_KEY", "INDEX_EMBEDDING_API_KEY"},
	}
}

func (c GraphConfig) Validate(dataDir string) error {
	if !filepath.IsAbs(c.JobRoot) || strings.TrimSpace(dataDir) == "" || !filepath.IsAbs(dataDir) {
		return fmt.Errorf("graph.job_root and data_dir must be absolute")
	}
	relative, err := filepath.Rel(filepath.Clean(dataDir), filepath.Clean(c.JobRoot))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("graph.job_root must be contained by data_dir")
	}
	if c.Enabled && !filepath.IsAbs(c.Executable) {
		return fmt.Errorf("graph.executable must be absolute when graph indexing is enabled")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 24*60*60 || c.CancelGraceSeconds < 1 || c.CancelGraceSeconds > 60 ||
		c.MaxOutputBytes < 1024 || c.MaxOutputBytes > 16<<20 || c.MaxRequestBytes < 1024 || c.MaxRequestBytes > 64<<20 ||
		c.MaxDiskBytes < 1<<20 || c.MaxDiskBytes > 1<<40 || c.MaxMemoryBytes < 64<<20 || c.MaxMemoryBytes > 1<<40 ||
		c.MaxCPUSeconds < 1 || c.MaxCPUSeconds > 24*60*60 {
		return fmt.Errorf("graph resource controls are outside policy")
	}
	allowed := map[string]struct{}{"INDEX_COMPLETION_API_KEY": {}, "INDEX_EMBEDDING_API_KEY": {}}
	for _, name := range c.CredentialEnv {
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("graph credential environment name %q is not allowlisted", name)
		}
	}
	return nil
}
