package application

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillCanaryAllocationInput struct {
	Workspace                string             `json:"workspace"`
	Environment              string             `json:"environment"`
	TaskID                   string             `json:"task_id"`
	SkillID                  string             `json:"skill_id"`
	PolicyVersion            int64              `json:"policy_version"`
	BasisPoints              int                `json:"basis_points"`
	RiskTier                 core.SkillRiskTier `json:"risk_tier"`
	Approved                 bool               `json:"approved"`
	Pinned                   bool               `json:"pinned"`
	Compatible               bool               `json:"compatible"`
	AcknowledgementSupported bool               `json:"acknowledgement_supported"`
}

type SkillCanaryAllocation struct {
	Allocated bool   `json:"allocated"`
	Bucket    int    `json:"bucket"`
	Reason    string `json:"reason"`
}

type SkillCanaryAllocator struct{}

func (SkillCanaryAllocator) Allocate(input SkillCanaryAllocationInput) SkillCanaryAllocation {
	if input.Pinned {
		return SkillCanaryAllocation{Reason: "pinned"}
	}
	if !input.Compatible {
		return SkillCanaryAllocation{Reason: "incompatible"}
	}
	if !input.AcknowledgementSupported {
		return SkillCanaryAllocation{Reason: "acknowledgement_unsupported"}
	}
	if input.RiskTier == core.SkillRiskHigh || (input.RiskTier == core.SkillRiskMedium && !input.Approved) || !input.RiskTier.Valid() {
		return SkillCanaryAllocation{Reason: "risk_ineligible"}
	}
	if input.BasisPoints <= 0 {
		return SkillCanaryAllocation{Reason: "allocation_zero"}
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{input.Workspace, input.Environment, input.TaskID, input.SkillID, strconv.FormatInt(input.PolicyVersion, 10)}, "\x00")))
	bucket := int(binary.BigEndian.Uint64(digest[:8]) % 10_000)
	limit := input.BasisPoints
	if limit > 10_000 {
		limit = 10_000
	}
	return SkillCanaryAllocation{Allocated: bucket < limit, Bucket: bucket, Reason: map[bool]string{true: "allocated", false: "outside_allocation"}[bucket < limit]}
}
