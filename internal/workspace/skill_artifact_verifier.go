package workspace

import (
	"context"
	"errors"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillArtifactVerifier struct {
	bundles      *RevisionBundleStore
	materializer *SkillMaterializer
}

func NewSkillArtifactVerifier(bundles *RevisionBundleStore, materializer *SkillMaterializer) (*SkillArtifactVerifier, error) {
	if bundles == nil || materializer == nil {
		return nil, errors.New("skill artifact verifier dependencies are required")
	}
	return &SkillArtifactVerifier{bundles: bundles, materializer: materializer}, nil
}

func (v *SkillArtifactVerifier) VerifyActive(ctx context.Context, skill core.LogicalSkill, revision core.SkillRevision) error {
	if v == nil {
		return errors.New("skill artifact verifier is required")
	}
	return v.materializer.VerifyActive(ctx, skill, revision)
}

func (v *SkillArtifactVerifier) VerifyImmutable(ctx context.Context, revision core.SkillRevision) error {
	if v == nil {
		return errors.New("skill artifact verifier is required")
	}
	return v.bundles.VerifyImmutable(ctx, revision)
}
