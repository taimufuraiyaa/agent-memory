package library

import (
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type LibraryKind string

const (
	LibraryPersonal     LibraryKind = "personal"
	LibraryOrganization LibraryKind = "organization"
)

type Library struct {
	ID             string         `json:"id"`
	Kind           LibraryKind    `json:"kind"`
	Owner          core.Principal `json:"owner"`
	OrganizationID string         `json:"organization_id,omitempty"`
}

func (l Library) Validate() error {
	if strings.TrimSpace(l.ID) == "" {
		return errors.New("library id is required")
	}
	if err := l.Owner.Validate(); err != nil {
		return err
	}
	switch l.Kind {
	case LibraryPersonal:
		if l.Owner.Kind != core.PrincipalUser || l.OrganizationID != "" {
			return errors.New("personal library requires one user owner")
		}
	case LibraryOrganization:
		if l.Owner.Kind != core.PrincipalOrganization || strings.TrimSpace(l.OrganizationID) == "" || l.Owner.ID != l.OrganizationID {
			return errors.New("organization library requires matching organization owner")
		}
	default:
		return errors.New("invalid library kind")
	}
	return nil
}

type Membership struct {
	LibraryID    string            `json:"library_id"`
	PrincipalID  string            `json:"principal_id"`
	Capabilities []core.Capability `json:"capabilities"`
	Version      string            `json:"version"`
	Active       bool              `json:"active"`
}

func (m Membership) Validate() error {
	if strings.TrimSpace(m.LibraryID) == "" || strings.TrimSpace(m.PrincipalID) == "" || strings.TrimSpace(m.Version) == "" {
		return errors.New("membership identity and version are required")
	}
	if !m.Active || len(m.Capabilities) == 0 {
		return errors.New("active membership requires explicit capabilities")
	}
	for _, capability := range m.Capabilities {
		if !core.IsCapability(capability) {
			return errors.New("invalid membership capability")
		}
	}
	return nil
}

type ResourceType string

const (
	ResourceEdition    ResourceType = "edition"
	ResourceSource     ResourceType = "source"
	ResourceCitation   ResourceType = "citation"
	ResourceAnnotation ResourceType = "annotation"
	ResourceMemory     ResourceType = "memory"
	ResourceSession    ResourceType = "session"
	ResourceGraphNode  ResourceType = "graph_node"
)

type LibraryResourcePolicy struct {
	LibraryID    string            `json:"library_id"`
	ResourceType ResourceType      `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Policy       core.AccessPolicy `json:"policy"`
}

func (r LibraryResourcePolicy) Validate() error {
	if strings.TrimSpace(r.LibraryID) == "" || strings.TrimSpace(r.ResourceID) == "" {
		return errors.New("library resource identity is required")
	}
	switch r.ResourceType {
	case ResourceEdition, ResourceSource, ResourceCitation, ResourceAnnotation, ResourceMemory, ResourceSession, ResourceGraphNode:
	default:
		return errors.New("invalid library resource type")
	}
	return r.Policy.Validate()
}
