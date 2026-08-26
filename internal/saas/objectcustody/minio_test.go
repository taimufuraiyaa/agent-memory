package objectcustody

import "testing"

func TestGraphObjectLocationRoutesOnlyOwnedPrefixes(t *testing.T) {
	tests := map[string]string{
		"graph-projections/tenant/workspace/revision/manifest.json":           graphProjectionBucket,
		"graph-artifacts/staging/tenant/workspace/job/revision/manifest.json": graphArtifactBucket,
		"graph-artifacts/state/tenant/workspace/revision/manifest.json":       graphArtifactBucket,
	}
	for key, expected := range tests {
		bucket, object, err := graphObjectLocation(key)
		if err != nil || bucket != expected || object != key {
			t.Fatalf("key %q routed to %q/%q: %v", key, bucket, object, err)
		}
	}
	for _, key := range []string{"../graph-projections/a", "/graph-projections/a", "exports/a", "graph-artifacts/a/../b", "graph-projections\\a"} {
		if _, _, err := graphObjectLocation(key); err == nil {
			t.Fatalf("unsafe key %q accepted", key)
		}
	}
}
