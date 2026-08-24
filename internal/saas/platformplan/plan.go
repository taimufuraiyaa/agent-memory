// Package platformplan validates sanitized self-managed infrastructure plans.
package platformplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	SchemaV1         = "agent-memory-self-managed-infrastructure-plan-v1"
	maximumPlanBytes = 512 << 10
)

type CapabilityID string
type Action string

const (
	CapabilityEdgeIngress        CapabilityID = "edge_ingress"
	CapabilityApplicationNetwork CapabilityID = "application_network"
	CapabilityDataNetwork        CapabilityID = "data_network"
	CapabilityKubernetesCluster  CapabilityID = "kubernetes_cluster"
	CapabilityOIDCIdentity       CapabilityID = "oidc_identity"
	CapabilityPostgres           CapabilityID = "postgres"
	CapabilityQuarantineBucket   CapabilityID = "quarantine_bucket"
	CapabilityVaultBucket        CapabilityID = "vault_bucket"
	CapabilityExportBucket       CapabilityID = "export_bucket"
	CapabilityDurableQueue       CapabilityID = "durable_queue"
	CapabilityAPIIdentity        CapabilityID = "api_identity"
	CapabilityWorkerIdentity     CapabilityID = "worker_identity"
	CapabilityReconcilerIdentity CapabilityID = "reconciler_identity"
	CapabilityMigrationIdentity  CapabilityID = "migration_identity"
	CapabilityAPISecret          CapabilityID = "api_secret"
	CapabilityWorkerSecret       CapabilityID = "worker_secret"
	CapabilityReconcilerSecret   CapabilityID = "reconciler_secret"
	CapabilityMigrationSecret    CapabilityID = "migration_secret"
	CapabilityTelemetry          CapabilityID = "telemetry"
	CapabilityPostgresBackup     CapabilityID = "postgres_backup"
	CapabilityObjectBackup       CapabilityID = "object_backup"

	ActionNoChange Action = "no_change"
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionReplace  Action = "replace"
	ActionDelete   Action = "delete"
)

var requiredCapabilityIDs = []CapabilityID{
	CapabilityEdgeIngress,
	CapabilityApplicationNetwork,
	CapabilityDataNetwork,
	CapabilityKubernetesCluster,
	CapabilityOIDCIdentity,
	CapabilityPostgres,
	CapabilityQuarantineBucket,
	CapabilityVaultBucket,
	CapabilityExportBucket,
	CapabilityDurableQueue,
	CapabilityAPIIdentity,
	CapabilityWorkerIdentity,
	CapabilityReconcilerIdentity,
	CapabilityMigrationIdentity,
	CapabilityAPISecret,
	CapabilityWorkerSecret,
	CapabilityReconcilerSecret,
	CapabilityMigrationSecret,
	CapabilityTelemetry,
	CapabilityPostgresBackup,
	CapabilityObjectBackup,
}

