package application

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	SkillChaosCertificateSchemaV1 = "agent-memory/skill-orchestrator-chaos-certificate/v1"
	MaxSkillChaosObservations     = 128
)

type SkillChaosRuntime = core.SkillChaosRuntime
type SkillChaosFaultPoint = core.SkillChaosFaultPoint
type SkillChaosObservation = core.SkillChaosObservation

const (
	SkillChaosStandalone       = core.SkillChaosStandalone
	SkillChaosHosted           = core.SkillChaosHosted
	SkillChaosBeforeSideEffect = core.SkillChaosBeforeSideEffect
	SkillChaosAfterSideEffect  = core.SkillChaosAfterSideEffect
)

var ErrSkillChaosInjected = errors.New("injected skill orchestrator crash")

type SkillChaosCertificationInput struct {
	ReleaseID             string                  `json:"release_id"`
	BuildDigest           string                  `json:"build_digest"`
	MigrationDigest       string                  `json:"migration_digest"`
	GeneratedAt           time.Time               `json:"generated_at"`
	MaximumCaseDurationMS int64                   `json:"maximum_case_duration_ms"`
	Observations          []SkillChaosObservation `json:"observations"`
}

type SkillChaosCertificate struct {
	Schema                string                  `json:"schema"`
	ReleaseID             string                  `json:"release_id"`
	BuildDigest           string                  `json:"build_digest"`
	MigrationDigest       string                  `json:"migration_digest"`
	GeneratedAt           time.Time               `json:"generated_at"`
	MaximumCaseDurationMS int64                   `json:"maximum_case_duration_ms"`
	Observations          []SkillChaosObservation `json:"observations"`
	ReportDigest          string                  `json:"report_digest"`
	SigningKeyID          string                  `json:"signing_key_id"`
	Signature             string                  `json:"signature"`
}

type skillChaosUnsignedCertificate struct {
	Schema                string                  `json:"schema"`
	ReleaseID             string                  `json:"release_id"`
	BuildDigest           string                  `json:"build_digest"`
	MigrationDigest       string                  `json:"migration_digest"`
	GeneratedAt           time.Time               `json:"generated_at"`
	MaximumCaseDurationMS int64                   `json:"maximum_case_duration_ms"`
	Observations          []SkillChaosObservation `json:"observations"`
	SigningKeyID          string                  `json:"signing_key_id"`
}

func RequiredSkillChaosCaseIDs() []string {
	return core.RequiredSkillChaosCaseIDs()
}

func CertifySkillOrchestratorChaos(input SkillChaosCertificationInput, keyID string, privateKey ed25519.PrivateKey) (SkillChaosCertificate, error) {
	if strings.TrimSpace(input.ReleaseID) == "" || !validSHA256Digest(input.BuildDigest) || !validSHA256Digest(input.MigrationDigest) || input.GeneratedAt.IsZero() {
		return SkillChaosCertificate{}, errors.New("bounded chaos certificate identity and provenance are required")
	}
	if strings.TrimSpace(keyID) == "" || len(keyID) > 128 || len(privateKey) != ed25519.PrivateKeySize {
		return SkillChaosCertificate{}, errors.New("valid chaos certificate signing identity is required")
	}
	if input.MaximumCaseDurationMS < 1 || input.MaximumCaseDurationMS > int64((10*time.Minute)/time.Millisecond) {
		return SkillChaosCertificate{}, errors.New("chaos case duration bound is invalid")
	}
	if err := validateSkillChaosMatrix(input.Observations, input.MaximumCaseDurationMS); err != nil {
		return SkillChaosCertificate{}, err
	}
	observations := append([]SkillChaosObservation(nil), input.Observations...)
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].Runtime != observations[j].Runtime {
			return observations[i].Runtime < observations[j].Runtime
		}
		return observations[i].CaseID < observations[j].CaseID
	})
	unsigned := skillChaosUnsignedCertificate{
		Schema: SkillChaosCertificateSchemaV1, ReleaseID: input.ReleaseID,
		BuildDigest: input.BuildDigest, MigrationDigest: input.MigrationDigest,
		GeneratedAt: input.GeneratedAt.UTC(), MaximumCaseDurationMS: input.MaximumCaseDurationMS,
		Observations: observations, SigningKeyID: keyID,
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return SkillChaosCertificate{}, err
	}
	digest := sha256.Sum256(payload)
	return SkillChaosCertificate{
		Schema: unsigned.Schema, ReleaseID: unsigned.ReleaseID, BuildDigest: unsigned.BuildDigest,
		MigrationDigest: unsigned.MigrationDigest, GeneratedAt: unsigned.GeneratedAt,
		MaximumCaseDurationMS: unsigned.MaximumCaseDurationMS, Observations: observations,
		ReportDigest: "sha256:" + hex.EncodeToString(digest[:]), SigningKeyID: keyID,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
	}, nil
}

