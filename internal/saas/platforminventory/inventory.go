// Package platforminventory validates content-free self-managed deployment inventories.
package platforminventory

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
)

const (
	SchemaV1              = "agent-memory-self-managed-platform-inventory-v1"
	maximumInventoryBytes = 256 << 10
)

type Environment string
type ComponentKind string
type IntegrationKind string

const (
	Staging    Environment = "staging"
	Production Environment = "production"

	ComponentKubernetes    ComponentKind = "kubernetes"
	ComponentIdentity      ComponentKind = "identity"
	ComponentPostgres      ComponentKind = "postgres"
	ComponentObjectStorage ComponentKind = "object_storage"
	ComponentQueue         ComponentKind = "queue"
	ComponentSecrets       ComponentKind = "secrets"
	ComponentObservability ComponentKind = "observability"
	ComponentBackup        ComponentKind = "backup"

	IntegrationPayment IntegrationKind = "payment"
	IntegrationEmail   IntegrationKind = "email"
	IntegrationModel   IntegrationKind = "model"
)

var requiredComponentKinds = []ComponentKind{
	ComponentKubernetes,
	ComponentIdentity,
	ComponentPostgres,
	ComponentObjectStorage,
	ComponentQueue,
	ComponentSecrets,
	ComponentObservability,
	ComponentBackup,
}

var requiredIntegrationKinds = []IntegrationKind{
	IntegrationPayment,
	IntegrationEmail,
	IntegrationModel,
}

type Inventory struct {
	Schema                 string                `json:"schema"`
	Environment            Environment           `json:"environment"`
	InventoryID            string                `json:"inventory_id"`
	GeneratedAt            time.Time             `json:"generated_at"`
	AdministrativeDomainID string                `json:"administrative_domain_id"`
	SiteID                 string                `json:"site_id"`
	FailureDomains         []FailureDomain       `json:"failure_domains"`
	Components             []Component           `json:"components"`
	ExternalIntegrations   []ExternalIntegration `json:"external_integrations"`
	ReceiptSHA256          string                `json:"-"`
}

type FailureDomain struct {
	ID string `json:"id"`
}

type Component struct {
	Kind             ComponentKind `json:"kind"`
	OwnerGroup       string        `json:"owner_group"`
	Version          string        `json:"version"`
	Replicas         int           `json:"replicas"`
	FailureDomainIDs []string      `json:"failure_domain_ids"`
	PublicIngress    bool          `json:"public_ingress"`
}

type ExternalIntegration struct {
	Kind       IntegrationKind `json:"kind"`
	Enabled    bool            `json:"enabled"`
	OwnerGroup string          `json:"owner_group"`
}

var (
	opaqueIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	ownerPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	versionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

func Load(path string) (Inventory, error) {
	var inventory Inventory
	digest, err := decodeStrictRegular(path, &inventory)
	if err != nil {
		return Inventory{}, fmt.Errorf("load platform inventory: %w", err)
	}
	inventory.ReceiptSHA256 = digest
	if err := validate(inventory); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}

func validate(inventory Inventory) error {
	if inventory.Schema != SchemaV1 || inventory.GeneratedAt.IsZero() || !validEnvironment(inventory.Environment) {
		return errors.New("platform inventory identity is invalid")
	}
	if !opaqueIDPattern.MatchString(inventory.InventoryID) || !opaqueIDPattern.MatchString(inventory.AdministrativeDomainID) || !opaqueIDPattern.MatchString(inventory.SiteID) {
		return errors.New("platform inventory locator is invalid")
	}
	minimumDomains := 1
	if inventory.Environment == Production {
		minimumDomains = 2
	}
	if len(inventory.FailureDomains) < minimumDomains || len(inventory.FailureDomains) > 32 {
		return errors.New("platform inventory failure domains are invalid")
	}
	domains := make(map[string]struct{}, len(inventory.FailureDomains))
	for _, domain := range inventory.FailureDomains {
		if !opaqueIDPattern.MatchString(domain.ID) {
			return errors.New("platform inventory failure domain is invalid")
		}
		if _, duplicate := domains[domain.ID]; duplicate {
			return errors.New("platform inventory failure domain is duplicated")
		}
		domains[domain.ID] = struct{}{}
	}
	if err := validateComponents(inventory.Environment, inventory.Components, domains); err != nil {
		return err
	}
	return validateIntegrations(inventory.ExternalIntegrations)
}

func validateComponents(environment Environment, components []Component, domains map[string]struct{}) error {
	if len(components) != len(requiredComponentKinds) {
		return errors.New("platform inventory component set is incomplete")
	}
	allowed := make(map[ComponentKind]struct{}, len(requiredComponentKinds))
	for _, kind := range requiredComponentKinds {
		allowed[kind] = struct{}{}
	}
	seen := make(map[ComponentKind]struct{}, len(components))
	for _, component := range components {
		if _, ok := allowed[component.Kind]; !ok {
			return errors.New("platform inventory component is unknown")
		}
		if _, duplicate := seen[component.Kind]; duplicate {
			return errors.New("platform inventory component is duplicated")
		}
		seen[component.Kind] = struct{}{}
		if !ownerPattern.MatchString(component.OwnerGroup) || !versionPattern.MatchString(component.Version) || component.Replicas < 1 || component.Replicas > 99 {
			return errors.New("platform inventory component metadata is invalid")
		}
		minimumDomains := 1
		if environment == Production {
			minimumDomains = 2
		}
		if len(component.FailureDomainIDs) < minimumDomains || len(component.FailureDomainIDs) > len(domains) {
			return errors.New("platform inventory component redundancy is invalid")
		}
		componentDomains := map[string]struct{}{}
		for _, domainID := range component.FailureDomainIDs {
			if _, exists := domains[domainID]; !exists {
				return errors.New("platform inventory component references an unknown failure domain")
			}
			if _, duplicate := componentDomains[domainID]; duplicate {
				return errors.New("platform inventory component failure domain is duplicated")
			}
			componentDomains[domainID] = struct{}{}
		}
		if component.PublicIngress && component.Kind != ComponentIdentity {
			return errors.New("platform inventory exposes a private component publicly")
		}
	}
	return nil
}

func validateIntegrations(integrations []ExternalIntegration) error {
	if len(integrations) != len(requiredIntegrationKinds) {
		return errors.New("platform inventory external integration set is incomplete")
	}
	allowed := make(map[IntegrationKind]struct{}, len(requiredIntegrationKinds))
	for _, kind := range requiredIntegrationKinds {
		allowed[kind] = struct{}{}
	}
	seen := make(map[IntegrationKind]struct{}, len(integrations))
	for _, integration := range integrations {
		if _, ok := allowed[integration.Kind]; !ok || !ownerPattern.MatchString(integration.OwnerGroup) {
			return errors.New("platform inventory external integration is invalid")
		}
		if _, duplicate := seen[integration.Kind]; duplicate {
			return errors.New("platform inventory external integration is duplicated")
		}
		seen[integration.Kind] = struct{}{}
	}
	return nil
}

func validEnvironment(environment Environment) bool {
	return environment == Staging || environment == Production
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("inventory path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInventoryBytes {
		return "", errors.New("inventory must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open inventory")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("inventory changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInventoryBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInventoryBytes {
		return "", errors.New("read inventory")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("inventory JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("inventory contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("inventory changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("inventory changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
