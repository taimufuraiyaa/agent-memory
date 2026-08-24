package export

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPortableBundleEncryptedManifestAndExplicitSourceBytes(t *testing.T) {
	bundle := Bundle{Format: "agent-memory-portable", Version: "2.0", MinReaderVersion: "2.0", ExportedAt: time.Now().UTC(), Memories: []map[string]any{{"id": "memory"}}, Notes: []map[string]any{}, Sources: []map[string]any{{"id": "source"}}, SourceVersions: []map[string]any{}, Lineage: []map[string]any{}, Attestations: []map[string]any{}, Policies: []map[string]any{}, SourceBytesIncluded: false, SourceObjects: []SourceObject{}}
	if err := bundle.SealManifest(); err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(bundle)
	encrypted, err := EncryptPortable("correct horse battery staple", plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == string(plain) {
		t.Fatal("portable bundle was not encrypted")
	}
	decoded, err := DecryptPortable("correct horse battery staple", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Bundle
	if err := json.Unmarshal(decoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := roundTrip.VerifyManifest(); err != nil {
		t.Fatal(err)
	}
	roundTrip.Manifest.Counts["memories"]++
	if err := roundTrip.VerifyManifest(); err == nil {
		t.Fatal("tampered manifest counts verified")
	}
	roundTrip.Manifest.Counts["memories"]--
	roundTrip.Memories[0]["id"] = "tampered"
	if err := roundTrip.VerifyManifest(); err == nil {
		t.Fatal("tampered manifest verified")
	}
}
