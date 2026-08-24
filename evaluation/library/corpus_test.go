package libraryevaluation

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCorpusCoversFormatsCasesRightsAndContracts(t *testing.T) {
	data, err := os.ReadFile("testdata/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version, Rights string
		Formats         []string
		Cases           []map[string]any
		Contracts       []string
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version == "" || manifest.Rights == "" || len(manifest.Formats) != 4 || len(manifest.Cases) < 4 || len(manifest.Contracts) != 4 {
		t.Fatalf("incomplete corpus manifest: %+v", manifest)
	}
	required := map[string]bool{"markdown": false, "epub": false, "pdf": false, "web": false}
	for _, format := range manifest.Formats {
		required[format] = true
	}
	for format, ok := range required {
		if !ok {
			t.Fatalf("format %s missing", format)
		}
	}
}
