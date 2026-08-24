package readingroom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type seminarRunner struct{ fail Role }

func (r seminarRunner) Run(ctx context.Context, input RoleRunInput) (RoleRunResult, error) {
	if input.Profile.Role == r.fail {
		return RoleRunResult{}, errors.New("role unavailable")
	}
	kind, form := input.Profile.AllowedOutputs[0], core.KnowledgeInsight
	if kind == ContributionQuestion {
		form = core.KnowledgeQuestion
	}
	now := time.Now().UTC()
	contribution := Contribution{ID: input.NodeID + ":1", Role: input.Profile.Role, ProfileID: input.Profile.ID, ProfileVersion: input.Profile.Version, Kind: kind, Statement: "Attributed " + input.NodeID + " contribution", Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: input.Profile.ID}, Form: form, Derivation: core.DerivationInterpreted}, Confidence: .7}
	return RoleRunResult{RunID: input.RunID, NodeID: input.NodeID, ProfileID: input.Profile.ID, ProfileVersion: input.Profile.Version, PacketFingerprint: input.EvidencePacketFingerprint, Contributions: []Contribution{contribution}, Model: ModelMetadata{Provider: "test", Model: "fake"}, StartedAt: now, FinishedAt: now}, nil
}
func TestSeminarRunsRolesAndReturnsLabeledPartialResult(t *testing.T) {
	packet := testPacket()
	packet.Profiles = SeminarProfiles()
	result, err := NewSeminar(seminarRunner{fail: RoleCritic}, NewVerifierGate("verifier", "v1", nil), nil).Run(context.Background(), "run", packet, 700)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SeminarPartial || result.RoleErrors[string(RoleCritic)] == "" || result.Synthesis == nil {
		t.Fatalf("partial work was lost or mislabeled: %+v", result)
	}
	if len(result.Synthesis.Contribution.Provenance.DerivedFrom) != len(result.Contributions) {
		t.Fatal("synthesis lineage incomplete")
	}
}
