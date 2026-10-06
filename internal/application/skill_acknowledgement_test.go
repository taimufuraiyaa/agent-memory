package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillAcknowledgementServiceAcknowledgesAndReplays(t *testing.T) {
	fixture := newSkillAcknowledgementFixture()
	input := fixture.input()
	ack, err := fixture.service.Acknowledge(context.Background(), input)
	if err != nil || ack.RevisionDigest != fixture.resolution.Digest {
		t.Fatalf("acknowledgement = %+v, %v", ack, err)
	}
	replayed, err := fixture.service.Acknowledge(context.Background(), input)
	if err != nil || replayed.AcknowledgedAt != ack.AcknowledgedAt || fixture.repository.writes != 1 {
		t.Fatalf("replay = %+v, writes %d, err %v", replayed, fixture.repository.writes, err)
	}
}

func TestSkillAcknowledgementServiceRejectsExpiryAndReplayMismatch(t *testing.T) {
	fixture := newSkillAcknowledgementFixture()
	fixture.now = fixture.resolution.ExpiresAt.Add(time.Second)
	fixture.service.now = func() time.Time { return fixture.now }
	if _, err := fixture.service.Acknowledge(context.Background(), fixture.input()); err == nil {
		t.Fatal("expired acknowledgement token was accepted")
	}

	fixture = newSkillAcknowledgementFixture()
	if _, err := fixture.service.Acknowledge(context.Background(), fixture.input()); err != nil {
		t.Fatal(err)
	}
	input := fixture.input()
	input.Digest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := fixture.service.Acknowledge(context.Background(), input); err == nil {
		t.Fatal("mismatched acknowledgement replay was accepted")
	}
}

func TestSkillAcknowledgementServiceRejectsWrongScope(t *testing.T) {
	tests := []func(*SkillAcknowledgementInput){
		func(i *SkillAcknowledgementInput) { i.Workspace = "other" },
		func(i *SkillAcknowledgementInput) { i.PrincipalID = "other-agent" },
		func(i *SkillAcknowledgementInput) { i.TaskID = "other-task" },
		func(i *SkillAcknowledgementInput) { i.RevisionID = "other-revision" },
		func(i *SkillAcknowledgementInput) { i.Token = "wrong-token" },
	}
	for index, mutate := range tests {
		fixture := newSkillAcknowledgementFixture()
		input := fixture.input()
		mutate(&input)
		if _, err := fixture.service.Acknowledge(context.Background(), input); err == nil {
			t.Fatalf("wrong scope case %d was accepted", index)
		}
	}
}

type skillAcknowledgementRepository struct {
	resolution core.SkillResolution
	ack        core.SkillResolutionAcknowledgement
	writes     int
}

func (r *skillAcknowledgementRepository) GetSkillResolution(_ context.Context, workspace, resolutionID string) (core.SkillResolution, error) {
	if workspace != r.resolution.Workspace || resolutionID != r.resolution.ID {
		return core.SkillResolution{}, errors.New("resolution not found")
	}
	return r.resolution, nil
}

func (r *skillAcknowledgementRepository) GetSkillResolutionAcknowledgement(_ context.Context, workspace, resolutionID string) (core.SkillResolutionAcknowledgement, error) {
	if r.ack.ResolutionID == "" || workspace != r.ack.Workspace || resolutionID != r.ack.ResolutionID {
		return core.SkillResolutionAcknowledgement{}, sql.ErrNoRows
	}
	return r.ack, nil
}

func (r *skillAcknowledgementRepository) AcknowledgeSkillResolution(_ context.Context, acknowledgement core.SkillResolutionAcknowledgement) (core.SkillResolutionAcknowledgement, error) {
	if r.ack.ResolutionID != "" {
		return r.ack, nil
	}
	r.ack = acknowledgement
	r.writes++
	return acknowledgement, nil
}

type skillAcknowledgementFixture struct {
	repository *skillAcknowledgementRepository
	service    *SkillAcknowledgementService
	resolution core.SkillResolution
	token      string
	now        time.Time
}

func newSkillAcknowledgementFixture() skillAcknowledgementFixture {
	now := time.Date(2026, 8, 29, 22, 0, 0, 0, time.UTC)
	token := "acknowledgement-token"
	digest := sha256.Sum256([]byte(token))
	resolution := core.SkillResolution{ID: "resolution-1", Workspace: "ws", Environment: "local", PrincipalID: "agent-1", TaskID: "task-1", SkillID: "skill-1", RevisionID: "revision-2", RevisionNumber: 2, Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Reason: core.SkillResolutionActive, PolicyVersion: 1, AcknowledgementTokenHash: "sha256:" + hex.EncodeToString(digest[:]), ExpiresAt: now.Add(time.Minute), ResolvedAt: now}
	repository := &skillAcknowledgementRepository{resolution: resolution}
	service := NewSkillAcknowledgementService(repository, func() time.Time { return now.Add(10 * time.Second) })
	return skillAcknowledgementFixture{repository: repository, service: service, resolution: resolution, token: token, now: now.Add(10 * time.Second)}
}

func (f skillAcknowledgementFixture) input() SkillAcknowledgementInput {
	return SkillAcknowledgementInput{Workspace: f.resolution.Workspace, ResolutionID: f.resolution.ID, PrincipalID: f.resolution.PrincipalID, TaskID: f.resolution.TaskID, RevisionID: f.resolution.RevisionID, Digest: f.resolution.Digest, Token: f.token}
}
