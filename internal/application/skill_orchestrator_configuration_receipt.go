package application

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const SkillOrchestratorConfigurationReceiptSchemaV1 = "agent-memory/skill-orchestrator-configuration-receipt/v1"

type SkillOrchestratorConfigurationReceipt struct {
	Schema          string                              `json:"schema"`
	ReceiptID       string                              `json:"receipt_id"`
	ReleaseID       string                              `json:"release_id"`
	BuildDigest     string                              `json:"build_digest"`
	MigrationDigest string                              `json:"migration_digest"`
	Configuration   core.SkillOrchestratorConfiguration `json:"configuration"`
	SignerID        string                              `json:"signer_id"`
	SignedAt        time.Time                           `json:"signed_at"`
	SigningKeyID    string                              `json:"signing_key_id"`
	Signature       string                              `json:"signature"`
}

type skillOrchestratorConfigurationReceiptPayload struct {
	Schema          string                              `json:"schema"`
	ReceiptID       string                              `json:"receipt_id"`
	ReleaseID       string                              `json:"release_id"`
	BuildDigest     string                              `json:"build_digest"`
	MigrationDigest string                              `json:"migration_digest"`
	Configuration   core.SkillOrchestratorConfiguration `json:"configuration"`
	SignerID        string                              `json:"signer_id"`
	SignedAt        time.Time                           `json:"signed_at"`
	SigningKeyID    string                              `json:"signing_key_id"`
}

func SignSkillOrchestratorConfigurationReceipt(receipt SkillOrchestratorConfigurationReceipt, privateKey ed25519.PrivateKey) (SkillOrchestratorConfigurationReceipt, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return SkillOrchestratorConfigurationReceipt{}, errors.New("valid orchestrator configuration signing key is required")
	}
	payload, err := skillOrchestratorConfigurationReceiptBytes(receipt)
	if err != nil {
		return SkillOrchestratorConfigurationReceipt{}, err
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return receipt, nil
}

func VerifySkillOrchestratorConfigurationReceipt(receipt SkillOrchestratorConfigurationReceipt, trustedKeys map[string]ed25519.PublicKey) error {
	payload, err := skillOrchestratorConfigurationReceiptBytes(receipt)
	if err != nil {
		return err
	}
	if !verifyReleaseSignature(trustedKeys, receipt.SigningKeyID, receipt.Signature, payload) {
		return errors.New("orchestrator configuration receipt signature is invalid")
	}
	return nil
}

func skillOrchestratorConfigurationReceiptBytes(receipt SkillOrchestratorConfigurationReceipt) ([]byte, error) {
	if receipt.Schema != SkillOrchestratorConfigurationReceiptSchemaV1 || !boundedReleaseReference(receipt.ReceiptID) || !boundedReleaseReference(receipt.ReleaseID) || !boundedReleaseReference(receipt.SignerID) || !boundedReleaseReference(receipt.SigningKeyID) || !validSHA256Digest(receipt.BuildDigest) || !validSHA256Digest(receipt.MigrationDigest) || receipt.SignedAt.IsZero() {
		return nil, errors.New("orchestrator configuration receipt identity or provenance is invalid")
	}
	if err := receipt.Configuration.Validate(); err != nil {
		return nil, err
	}
	expectedDigest, err := ComputeSkillOrchestratorConfigurationDigest(receipt.Configuration)
	if err != nil {
		return nil, err
	}
	if receipt.Configuration.Digest != expectedDigest {
		return nil, errors.New("orchestrator configuration receipt digest is invalid")
	}
	if receipt.SignedAt.Before(receipt.Configuration.CreatedAt) {
		return nil, errors.New("orchestrator configuration receipt predates the configuration")
	}
	return json.Marshal(skillOrchestratorConfigurationReceiptPayload{
		Schema: receipt.Schema, ReceiptID: receipt.ReceiptID, ReleaseID: receipt.ReleaseID,
		BuildDigest: receipt.BuildDigest, MigrationDigest: receipt.MigrationDigest,
		Configuration: receipt.Configuration, SignerID: receipt.SignerID,
		SignedAt: receipt.SignedAt.UTC(), SigningKeyID: receipt.SigningKeyID,
	})
}
