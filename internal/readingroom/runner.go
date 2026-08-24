package readingroom

import (
	"context"
	"errors"
	"strings"
	"time"
)

type RoleRunInput struct {
	RunID                     string         `json:"run_id"`
	NodeID                    string         `json:"node_id"`
	Profile                   AgentProfile   `json:"profile"`
	EvidencePacketFingerprint string         `json:"evidence_packet_fingerprint"`
	Packet                    EvidencePacket `json:"packet"`
	MaxOutputTokens           int            `json:"max_output_tokens"`
}
type ModelMetadata struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Version  string `json:"version,omitempty"`
}
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}
type RoleRunResult struct {
	RunID             string         `json:"run_id"`
	NodeID            string         `json:"node_id"`
	ProfileID         string         `json:"profile_id"`
	ProfileVersion    string         `json:"profile_version"`
	PacketFingerprint string         `json:"packet_fingerprint"`
	Contributions     []Contribution `json:"contributions"`
	Model             ModelMetadata  `json:"model"`
	Tokens            TokenUsage     `json:"tokens"`
	StartedAt         time.Time      `json:"started_at"`
	FinishedAt        time.Time      `json:"finished_at"`
	Cancelled         bool           `json:"cancelled,omitempty"`
	Error             string         `json:"error,omitempty"`
}

func (r RoleRunResult) Validate(input RoleRunInput) error {
	if strings.TrimSpace(r.RunID) == "" || r.RunID != input.RunID || r.NodeID != input.NodeID || r.ProfileID != input.Profile.ID || r.ProfileVersion != input.Profile.Version || r.PacketFingerprint != input.EvidencePacketFingerprint {
		return errors.New("role result identity does not match input")
	}
	if r.StartedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) || r.Tokens.Input < 0 || r.Tokens.Output < 0 || strings.TrimSpace(r.Model.Provider) == "" || strings.TrimSpace(r.Model.Model) == "" {
		return errors.New("role result requires timing, non-negative usage, and model metadata")
	}
	for _, c := range r.Contributions {
		if err := c.Validate(input.Profile); err != nil {
			return err
		}
	}
	return nil
}

type RoleRunner interface {
	Run(context.Context, RoleRunInput) (RoleRunResult, error)
}
