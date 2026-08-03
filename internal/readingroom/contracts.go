// Package readingroom defines provenance-safe contracts for role-based book study.
package readingroom

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type Role string

const (
	RoleLibrarian        Role = "librarian"
	RoleSummarizer       Role = "summarizer"
	RoleCritic           Role = "critic"
	RoleQuestioner       Role = "questioner"
	RoleDomainExpert     Role = "domain_expert"
	RoleConnector        Role = "connector"
	RoleSynthesizer      Role = "synthesizer"
	RoleCitationVerifier Role = "citation_verifier"
)

type ContributionKind string

const (
	ContributionEvidence     ContributionKind = "evidence"
	ContributionClaim        ContributionKind = "claim"
	ContributionSummary      ContributionKind = "summary"
	ContributionQuote        ContributionKind = "quote"
	ContributionCritique     ContributionKind = "critique"
	ContributionQuestion     ContributionKind = "question"
	ContributionConnection   ContributionKind = "connection"
	ContributionSynthesis    ContributionKind = "synthesis"
	ContributionVerification ContributionKind = "verification"
)

// AgentProfile constrains one role's mandate and typed outputs.
type AgentProfile struct {
	ID             string             `json:"id"`
	Version        string             `json:"version"`
	Role           Role               `json:"role"`
	Mandate        string             `json:"mandate"`
	AllowedOutputs []ContributionKind `json:"allowed_outputs"`
}

func (p AgentProfile) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Version) == "" || !IsRole(p.Role) || strings.TrimSpace(p.Mandate) == "" {
		return errors.New("profile requires id, version, valid role, and mandate")
	}
	if len(p.AllowedOutputs) == 0 {
		return errors.New("profile requires at least one allowed output")
	}
	for _, kind := range p.AllowedOutputs {
		if !IsContributionKind(kind) {
			return fmt.Errorf("invalid contribution kind %q", kind)
		}
	}
	return nil
}

func (p AgentProfile) Accepts(kind ContributionKind) bool {
	for _, allowed := range p.AllowedOutputs {
		if kind == allowed {
			return true
		}
	}
	return false
}

// Contribution is the auditable, typed result of one role run.
type Contribution struct {
	ID             string                      `json:"id"`
	Role           Role                        `json:"role"`
	ProfileID      string                      `json:"profile_id"`
	ProfileVersion string                      `json:"profile_version"`
	Kind           ContributionKind            `json:"kind"`
	Provenance     core.KnowledgeProvenance    `json:"provenance"`
	Statement      string                      `json:"statement"`
	Citations      []core.Citation             `json:"citations,omitempty"`
	Verifications  []core.EvidenceVerification `json:"verifications,omitempty"`
	Confidence     float64                     `json:"confidence"`
}

func (c Contribution) Validate(profile AgentProfile) error {
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("invalid agent profile: %w", err)
	}
	if strings.TrimSpace(c.ID) == "" || !IsRole(c.Role) || !IsContributionKind(c.Kind) {
		return errors.New("contribution requires id, valid role, and valid kind")
	}
	if c.ProfileID != profile.ID || c.ProfileVersion != profile.Version || c.Role != profile.Role {
		return errors.New("contribution profile identity or version does not match active profile")
	}
	if !profile.Accepts(c.Kind) {
		return errors.New("agent profile does not permit contribution kind")
	}
	if err := c.Provenance.Validate(); err != nil {
		return fmt.Errorf("invalid contribution provenance: %w", err)
	}
	if strings.TrimSpace(c.Statement) == "" {
		return errors.New("contribution statement is required")
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return errors.New("contribution confidence must be between 0 and 1")
	}
	for _, citation := range c.Citations {
		if err := citation.Validate(); err != nil {
			return fmt.Errorf("invalid citation: %w", err)
		}
	}
	for _, verification := range c.Verifications {
		if err := verification.Validate(); err != nil {
			return fmt.Errorf("invalid verification: %w", err)
		}
	}
	if !citationsResolve(c.Provenance.CitationIDs, c.Citations) {
		return errors.New("provenance references a citation not supplied by the contribution")
	}
	if !verificationsResolve(c.ID, c.Citations, c.Verifications) {
		return errors.New("verification does not resolve to this contribution and its citations")
	}
	if c.Provenance.Attribution.Kind == core.AttributionAuthor && c.Provenance.Form == core.KnowledgeClaim &&
		!hasSupportingVerification(c.Statement, c.Citations, c.Verifications, core.VerificationEntailment) {
		return errors.New("author claim requires supporting entailment verification")
	}
	if c.Provenance.Form == core.KnowledgeQuote {
		if c.Kind != ContributionQuote {
			return errors.New("quote knowledge requires quote contribution kind")
		}
		if !hasVerifiedExactQuote(c.Statement, c.Citations, c.Verifications) {
			return errors.New("direct quote requires a verified citation with exact quote text")
		}
	} else if c.Kind == ContributionQuote {
		return errors.New("quote contribution requires quote knowledge form")
	}
	if c.Provenance.Form == core.KnowledgeSynthesis && c.Kind != ContributionSynthesis {
		return errors.New("synthesis knowledge requires synthesis contribution kind")
	}
	if c.Kind == ContributionSynthesis && c.Provenance.Form != core.KnowledgeSynthesis {
		return errors.New("synthesis contribution requires synthesis knowledge form")
	}
	return nil
}

