package readingroom

import (
	"testing"
	"time"
)

func TestRoleRunnerResultValidation(t *testing.T) {
	profile := DefaultProfiles()[RoleQuestioner]
	packet := testPacket()
	fp, _ := packet.Fingerprint()
	input := RoleRunInput{RunID: "run", NodeID: "questioner", Profile: profile, EvidencePacketFingerprint: fp, Packet: packet, MaxOutputTokens: 100}
	now := time.Now().UTC()
	result := RoleRunResult{RunID: "run", NodeID: "questioner", ProfileID: profile.ID, ProfileVersion: profile.Version, PacketFingerprint: fp, Model: ModelMetadata{Provider: "test", Model: "fake"}, StartedAt: now, FinishedAt: now.Add(time.Millisecond)}
	if err := result.Validate(input); err != nil {
		t.Fatal(err)
	}
	result.ProfileVersion = "wrong"
	if err := result.Validate(input); err == nil {
		t.Fatal("mismatched runner result accepted")
	}
}
