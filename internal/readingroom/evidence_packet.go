package readingroom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"sort"
	"strings"
)

type EvidencePacket struct {
	Question                 string            `json:"question"`
	AuthorizationFingerprint string            `json:"authorization_fingerprint"`
	RetrievalVersion         string            `json:"retrieval_version"`
	Evidence                 []library.Passage `json:"evidence"`
	Profiles                 []AgentProfile    `json:"profiles"`
	CreatedAt                string            `json:"-"`
}

func (p EvidencePacket) Validate() error {
	if strings.TrimSpace(p.Question) == "" || strings.TrimSpace(p.AuthorizationFingerprint) == "" || strings.TrimSpace(p.RetrievalVersion) == "" {
		return errors.New("evidence packet requires question, authorization fingerprint, and retrieval version")
	}
	for _, v := range p.Evidence {
		if err := v.Validate(); err != nil {
			return err
		}
	}
	for _, v := range p.Profiles {
		if err := v.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (p EvidencePacket) Fingerprint() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	copyPacket := p
	copyPacket.Evidence = append([]library.Passage(nil), p.Evidence...)
	copyPacket.Profiles = append([]AgentProfile(nil), p.Profiles...)
	sort.Slice(copyPacket.Evidence, func(i, j int) bool { return copyPacket.Evidence[i].ID < copyPacket.Evidence[j].ID })
	sort.Slice(copyPacket.Profiles, func(i, j int) bool {
		if copyPacket.Profiles[i].ID == copyPacket.Profiles[j].ID {
			return copyPacket.Profiles[i].Version < copyPacket.Profiles[j].Version
		}
		return copyPacket.Profiles[i].ID < copyPacket.Profiles[j].ID
	})
	payload, err := json.Marshal(copyPacket)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "packet_" + hex.EncodeToString(sum[:]), nil
}
func AuthorizationFingerprint(scope core.AuthorizationScope) string {
	copyScope := scope
	copyScope.OrganizationIDs = append([]string(nil), scope.OrganizationIDs...)
	copyScope.Capabilities = append([]core.Capability(nil), scope.Capabilities...)
	sort.Strings(copyScope.OrganizationIDs)
	sort.Slice(copyScope.Capabilities, func(i, j int) bool { return copyScope.Capabilities[i] < copyScope.Capabilities[j] })
	b, _ := json.Marshal(copyScope)
	return core.FingerprintText(string(b))
}