func citationsResolve(ids []string, citations []core.Citation) bool {
	available := make(map[string]struct{}, len(citations))
	for _, citation := range citations {
		available[citation.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := available[id]; !ok {
			return false
		}
	}
	return true
}

func verificationsResolve(subjectID string, citations []core.Citation, verifications []core.EvidenceVerification) bool {
	citationByID := make(map[string]core.Citation, len(citations))
	for _, citation := range citations {
		citationByID[citation.ID] = citation
	}
	for _, verification := range verifications {
		citation, ok := citationByID[verification.CitationID]
		if !ok || verification.SubjectID != subjectID || !containsString(citation.VerificationIDs, verification.ID) {
			return false
		}
	}
	return true
}

func hasVerifiedExactQuote(statement string, citations []core.Citation, verifications []core.EvidenceVerification) bool {
	for _, citation := range citations {
		if citation.ShortQuote == statement && hasVerification(statement, citation, verifications, core.VerificationExactMatch) {
			return true
		}
	}
	return false
}

func hasSupportingVerification(statement string, citations []core.Citation, verifications []core.EvidenceVerification, method core.VerificationMethod) bool {
	for _, citation := range citations {
		if hasVerification(statement, citation, verifications, method) {
			return true
		}
	}
	return false
}

func hasVerification(statement string, citation core.Citation, verifications []core.EvidenceVerification, method core.VerificationMethod) bool {
	for _, verification := range verifications {
		if verification.CitationID == citation.ID && verification.Method == method &&
			(verification.Verdict == core.VerdictSupports || verification.Verdict == core.VerdictPartial) &&
			verification.SubjectFingerprint == core.FingerprintText(statement) &&
			verification.EvidenceFingerprint == citation.PassageFingerprint {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func IsRole(role Role) bool {
	switch role {
	case RoleLibrarian, RoleSummarizer, RoleCritic, RoleQuestioner,
		RoleDomainExpert, RoleConnector, RoleSynthesizer, RoleCitationVerifier:
		return true
	default:
		return false
	}
}

func IsContributionKind(kind ContributionKind) bool {
	switch kind {
	case ContributionEvidence, ContributionClaim, ContributionSummary, ContributionQuote,
		ContributionCritique, ContributionQuestion, ContributionConnection,
		ContributionSynthesis, ContributionVerification:
		return true
	default:
		return false
	}
}

func DefaultProfiles() map[Role]AgentProfile {
	return map[Role]AgentProfile{
		RoleLibrarian:        profile(RoleLibrarian, "Select relevant authorized evidence without interpreting beyond it.", ContributionEvidence),
		RoleSummarizer:       profile(RoleSummarizer, "Represent the source position faithfully and cite every author claim.", ContributionClaim, ContributionSummary, ContributionQuote),
		RoleCritic:           profile(RoleCritic, "Test assumptions and identify supported weaknesses or counterarguments.", ContributionCritique, ContributionQuestion),
		RoleQuestioner:       profile(RoleQuestioner, "Produce comprehension, Socratic, and reflection questions.", ContributionQuestion),
		RoleDomainExpert:     profile(RoleDomainExpert, "Connect evidence to attributed domain knowledge without assigning it to the source.", ContributionClaim, ContributionCritique, ContributionConnection),
		RoleConnector:        profile(RoleConnector, "Relate the evidence to other books, concepts, and project knowledge.", ContributionConnection),
		RoleSynthesizer:      profile(RoleSynthesizer, "Reconcile verified contributions while preserving disagreement and derivation.", ContributionSynthesis),
		RoleCitationVerifier: profile(RoleCitationVerifier, "Verify quotation text, locations, attribution, and source entailment.", ContributionVerification),
	}
}

func profile(role Role, mandate string, outputs ...ContributionKind) AgentProfile {
	return AgentProfile{ID: "default:" + string(role), Version: "v1", Role: role, Mandate: mandate, AllowedOutputs: outputs}
}
