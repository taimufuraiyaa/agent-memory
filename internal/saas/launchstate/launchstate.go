// Package launchstate collects content-free evidence that one deployed staging
// platform remains in its fail-closed pre-customer launch state.
package launchstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

const (
	ReceiptSchemaV1     = "agent-memory-staging-safe-platform-launch-state-v1"
	maximumReceiptBytes = 64 << 10
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

type PolicyState struct {
	Phase              string
	SignupEnabled      bool
	InvitationRequired bool
	PolicyVersion      string
	UpdatedAt          time.Time
}

type Receipt struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	Environment            string    `json:"environment"`
	InventoryID            string    `json:"inventory_id"`
	InventoryReceiptSHA256 string    `json:"inventory_receipt_sha256"`
	PlanID                 string    `json:"plan_id"`
	PlanReceiptSHA256      string    `json:"plan_receipt_sha256"`
	ChangeID               string    `json:"change_id"`
	ChangeReceiptSHA256    string    `json:"change_receipt_sha256"`
	ReleaseID              string    `json:"release_id"`
	ReleaseReceiptSHA256   string    `json:"release_receipt_sha256"`
	CollectedAt            time.Time `json:"collected_at"`
	Phase                  string    `json:"phase"`
	SignupEnabled          bool      `json:"signup_enabled"`
	InvitationRequired     bool      `json:"invitation_required"`
	PolicyVersion          string    `json:"policy_version"`
	PolicyUpdatedAt        time.Time `json:"policy_updated_at"`
	Ready                  bool      `json:"ready"`
}

type chain struct {
	inventory     platforminventory.Inventory
	plan          platformplan.Plan
	change        platformchange.Receipt
	release       platformrollback.ReleaseReceipt
	releaseDigest string
}

func Collect(ctx context.Context, inventoryPath, planPath, changePath, releasePath, connectionURL string, now time.Time) (Receipt, error) {
	evidence, err := loadChain(inventoryPath, planPath, changePath, releasePath)
	if err != nil {
		return Receipt{}, err
	}
	if strings.TrimSpace(connectionURL) == "" {
		return Receipt{}, errors.New("PostgreSQL configuration is required")
	}
	pool, err := postgres.Open(ctx, connectionURL)
	if err != nil {
		return Receipt{}, err
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Receipt{}, fmt.Errorf("begin read-only launch-policy observation: %w", err)
	}
	defer tx.Rollback(context.Background())
	var policy PolicyState
	err = tx.QueryRow(ctx, `SELECT phase, signup_enabled, invitation_required, policy_version, updated_at
		FROM saas_launch_policy WHERE singleton = true`).Scan(
		&policy.Phase, &policy.SignupEnabled, &policy.InvitationRequired, &policy.PolicyVersion, &policy.UpdatedAt,
	)
	if err != nil {
		return Receipt{}, fmt.Errorf("read singleton launch policy: %w", err)
	}
	return build(evidence.inventory, evidence.plan, evidence.change, evidence.release, evidence.releaseDigest, policy, now)
}

// CollectEvidence validates the complete file chain and combines it with an
// already-observed policy. It supports deterministic tests without weakening
// the production collector's read-only database transaction.
func CollectEvidence(inventoryPath, planPath, changePath, releasePath string, policy PolicyState, now time.Time) (Receipt, error) {
	evidence, err := loadChain(inventoryPath, planPath, changePath, releasePath)
	if err != nil {
		return Receipt{}, err
	}
	return build(evidence.inventory, evidence.plan, evidence.change, evidence.release, evidence.releaseDigest, policy, now)
}

func loadChain(inventoryPath, planPath, changePath, releasePath string) (chain, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return chain{}, fmt.Errorf("load self-managed platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return chain{}, fmt.Errorf("load self-managed infrastructure plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return chain{}, fmt.Errorf("load self-managed infrastructure change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return chain{}, fmt.Errorf("load passed staging release: %w", err)
	}
	return chain{inventory: inventory, plan: plan, change: change, release: release, releaseDigest: releaseDigest}, nil
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, policy PolicyState, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging ||
		plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID ||
		plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID ||
		change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 ||
		!platformchange.Assess(change).Ready || !digestPattern.MatchString(inventory.ReceiptSHA256) ||
		!digestPattern.MatchString(plan.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("safe-platform launch-state chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" ||
		!opaquePattern.MatchString(release.ReleaseID) || release.Outcome != "passed" || release.Migration.Outcome != "complete" ||
		release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || release.StartedAt.IsZero() ||
		release.CompletedAt.Before(release.StartedAt) || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("safe-platform staging release is invalid or unready")
	}
	if now.IsZero() || now.Before(change.GeneratedAt) || now.Before(release.CompletedAt) {
		return Receipt{}, errors.New("safe-platform launch-state collection time is invalid")
	}
	now = now.UTC()
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	if !validPhase(policy.Phase) || !versionPattern.MatchString(policy.PolicyVersion) || policy.UpdatedAt.IsZero() || policy.UpdatedAt.After(now) {
		return Receipt{}, errors.New("installed launch policy is malformed")
	}
	ready := policy.Phase == "internal_alpha" && !policy.SignupEnabled && policy.InvitationRequired
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: "staging_external", Environment: "staging",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256,
		ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256,
		ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, CollectedAt: now,
		Phase: policy.Phase, SignupEnabled: policy.SignupEnabled, InvitationRequired: policy.InvitationRequired,
		PolicyVersion: policy.PolicyVersion, PolicyUpdatedAt: policy.UpdatedAt, Ready: ready,
	}, nil
}

func validPhase(phase string) bool {
	switch phase {
	case "internal_alpha", "private_beta", "public_beta", "ga":
		return true
	default:
		return false
	}
}

func Load(path string) (Receipt, error) {
	var receipt Receipt
	if _, err := decodeStrictRegular(path, &receipt); err != nil {
		return Receipt{}, err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "staging_external" || receipt.Environment != "staging" ||
		!opaquePattern.MatchString(receipt.InventoryID) || !opaquePattern.MatchString(receipt.PlanID) || !opaquePattern.MatchString(receipt.ChangeID) ||
		!opaquePattern.MatchString(receipt.ReleaseID) || !digestPattern.MatchString(receipt.InventoryReceiptSHA256) ||
		!digestPattern.MatchString(receipt.PlanReceiptSHA256) || !digestPattern.MatchString(receipt.ChangeReceiptSHA256) ||
		!digestPattern.MatchString(receipt.ReleaseReceiptSHA256) || receipt.CollectedAt.IsZero() || !validPhase(receipt.Phase) ||
		!versionPattern.MatchString(receipt.PolicyVersion) || receipt.PolicyUpdatedAt.IsZero() || receipt.PolicyUpdatedAt.After(receipt.CollectedAt) ||
		receipt.Ready != (receipt.Phase == "internal_alpha" && !receipt.SignupEnabled && receipt.InvitationRequired) {
		return Receipt{}, errors.New("safe-platform launch-state receipt is invalid")
	}
	return receipt, nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("safe-platform launch-state receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("safe-platform launch-state receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect safe-platform launch-state receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-launch-state-*")
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("safe-platform launch-state path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumReceiptBytes {
		return "", errors.New("safe-platform launch-state must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open safe-platform launch-state")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("safe-platform launch-state changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumReceiptBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumReceiptBytes {
		return "", errors.New("read safe-platform launch-state")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("safe-platform launch-state JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("safe-platform launch-state contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("safe-platform launch-state changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("safe-platform launch-state changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
