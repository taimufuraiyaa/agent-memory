package application

import (
	"fmt"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillCanaryAllocatorIsStableAndBounded(t *testing.T) {
	allocator := SkillCanaryAllocator{}
	input := SkillCanaryAllocationInput{Workspace: "ws", Environment: "production", TaskID: "task-1", SkillID: "skill-1", PolicyVersion: 3, BasisPoints: 2500, RiskTier: core.SkillRiskLow, Compatible: true, AcknowledgementSupported: true}
	first := allocator.Allocate(input)
	for range 20 {
		if retry := allocator.Allocate(input); retry != first {
			t.Fatalf("allocation changed across retry: %+v then %+v", first, retry)
		}
	}
	if first.Bucket < 0 || first.Bucket >= 10_000 {
		t.Fatalf("bucket = %d", first.Bucket)
	}
}

func TestSkillCanaryAllocatorPercentageIsWithinExpectedBound(t *testing.T) {
	allocator := SkillCanaryAllocator{}
	allocated := 0
	for index := range 20_000 {
		result := allocator.Allocate(SkillCanaryAllocationInput{Workspace: "ws", Environment: "production", TaskID: fmt.Sprintf("task-%d", index), SkillID: "skill-1", PolicyVersion: 1, BasisPoints: 1000, RiskTier: core.SkillRiskLow, Compatible: true, AcknowledgementSupported: true})
		if result.Allocated {
			allocated++
		}
	}
	if allocated < 1800 || allocated > 2200 {
		t.Fatalf("10%% allocation selected %d/20000 tasks", allocated)
	}
}

func TestSkillCanaryAllocatorExcludesPinsRiskAndIncompatibility(t *testing.T) {
	base := SkillCanaryAllocationInput{Workspace: "ws", Environment: "production", TaskID: "task", SkillID: "skill", PolicyVersion: 1, BasisPoints: 10_000, RiskTier: core.SkillRiskLow, Compatible: true, AcknowledgementSupported: true}
	tests := []struct {
		name   string
		mutate func(*SkillCanaryAllocationInput)
		reason string
	}{
		{name: "pin", mutate: func(i *SkillCanaryAllocationInput) { i.Pinned = true }, reason: "pinned"},
		{name: "incompatible", mutate: func(i *SkillCanaryAllocationInput) { i.Compatible = false }, reason: "incompatible"},
		{name: "high risk", mutate: func(i *SkillCanaryAllocationInput) { i.RiskTier = core.SkillRiskHigh }, reason: "risk_ineligible"},
		{name: "medium unapproved", mutate: func(i *SkillCanaryAllocationInput) { i.RiskTier = core.SkillRiskMedium }, reason: "risk_ineligible"},
		{name: "no acknowledgement", mutate: func(i *SkillCanaryAllocationInput) { i.AcknowledgementSupported = false }, reason: "acknowledgement_unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			test.mutate(&input)
			result := (SkillCanaryAllocator{}).Allocate(input)
			if result.Allocated || result.Reason != test.reason {
				t.Fatalf("allocation = %+v", result)
			}
		})
	}
}