func validateSkillChaosMatrix(observations []SkillChaosObservation, maximumCaseDurationMS int64) error {
	want := RequiredSkillChaosCaseIDs()
	if len(observations) != len(want)*2 || len(observations) > MaxSkillChaosObservations {
		return errors.New("complete standalone and hosted chaos observations are required")
	}
	required := make(map[string]struct{}, len(want)*2)
	for _, runtime := range []SkillChaosRuntime{SkillChaosStandalone, SkillChaosHosted} {
		for _, caseID := range want {
			required[string(runtime)+"\x00"+caseID] = struct{}{}
		}
	}
	for _, observation := range observations {
		key := string(observation.Runtime) + "\x00" + observation.CaseID
		if _, ok := required[key]; !ok {
			return fmt.Errorf("unexpected or duplicate chaos case %q", observation.CaseID)
		}
		delete(required, key)
		if !observation.Passed || !observation.Converged || observation.DomainSideEffects < 0 || observation.DomainSideEffects > 1 || observation.UnsafeActivations != 0 || observation.DurationMillis < 0 || observation.DurationMillis > maximumCaseDurationMS {
			return fmt.Errorf("unsafe chaos observation %q", observation.CaseID)
		}
		if err := validateSkillChaosCaseBinding(observation); err != nil {
			return err
		}
	}
	if len(required) != 0 {
		return errors.New("chaos observation matrix is incomplete")
	}
	return nil
}

func validateSkillChaosCaseBinding(observation SkillChaosObservation) error {
	for _, prefix := range []struct {
		value string
		point SkillChaosFaultPoint
	}{{"crash_before:", SkillChaosBeforeSideEffect}, {"crash_after:", SkillChaosAfterSideEffect}} {
		if strings.HasPrefix(observation.CaseID, prefix.value) {
			stage := core.SkillOrchestratorStage(strings.TrimPrefix(observation.CaseID, prefix.value))
			if !stage.Valid() || observation.Stage != stage || observation.FaultPoint != prefix.point {
				return fmt.Errorf("chaos case binding is invalid for %q", observation.CaseID)
			}
			return nil
		}
	}
	if observation.Stage != "" || observation.FaultPoint != "" {
		return fmt.Errorf("general chaos case %q has a stage binding", observation.CaseID)
	}
	return nil
}

func VerifySkillOrchestratorChaosCertificate(certificate SkillChaosCertificate, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize || certificate.Schema != SkillChaosCertificateSchemaV1 {
		return errors.New("valid chaos certificate trust material is required")
	}
	if strings.TrimSpace(certificate.ReleaseID) == "" || !validSHA256Digest(certificate.BuildDigest) || !validSHA256Digest(certificate.MigrationDigest) || certificate.GeneratedAt.IsZero() || strings.TrimSpace(certificate.SigningKeyID) == "" || certificate.MaximumCaseDurationMS < 1 || certificate.MaximumCaseDurationMS > int64((10*time.Minute)/time.Millisecond) {
		return errors.New("chaos certificate provenance or bounds are invalid")
	}
	if err := validateSkillChaosMatrix(certificate.Observations, certificate.MaximumCaseDurationMS); err != nil {
		return err
	}
	unsigned := skillChaosUnsignedCertificate{
		Schema: certificate.Schema, ReleaseID: certificate.ReleaseID, BuildDigest: certificate.BuildDigest,
		MigrationDigest: certificate.MigrationDigest, GeneratedAt: certificate.GeneratedAt.UTC(),
		MaximumCaseDurationMS: certificate.MaximumCaseDurationMS,
		Observations:          certificate.Observations, SigningKeyID: certificate.SigningKeyID,
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if certificate.ReportDigest != "sha256:"+hex.EncodeToString(digest[:]) {
		return errors.New("chaos certificate digest mismatch")
	}
	signature, err := base64.StdEncoding.DecodeString(certificate.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("chaos certificate signature is invalid")
	}
	return nil
}

type SkillChaosFaultAdapter struct {
	delegate SkillStageAdapter
	point    SkillChaosFaultPoint
	mu       sync.Mutex
	fired    map[string]struct{}
}

func NewSkillChaosFaultAdapter(delegate SkillStageAdapter, point SkillChaosFaultPoint) (*SkillChaosFaultAdapter, error) {
	if delegate == nil || point != SkillChaosBeforeSideEffect && point != SkillChaosAfterSideEffect {
		return nil, errors.New("valid chaos delegate and fault point are required")
	}
	return &SkillChaosFaultAdapter{delegate: delegate, point: point, fired: make(map[string]struct{})}, nil
}

func (a *SkillChaosFaultAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.delegate == nil {
		return SkillStageResult{}, errors.New("chaos adapter is not configured")
	}
	key := job.ID + "\x00" + string(a.point)
	if a.point == SkillChaosBeforeSideEffect && a.injectOnce(key) {
		return SkillStageResult{}, ErrSkillChaosInjected
	}
	result, err := a.delegate.Execute(ctx, job)
	if err != nil {
		return result, err
	}
	if a.point == SkillChaosAfterSideEffect && a.injectOnce(key) {
		return SkillStageResult{}, ErrSkillChaosInjected
	}
	return result, nil
}

func (a *SkillChaosFaultAdapter) injectOnce(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.fired[key]; exists {
		return false
	}
	a.fired[key] = struct{}{}
	return true
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
