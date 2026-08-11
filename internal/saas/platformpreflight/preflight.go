// Package platformpreflight produces content-free Kubernetes readiness receipts.
package platformpreflight

import (
	"bytes"
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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	ReceiptSchemaV1     = "agent-memory-kubernetes-platform-preflight-receipt-v1"
	maximumReceiptBytes = 64 << 10
)

type Outcome string

const (
	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var requiredCheckIDs = []string{
	"namespace",
	"service_accounts",
	"secret_contracts",
	"network_policy",
	"private_service",
	"workload_identity",
	"immutable_images",
	"ready_workloads",
}

var (
	opaqueIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	contextPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)
	immutableImagePattern = regexp.MustCompile(`^[^\s@]+(?:/[^\s@]+)*@sha256:[a-f0-9]{64}$`)
	digestPattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Workload struct {
	ServiceAccount  string
	Image           string
	DesiredReplicas int
	ReadyReplicas   int
}

type Snapshot struct {
	Environment            platforminventory.Environment
	KubernetesContext      string
	Namespace              string
	InventoryID            string
	InventoryReceiptSHA256 string
	CollectedAt            time.Time
	NamespaceExists        bool
	ServiceAccounts        map[string]bool
	Secrets                map[string]bool
	NetworkPolicies        map[string]bool
	ServiceTypes           map[string]string
	Workloads              map[string]Workload
}

type Check struct {
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
}

type Receipt struct {
	Schema                 string                        `json:"schema"`
	Ready                  bool                          `json:"ready"`
	Environment            platforminventory.Environment `json:"environment"`
	KubernetesContext      string                        `json:"kubernetes_context"`
	Namespace              string                        `json:"namespace"`
	InventoryID            string                        `json:"inventory_id"`
	InventoryReceiptSHA256 string                        `json:"inventory_receipt_sha256"`
	CollectedAt            time.Time                     `json:"collected_at"`
	Checks                 []Check                       `json:"checks"`
	ReceiptSHA256          string                        `json:"-"`
}

func Evaluate(snapshot Snapshot) (Receipt, error) {
	expectedNamespace := ""
	switch snapshot.Environment {
	case platforminventory.Staging:
		expectedNamespace = "agent-memory-staging"
	case platforminventory.Production:
		expectedNamespace = "agent-memory-production"
	default:
		return Receipt{}, errors.New("platform preflight environment is invalid")
	}
	if snapshot.Namespace != expectedNamespace || !contextPattern.MatchString(snapshot.KubernetesContext) || !opaqueIDPattern.MatchString(snapshot.InventoryID) || !digestPattern.MatchString(snapshot.InventoryReceiptSHA256) || snapshot.CollectedAt.IsZero() {
		return Receipt{}, errors.New("platform preflight identity is invalid")
	}

	results := map[string]bool{
		"namespace":        snapshot.NamespaceExists,
		"service_accounts": containsAll(snapshot.ServiceAccounts, requiredServiceAccounts),
		"secret_contracts": containsAll(snapshot.Secrets, requiredSecrets),
		"network_policy":   containsAll(snapshot.NetworkPolicies, requiredNetworkPolicies),
		"private_service":  snapshot.ServiceTypes["agent-memory-api"] == "ClusterIP",
		"workload_identity": workloadsMatch(snapshot.Workloads, func(name string, workload Workload) bool {
			return workload.ServiceAccount == name
		}),
		"immutable_images": workloadsMatch(snapshot.Workloads, func(_ string, workload Workload) bool {
			return immutableImagePattern.MatchString(workload.Image)
		}),
		"ready_workloads": workloadsMatch(snapshot.Workloads, func(_ string, workload Workload) bool {
			return workload.DesiredReplicas > 0 && workload.ReadyReplicas == workload.DesiredReplicas
		}),
	}

	receipt := Receipt{
		Schema:                 ReceiptSchemaV1,
		Ready:                  true,
		Environment:            snapshot.Environment,
		KubernetesContext:      snapshot.KubernetesContext,
		Namespace:              snapshot.Namespace,
		InventoryID:            snapshot.InventoryID,
		InventoryReceiptSHA256: snapshot.InventoryReceiptSHA256,
		CollectedAt:            snapshot.CollectedAt.UTC(),
		Checks:                 make([]Check, 0, len(requiredCheckIDs)),
	}
	for _, id := range requiredCheckIDs {
		outcome := OutcomePassed
		if !results[id] {
			outcome = OutcomeFailed
			receipt.Ready = false
		}
		receipt.Checks = append(receipt.Checks, Check{ID: id, Outcome: outcome})
	}
	return receipt, nil
}

func Load(path string, inventory platforminventory.Inventory) (Receipt, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform preflight receipt: %w", err)
	}
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	if receipt.Environment != inventory.Environment || receipt.InventoryID != inventory.InventoryID || receipt.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !digestPattern.MatchString(inventory.ReceiptSHA256) {
		return Receipt{}, errors.New("platform preflight receipt inventory binding is invalid")
	}
	receipt.ReceiptSHA256 = digest
	return receipt, nil
}