var (
	opaqueIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	revisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	versionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Plan struct {
	Schema                 string                        `json:"schema"`
	Environment            platforminventory.Environment `json:"environment"`
	PlanID                 string                        `json:"plan_id"`
	InventoryID            string                        `json:"inventory_id"`
	InventoryReceiptSHA256 string                        `json:"inventory_receipt_sha256"`
	GeneratedAt            time.Time                     `json:"generated_at"`
	SourceRevision         string                        `json:"source_revision"`
	SourceBundleSHA256     string                        `json:"source_bundle_sha256"`
	RawPlanSHA256          string                        `json:"raw_plan_sha256"`
	Toolchain              []Tool                        `json:"toolchain"`
	Capabilities           []Capability                  `json:"capabilities"`
	ReceiptSHA256          string                        `json:"-"`
}

type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Capability struct {
	ID               CapabilityID `json:"id"`
	Action           Action       `json:"action"`
	FailureDomainIDs []string     `json:"failure_domain_ids"`
	PublicIngress    bool         `json:"public_ingress"`
}

type Assessment struct {
	Ready           bool
	CapabilityCount int
	ToolCount       int
	ActionCounts    map[Action]int
}

func Load(path string, inventory platforminventory.Inventory) (Plan, error) {
	var plan Plan
	digest, err := decodeStrictRegular(path, &plan)
	if err != nil {
		return Plan{}, fmt.Errorf("load infrastructure plan: %w", err)
	}
	plan.ReceiptSHA256 = digest
	if err := validate(plan, inventory); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func Assess(plan Plan) Assessment {
	assessment := Assessment{
		Ready:           true,
		CapabilityCount: len(plan.Capabilities),
		ToolCount:       len(plan.Toolchain),
		ActionCounts:    make(map[Action]int, 5),
	}
	for _, capability := range plan.Capabilities {
		assessment.ActionCounts[capability.Action]++
		if capability.Action == ActionReplace || capability.Action == ActionDelete {
			assessment.Ready = false
		}
	}
	return assessment
}

func validate(plan Plan, inventory platforminventory.Inventory) error {
	if plan.Schema != SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 {
		return errors.New("infrastructure plan inventory binding is invalid")
	}
	if !opaqueIDPattern.MatchString(plan.PlanID) || !opaqueIDPattern.MatchString(plan.InventoryID) || plan.GeneratedAt.IsZero() || plan.GeneratedAt.Before(inventory.GeneratedAt) {
		return errors.New("infrastructure plan identity is invalid")
	}
	if !revisionPattern.MatchString(plan.SourceRevision) || !digestPattern.MatchString(plan.SourceBundleSHA256) || !digestPattern.MatchString(plan.RawPlanSHA256) {
		return errors.New("infrastructure plan source binding is invalid")
	}
	if err := validateToolchain(plan.Toolchain); err != nil {
		return err
	}
	return validateCapabilities(plan.Environment, plan.Capabilities, inventory.FailureDomains)
}

func validateToolchain(toolchain []Tool) error {
	if len(toolchain) < 1 || len(toolchain) > 8 {
		return errors.New("infrastructure plan toolchain is invalid")
	}
	allowed := map[string]bool{
		"ansible": true, "custom": true, "helm": true, "kustomize": true,
		"opentofu": true, "pulumi": true, "terraform": true,
	}
	seen := make(map[string]struct{}, len(toolchain))
	for _, tool := range toolchain {
		if !allowed[tool.Name] || !versionPattern.MatchString(tool.Version) {
			return errors.New("infrastructure plan tool is invalid")
		}
		if _, duplicate := seen[tool.Name]; duplicate {
			return errors.New("infrastructure plan tool is duplicated")
		}
		seen[tool.Name] = struct{}{}
	}
	return nil
}

func validateCapabilities(environment platforminventory.Environment, capabilities []Capability, inventoryDomains []platforminventory.FailureDomain) error {
	if len(capabilities) != len(requiredCapabilityIDs) {
		return errors.New("infrastructure plan capability set is incomplete")
	}
	allowed := make(map[CapabilityID]struct{}, len(requiredCapabilityIDs))
	for _, id := range requiredCapabilityIDs {
		allowed[id] = struct{}{}
	}
	domains := make(map[string]struct{}, len(inventoryDomains))
	for _, domain := range inventoryDomains {
		domains[domain.ID] = struct{}{}
	}
	minimumDomains := 1
	if environment == platforminventory.Production {
		minimumDomains = 2
	}
	seen := make(map[CapabilityID]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if _, ok := allowed[capability.ID]; !ok {
			return errors.New("infrastructure plan capability is unknown")
		}
		if _, duplicate := seen[capability.ID]; duplicate {
			return errors.New("infrastructure plan capability is duplicated")
		}
		seen[capability.ID] = struct{}{}
		if !validAction(capability.Action) {
			return errors.New("infrastructure plan capability action is invalid")
		}
		if capability.PublicIngress && capability.ID != CapabilityEdgeIngress && capability.ID != CapabilityOIDCIdentity {
			return errors.New("infrastructure plan exposes a private capability publicly")
		}
		if len(capability.FailureDomainIDs) < minimumDomains || len(capability.FailureDomainIDs) > len(domains) {
			return errors.New("infrastructure plan capability redundancy is invalid")
		}
		capabilityDomains := make(map[string]struct{}, len(capability.FailureDomainIDs))
		for _, domainID := range capability.FailureDomainIDs {
			if _, exists := domains[domainID]; !exists {
				return errors.New("infrastructure plan capability references an unknown failure domain")
			}
			if _, duplicate := capabilityDomains[domainID]; duplicate {
				return errors.New("infrastructure plan capability failure domain is duplicated")
			}
			capabilityDomains[domainID] = struct{}{}
		}
	}
	return nil
}

func validAction(action Action) bool {
	return action == ActionNoChange || action == ActionCreate || action == ActionUpdate || action == ActionReplace || action == ActionDelete
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("plan path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumPlanBytes {
		return "", errors.New("plan must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open plan")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("plan changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumPlanBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumPlanBytes {
		return "", errors.New("read plan")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("plan JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("plan contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("plan changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("plan changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
