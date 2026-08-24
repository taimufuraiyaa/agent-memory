package readingroom

import (
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"testing"
)

func TestEvidencePacketFingerprintIsCanonicalAndSensitive(t *testing.T) {
	p1 := testPacket()
	p2 := testPacket()
	p2.Evidence[0], p2.Evidence[1] = p2.Evidence[1], p2.Evidence[0]
	p2.CreatedAt = "later"
	a, err := p1.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	b, err := p2.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("ordering or timestamp changed semantic fingerprint")
	}
	p2.AuthorizationFingerprint = "different"
	c, _ := p2.Fingerprint()
	if c == a {
		t.Fatal("authorization change did not change fingerprint")
	}
}
func testPacket() EvidencePacket {
	return EvidencePacket{Question: "What is claimed?", AuthorizationFingerprint: "auth", RetrievalVersion: "lexical-v1", Evidence: []library.Passage{testPassage("b"), testPassage("a")}, Profiles: []AgentProfile{DefaultProfiles()[RoleCritic], DefaultProfiles()[RoleSummarizer]}}
}
func testPassage(id string) library.Passage {
	return library.Passage{ID: id, EditionID: "edition", SourceAssetID: "asset", StructuralNodeID: "node-" + id, Text: "evidence " + id, Fingerprint: core.FingerprintText("evidence " + id), Locator: core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter", ParserVersion: "v1", NormalizationVersion: "v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 2, NormalizedStart: 0, NormalizedEnd: 2}}}
}
