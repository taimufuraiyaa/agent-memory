// Package retentioninventory collects a content-free inventory of the active
// retention policies installed on an applied self-managed platform.
package retentioninventory

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
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
)

const ReceiptSchemaV1 = "agent-memory-self-managed-retention-inventory-v1"

const maximumReceiptBytes = 128 << 10

var (
	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

type Policy struct {
	DataClass       string    `json:"data_class"`
	Purpose         string    `json:"purpose"`
	PolicyVersion   string    `json:"policy_version"`
	Owner           string    `json:"owner"`
	Trigger         string    `json:"trigger"`
	DurationSeconds int64     `json:"duration_seconds"`
	DeletionMethod  string    `json:"deletion_method"`
	HoldBehavior    string    `json:"hold_behavior"`
	MigrationPlan   string    `json:"migration_plan"`
	CustomerImpact  string    `json:"customer_impact"`
	EffectiveAt     time.Time `json:"effective_at"`
}

type Receipt struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	Ready                  bool      `json:"ready"`
	Environment            string    `json:"environment"`
	InventoryID            string    `json:"inventory_id"`
	InventoryReceiptSHA256 string    `json:"inventory_receipt_sha256"`
	ChangeID               string    `json:"change_id"`
	ChangeReceiptSHA256    string    `json:"change_receipt_sha256"`
	CollectedAt            time.Time `json:"collected_at"`
	PolicyCount            int       `json:"policy_count"`
	PoliciesSHA256         string    `json:"policies_sha256"`
	Policies               []Policy  `json:"policies"`
	ReceiptSHA256          string    `json:"-"`
}

func Load(path string) (Receipt, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, fmt.Errorf("load retention inventory receipt: %w", err)
	}
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	receipt.ReceiptSHA256 = digest
	return receipt, nil
}

func Collect(ctx context.Context, inventoryPath, planPath, changePath, connectionURL string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed infrastructure plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed infrastructure change: %w", err)
	}
	if strings.TrimSpace(connectionURL) == "" {
		return Receipt{}, errors.New("PostgreSQL configuration is required")
	}
	pool, err := postgres.Open(ctx, connectionURL)
	if err != nil {
		return Receipt{}, err
	}
	defer pool.Close()
	policies, err := retention.NewRegistry(pool).ListActive(ctx)
	if err != nil {
		return Receipt{}, fmt.Errorf("list active retention policies: %w", err)
	}
	return Build(inventory, change, policies, now)
}

func Build(inventory platforminventory.Inventory, change platformchange.Receipt, policies []retention.Policy, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || change.Schema != platformchange.SchemaV1 ||
		change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID ||
		change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || inventory.ReceiptSHA256 == "" || change.ReceiptSHA256 == "" {
		return Receipt{}, errors.New("retention inventory platform binding is invalid")
	}
	if !platformchange.Assess(change).Ready {
		return Receipt{}, errors.New("self-managed infrastructure change is not ready")
	}
	if now.IsZero() || now.Before(change.GeneratedAt) {
		return Receipt{}, errors.New("retention inventory collection time is invalid")
	}
	now = now.UTC()
	if err := retention.ValidatePolicies(policies, now); err != nil {
		return Receipt{}, err
	}
	ordered := append([]retention.Policy(nil), policies...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].DataClass < ordered[right].DataClass })
	normalized := make([]Policy, 0, len(ordered))
	for _, policy := range ordered {
		if policy.Duration%time.Second != 0 {
			return Receipt{}, fmt.Errorf("retention policy duration is not whole seconds for %q", policy.DataClass)
		}
		normalized = append(normalized, Policy{
			DataClass: policy.DataClass, Purpose: policy.Purpose, PolicyVersion: policy.Version,
			Owner: policy.Owner, Trigger: policy.Trigger, DurationSeconds: int64(policy.Duration / time.Second),
			DeletionMethod: policy.DeletionMethod, HoldBehavior: policy.HoldBehavior,
			MigrationPlan: policy.MigrationPlan, CustomerImpact: policy.CustomerImpact,
			EffectiveAt: policy.EffectiveAt.UTC(),
		})
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return Receipt{}, errors.New("encode canonical retention policies")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: "self_managed_external", Ready: true,
		Environment: string(inventory.Environment), InventoryID: inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256, ChangeID: change.ChangeID,
		ChangeReceiptSHA256: change.ReceiptSHA256, CollectedAt: now,
		PolicyCount: len(normalized), PoliciesSHA256: fmt.Sprintf("%x", sha256.Sum256(canonical)), Policies: normalized,
	}, nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("retention inventory receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("retention inventory receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect retention inventory receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-retention-inventory-*")
}

func validateReceipt(receipt Receipt) error {
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "self_managed_external" || !receipt.Ready ||
		(receipt.Environment != string(platforminventory.Staging) && receipt.Environment != string(platforminventory.Production)) ||
		!opaquePattern.MatchString(receipt.InventoryID) || !opaquePattern.MatchString(receipt.ChangeID) ||
		!digestPattern.MatchString(receipt.InventoryReceiptSHA256) || !digestPattern.MatchString(receipt.ChangeReceiptSHA256) ||
		receipt.CollectedAt.IsZero() || receipt.PolicyCount != len(retention.DataClasses) || len(receipt.Policies) != len(retention.DataClasses) ||
		!digestPattern.MatchString(receipt.PoliciesSHA256) {
		return errors.New("retention inventory receipt identity is invalid")
	}
	policies := make([]retention.Policy, 0, len(receipt.Policies))
	for index, policy := range receipt.Policies {
		if index > 0 && receipt.Policies[index-1].DataClass >= policy.DataClass {
			return errors.New("retention inventory policies are not in canonical order")
		}
		if policy.DurationSeconds < 0 || policy.DurationSeconds > int64((1<<63-1)/int64(time.Second)) {
			return fmt.Errorf("retention inventory duration is invalid for %q", policy.DataClass)
		}
		policies = append(policies, retention.Policy{
			DataClass: policy.DataClass, Purpose: policy.Purpose, Version: policy.PolicyVersion,
			Owner: policy.Owner, Trigger: policy.Trigger, Duration: time.Duration(policy.DurationSeconds) * time.Second,
			DeletionMethod: policy.DeletionMethod, HoldBehavior: policy.HoldBehavior,
			MigrationPlan: policy.MigrationPlan, CustomerImpact: policy.CustomerImpact,
			EffectiveAt: policy.EffectiveAt,
		})
	}
	if err := retention.ValidatePolicies(policies, receipt.CollectedAt.UTC()); err != nil {
		return err
	}
	canonical, err := json.Marshal(receipt.Policies)
	if err != nil {
		return errors.New("encode retention inventory policies")
	}
	if actual := fmt.Sprintf("%x", sha256.Sum256(canonical)); receipt.PoliciesSHA256 != actual {
		return errors.New("retention inventory policy digest is invalid")
	}
	return nil
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("retention inventory path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumReceiptBytes {
		return "", errors.New("retention inventory must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open retention inventory")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("retention inventory changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumReceiptBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumReceiptBytes {
		return "", errors.New("read retention inventory")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("retention inventory JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("retention inventory contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retention inventory changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retention inventory changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
