package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillChaosFaultAdapterInjectsOnceBeforeAndAfter(t *testing.T) {
	for _, point := range []SkillChaosFaultPoint{SkillChaosBeforeSideEffect, SkillChaosAfterSideEffect} {
		t.Run(string(point), func(t *testing.T) {
			var effects atomic.Int64
			delegate := SkillStageAdapterFunc(func(context.Context, core.SkillJob) (SkillStageResult, error) {
				effects.CompareAndSwap(0, 1)
				return SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
			})
			adapter, err := NewSkillChaosFaultAdapter(delegate, point)
			if err != nil {
				t.Fatal(err)
			}
			job := core.SkillJob{ID: "job-1"}
			if _, err := adapter.Execute(context.Background(), job); !errors.Is(err, ErrSkillChaosInjected) {
				t.Fatalf("first execution error = %v", err)
			}
			if _, err := adapter.Execute(context.Background(), job); err != nil {
				t.Fatalf("replay error = %v", err)
			}
			if effects.Load() != 1 {
				t.Fatalf("effects = %d, want 1", effects.Load())
			}
		})
	}
}

func TestSkillChaosCertificateBindsCompleteBoundedMatrix(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	input := completeSkillChaosInput()
	first, err := CertifySkillOrchestratorChaos(input, "release-chaos-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CertifySkillOrchestratorChaos(input, "release-chaos-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReportDigest != second.ReportDigest || first.Signature != second.Signature {
		t.Fatal("canonical chaos certificate was not deterministic")
	}
	if err := VerifySkillOrchestratorChaosCertificate(first, publicKey); err != nil {
		t.Fatal(err)
	}
	first.Observations[0].UnsafeActivations = 1
	if err := VerifySkillOrchestratorChaosCertificate(first, publicKey); err == nil {
		t.Fatal("tampered chaos certificate was accepted")
	}
}

func TestSkillChaosCertificateRejectsMissingFailedDuplicateAndUnboundedCases(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	tests := []struct {
		name   string
		mutate func(*SkillChaosCertificationInput)
	}{
		{"missing", func(input *SkillChaosCertificationInput) {
			input.Observations = input.Observations[:len(input.Observations)-1]
		}},
		{"failed", func(input *SkillChaosCertificationInput) { input.Observations[0].Passed = false }},
		{"effect", func(input *SkillChaosCertificationInput) { input.Observations[0].DomainSideEffects = 2 }},
		{"unsafe_activation", func(input *SkillChaosCertificationInput) { input.Observations[0].UnsafeActivations = 1 }},
		{"duration", func(input *SkillChaosCertificationInput) { input.Observations[0].DurationMillis = 1_001 }},
		{"duplicate", func(input *SkillChaosCertificationInput) { input.Observations[1] = input.Observations[0] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeSkillChaosInput()
			test.mutate(&input)
			if _, err := CertifySkillOrchestratorChaos(input, "key", privateKey); err == nil {
				t.Fatal("unsafe chaos matrix was certified")
			}
		})
	}
}

func completeSkillChaosInput() SkillChaosCertificationInput {
	observations := make([]SkillChaosObservation, 0, len(RequiredSkillChaosCaseIDs())*2)
	for _, runtime := range []SkillChaosRuntime{SkillChaosHosted, SkillChaosStandalone} {
		for _, caseID := range RequiredSkillChaosCaseIDs() {
			observation := SkillChaosObservation{
				CaseID: caseID, Runtime: runtime, Passed: true, Converged: true,
				DomainSideEffects: 1, DurationMillis: 1,
			}
			if strings.HasPrefix(caseID, "crash_before:") {
				observation.Stage = core.SkillOrchestratorStage(strings.TrimPrefix(caseID, "crash_before:"))
				observation.FaultPoint = SkillChaosBeforeSideEffect
			}
			if strings.HasPrefix(caseID, "crash_after:") {
				observation.Stage = core.SkillOrchestratorStage(strings.TrimPrefix(caseID, "crash_after:"))
				observation.FaultPoint = SkillChaosAfterSideEffect
			}
			observations = append(observations, observation)
		}
	}
	return SkillChaosCertificationInput{
		ReleaseID: "release-2026-09-01", BuildDigest: "sha256:" + strings.Repeat("a", 64),
		MigrationDigest: "sha256:" + strings.Repeat("b", 64), GeneratedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		MaximumCaseDurationMS: 1_000, Observations: observations,
	}
}
