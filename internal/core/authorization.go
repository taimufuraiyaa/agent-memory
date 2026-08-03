package core

import (
	"errors"
	"strings"
)

type PrincipalKind string

const (
	PrincipalUser         PrincipalKind = "user"
	PrincipalAgent        PrincipalKind = "agent"
	PrincipalOrganization PrincipalKind = "organization"
)

type Principal struct {
	ID   string        `json:"id"`
	Kind PrincipalKind `json:"kind"`
}

func (p Principal) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("principal id is required")
	}
	switch p.Kind {
	case PrincipalUser, PrincipalAgent, PrincipalOrganization:
		return nil
	default:
		return errors.New("invalid principal kind")
	}
}

type Visibility string

const (
	VisibilityPrivate      Visibility = "private"
	VisibilityOrganization Visibility = "organization"
	VisibilityPublic       Visibility = "public"
)

type ResourceOwnership struct {
	Owner          Principal  `json:"owner"`
	Visibility     Visibility `json:"visibility"`
	OrganizationID string     `json:"organization_id,omitempty"`
}

type Capability string

const (
	CapabilityReadSource       Capability = "read_source"
	CapabilitySearchSource     Capability = "search_source"
	CapabilityQuoteSource      Capability = "quote_source"
	CapabilityAnnotate         Capability = "annotate"
	CapabilityDiscuss          Capability = "discuss"
	CapabilityProposeKnowledge Capability = "propose_knowledge"
	CapabilityApproveKnowledge Capability = "approve_knowledge"
	CapabilityManageCollection Capability = "manage_collection"
	CapabilityExport           Capability = "export"
)

type AuthorizationScope struct {
	Principal       Principal    `json:"principal"`
	OrganizationIDs []string     `json:"organization_ids,omitempty"`
	Capabilities    []Capability `json:"capabilities"`
	PolicyVersion   string       `json:"policy_version"`
}

type AccessGrant struct {
	PrincipalID  string       `json:"principal_id"`
	Capabilities []Capability `json:"capabilities"`
}

type AccessPolicy struct {
	Version   string            `json:"version"`
	Ownership ResourceOwnership `json:"ownership"`
	Grants    []AccessGrant     `json:"grants,omitempty"`
}

type AccessDecision struct {
	Allowed       bool   `json:"allowed"`
	PolicyVersion string `json:"policy_version"`
	Reason        string `json:"reason"`
}

func IsCapability(capability Capability) bool {
	switch capability {
	case CapabilityReadSource, CapabilitySearchSource, CapabilityQuoteSource,
		CapabilityAnnotate, CapabilityDiscuss, CapabilityProposeKnowledge,
		CapabilityApproveKnowledge, CapabilityManageCollection, CapabilityExport:
		return true
	default:
		return false
	}
}

func (s AuthorizationScope) Validate() error {
	if err := s.Principal.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(s.PolicyVersion) == "" {
		return errors.New("authorization policy version is required")
	}
	for _, capability := range s.Capabilities {
		if !IsCapability(capability) {
			return errors.New("invalid authorization capability")
		}
	}
	return nil
}

func (p AccessPolicy) Validate() error {
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("access policy version is required")
	}
	if err := p.Ownership.Validate(); err != nil {
		return err
	}
	for _, grant := range p.Grants {
		if strings.TrimSpace(grant.PrincipalID) == "" {
			return errors.New("grant principal id is required")
		}
		for _, capability := range grant.Capabilities {
			if !IsCapability(capability) {
				return errors.New("invalid grant capability")
			}
		}
	}
	return nil
}

func Authorize(scope AuthorizationScope, policy AccessPolicy, capability Capability) AccessDecision {
	decision := AccessDecision{PolicyVersion: policy.Version, Reason: "access denied"}
	if scope.Validate() != nil || policy.Validate() != nil || !IsCapability(capability) || !hasCapability(scope.Capabilities, capability) {
		return decision
	}
	if scope.Principal.ID == policy.Ownership.Owner.ID || policy.Ownership.Visibility == VisibilityPublic {
		decision.Allowed, decision.Reason = true, "access granted"
		return decision
	}
	if policy.Ownership.Visibility == VisibilityOrganization && containsID(scope.OrganizationIDs, policy.Ownership.OrganizationID) {
		decision.Allowed, decision.Reason = true, "access granted"
		return decision
	}
	for _, grant := range policy.Grants {
		if grant.PrincipalID == scope.Principal.ID && hasCapability(grant.Capabilities, capability) {
			decision.Allowed, decision.Reason = true, "access granted"
			return decision
		}
	}
	return decision
}

func hasCapability(capabilities []Capability, target Capability) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func (o ResourceOwnership) Validate() error {
	if err := o.Owner.Validate(); err != nil {
		return err
	}
	switch o.Visibility {
	case VisibilityPrivate, VisibilityPublic:
		return nil
	case VisibilityOrganization:
		if strings.TrimSpace(o.OrganizationID) == "" {
			return errors.New("organization visibility requires organization id")
		}
		return nil
	default:
		return errors.New("invalid visibility")
	}
}