func validateReceipt(receipt Receipt) error {
	expectedNamespace := "agent-memory-" + string(receipt.Environment)
	if receipt.Schema != ReceiptSchemaV1 || (receipt.Environment != platforminventory.Staging && receipt.Environment != platforminventory.Production) || receipt.Namespace != expectedNamespace || !contextPattern.MatchString(receipt.KubernetesContext) || !opaqueIDPattern.MatchString(receipt.InventoryID) || !digestPattern.MatchString(receipt.InventoryReceiptSHA256) || receipt.CollectedAt.IsZero() || len(receipt.Checks) != len(requiredCheckIDs) {
		return errors.New("platform preflight receipt identity is invalid")
	}
	ready := true
	for index, id := range requiredCheckIDs {
		check := receipt.Checks[index]
		if check.ID != id || (check.Outcome != OutcomePassed && check.Outcome != OutcomeFailed) {
			return errors.New("platform preflight receipt checks are not canonical")
		}
		if check.Outcome != OutcomePassed {
			ready = false
		}
	}
	if receipt.Ready != ready {
		return errors.New("platform preflight receipt readiness contradicts checks")
	}
	return nil
}

var requiredServiceAccounts = []string{
	"agent-memory-api",
	"agent-memory-worker",
	"agent-memory-reconciler",
	"agent-memory-migration",
}

var requiredSecrets = []string{
	"agent-memory-api-secrets",
	"agent-memory-worker-secrets",
	"agent-memory-reconciler-secrets",
	"agent-memory-migration-secrets",
}

var requiredNetworkPolicies = []string{
	"default-deny",
	"allow-api-edge-ingress",
	"allow-dns-and-managed-services",
	"allow-observability-scrape",
}

var requiredWorkloads = []string{
	"agent-memory-api",
	"agent-memory-worker",
	"agent-memory-reconciler",
}

func containsAll(values map[string]bool, required []string) bool {
	for _, value := range required {
		if !values[value] {
			return false
		}
	}
	return true
}

func workloadsMatch(workloads map[string]Workload, predicate func(string, Workload) bool) bool {
	for _, name := range requiredWorkloads {
		workload, exists := workloads[name]
		if !exists || !predicate(name, workload) {
			return false
		}
	}
	return true
}

func Publish(path string, receipt Receipt) error {
	if err := validateReceipt(receipt); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("platform preflight receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("platform preflight receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect platform preflight receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-platform-preflight-*")
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("platform preflight receipt path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumReceiptBytes {
		return "", errors.New("platform preflight receipt must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open platform preflight receipt")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("platform preflight receipt changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumReceiptBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumReceiptBytes {
		return "", errors.New("read platform preflight receipt")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("platform preflight receipt JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("platform preflight receipt contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("platform preflight receipt changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("platform preflight receipt changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
