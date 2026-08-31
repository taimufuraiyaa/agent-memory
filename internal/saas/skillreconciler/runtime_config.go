package skillreconciler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func LoadRuntimeConfig() (RuntimeConfig, error) {
	enabled, err := strconv.ParseBool(envSkillReconciler("AGENT_MEMORY_SKILL_RECONCILER_ENABLED", "false"))
	if err != nil {
		return RuntimeConfig{}, errors.New("AGENT_MEMORY_SKILL_RECONCILER_ENABLED must be true or false")
	}
	configuration := RuntimeConfig{
		Enabled: enabled, Owner: envSkillReconciler("AGENT_MEMORY_SKILL_RECONCILER_ID", "skill-reconciler"),
		PartitionLimit: 32, LeaseDuration: 2 * time.Minute, PollInterval: 30 * time.Second,
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_SKILL_RECONCILER_ASSIGNMENTS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &configuration.Assignments); err != nil {
			return RuntimeConfig{}, errors.New("AGENT_MEMORY_SKILL_RECONCILER_ASSIGNMENTS must be valid JSON")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_SKILL_RECONCILER_PARTITION_LIMIT")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			return RuntimeConfig{}, errors.New("AGENT_MEMORY_SKILL_RECONCILER_PARTITION_LIMIT is invalid")
		}
		configuration.PartitionLimit = value
	}
	for name, target := range map[string]*time.Duration{
		"AGENT_MEMORY_SKILL_RECONCILER_LEASE":         &configuration.LeaseDuration,
		"AGENT_MEMORY_SKILL_RECONCILER_POLL_INTERVAL": &configuration.PollInterval,
	} {
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

func envSkillReconciler(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
