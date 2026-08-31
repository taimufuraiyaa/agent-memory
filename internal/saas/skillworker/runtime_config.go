package skillworker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const DatabaseRole = "agent_memory_skill_worker"

type RuntimeConfig struct {
	Enabled              bool
	DatabaseURL          string
	DatabaseRole         string
	WorkerIdentity       string
	TelemetryAddress     string
	Assignments          []core.SkillOrchestratorScope
	ClaimBatch           int
	Concurrency          int
	RollbackReserved     int
	TenantConcurrency    int
	WorkspaceConcurrency int
	LeaseDuration        time.Duration
	StageTimeout         time.Duration
	PollInterval         time.Duration
	DrainTimeout         time.Duration
}

func LoadRuntimeConfig() (RuntimeConfig, error) {
	enabled, err := strconv.ParseBool(envSkillWorker("AGENT_MEMORY_SKILL_WORKER_ENABLED", "false"))
	if err != nil {
		return RuntimeConfig{}, errors.New("AGENT_MEMORY_SKILL_WORKER_ENABLED must be true or false")
	}
	configuration := RuntimeConfig{Enabled: enabled, DatabaseURL: strings.TrimSpace(os.Getenv("AGENT_MEMORY_DATABASE_URL")),
		DatabaseRole: envSkillWorker("AGENT_MEMORY_SKILL_WORKER_DATABASE_ROLE", DatabaseRole), WorkerIdentity: envSkillWorker("AGENT_MEMORY_SKILL_WORKER_ID", "skill-worker"),
		TelemetryAddress: envSkillWorker("AGENT_MEMORY_TELEMETRY_LISTEN_ADDR", ":9090"), ClaimBatch: 16, Concurrency: 8,
		RollbackReserved: 2, TenantConcurrency: 4, WorkspaceConcurrency: 2, LeaseDuration: 2 * time.Minute, StageTimeout: time.Minute, PollInterval: time.Second, DrainTimeout: 30 * time.Second}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_SKILL_WORKER_ASSIGNMENTS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &configuration.Assignments); err != nil {
			return RuntimeConfig{}, errors.New("AGENT_MEMORY_SKILL_WORKER_ASSIGNMENTS must be valid JSON")
		}
	}
	for name, target := range map[string]*int{"AGENT_MEMORY_SKILL_WORKER_CLAIM_BATCH": &configuration.ClaimBatch, "AGENT_MEMORY_SKILL_WORKER_CONCURRENCY": &configuration.Concurrency, "AGENT_MEMORY_SKILL_WORKER_ROLLBACK_RESERVED": &configuration.RollbackReserved, "AGENT_MEMORY_SKILL_WORKER_TENANT_CONCURRENCY": &configuration.TenantConcurrency, "AGENT_MEMORY_SKILL_WORKER_WORKSPACE_CONCURRENCY": &configuration.WorkspaceConcurrency} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil {
				return RuntimeConfig{}, fmt.Errorf("%s is invalid", name)
			}
			*target = value
		}
	}
	for name, target := range map[string]*time.Duration{"AGENT_MEMORY_SKILL_WORKER_LEASE": &configuration.LeaseDuration, "AGENT_MEMORY_SKILL_WORKER_STAGE_TIMEOUT": &configuration.StageTimeout, "AGENT_MEMORY_SKILL_WORKER_POLL_INTERVAL": &configuration.PollInterval, "AGENT_MEMORY_SKILL_WORKER_DRAIN_TIMEOUT": &configuration.DrainTimeout} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			value, parseErr := time.ParseDuration(raw)
			if parseErr != nil {
				return RuntimeConfig{}, fmt.Errorf("%s is invalid", name)
			}
			*target = value
		}
	}
	if err := configuration.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return configuration, nil
}

func (c RuntimeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.DatabaseURL) == "" || c.DatabaseRole != DatabaseRole || strings.TrimSpace(c.WorkerIdentity) == "" || !strings.HasPrefix(c.TelemetryAddress, ":") {
		return errors.New("hosted skill worker database, role, identity, or telemetry configuration is invalid")
	}
	if len(c.Assignments) == 0 || len(c.Assignments) > 1_000 {
		return errors.New("hosted skill worker assignments are required and bounded")
	}
	seen := make(map[core.SkillOrchestratorScope]struct{}, len(c.Assignments))
	for _, scope := range c.Assignments {
		if err := scope.Validate(); err != nil || scope.TenantID == "" {
			return errors.New("hosted skill worker assignment must be tenant-scoped")
		}
		if _, exists := seen[scope]; exists {
			return errors.New("hosted skill worker assignments must be unique")
		}
		seen[scope] = struct{}{}
	}
	if c.ClaimBatch < 2 || c.ClaimBatch > 100 || c.Concurrency < 1 || c.Concurrency > c.ClaimBatch || c.RollbackReserved < 1 || c.RollbackReserved >= c.ClaimBatch || c.RollbackReserved > c.Concurrency {
		return errors.New("hosted skill worker claim, concurrency, or rollback reservation is invalid")
	}
	if c.TenantConcurrency < 1 || c.TenantConcurrency > c.Concurrency || c.WorkspaceConcurrency < 1 || c.WorkspaceConcurrency > c.TenantConcurrency {
		return errors.New("hosted skill worker tenant or workspace concurrency is invalid")
	}
	if c.LeaseDuration < time.Second || c.LeaseDuration > time.Hour || c.StageTimeout <= 0 || c.StageTimeout > c.LeaseDuration || c.PollInterval < 10*time.Millisecond || c.PollInterval > time.Minute || c.DrainTimeout <= 0 || c.DrainTimeout > time.Hour {
		return errors.New("hosted skill worker timing is invalid")
	}
	return nil
}

func envSkillWorker(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
