package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func mustOpenStoreForVersionCheck(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

type testProvider struct {
	name    string
	version string
}

func (p *testProvider) Name() string {
	return p.name
}

func (p *testProvider) ModelVersion() string {
	return p.version
}

func (p *testProvider) Dimension() int {
	return 384
}

func (p *testProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	// Return a simple test vector
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (p *testProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		vec, err := p.Embed(ctx, texts[i])
		if err != nil {
			return nil, err
		}
		result[i] = vec
	}
	return result, nil
}

func newTestProviderForVersionCheck() *testProvider {
	return &testProvider{
		name:    "test-provider",
		version: "test-v1",
	}
}

func TestCheckModelVersion_EmptyWorkspace(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStoreForVersionCheck(t)
	defer store.Close()

	provider := newTestProviderForVersionCheck()
	workspace := "test-workspace"

	check, err := CheckModelVersion(ctx, workspace, store, provider)
	if err != nil {
		t.Fatalf("CheckModelVersion failed: %v", err)
	}

	if check.HasMismatch {
		t.Errorf("expected no mismatch for empty workspace, got HasMismatch=true")
	}
	if check.ReembedRequired {
		t.Errorf("expected ReembedRequired=false for empty workspace")
	}
	if check.TotalVectors != 0 {
		t.Errorf("expected TotalVectors=0, got %d", check.TotalVectors)
	}
}

func TestCheckModelVersion_AllMatchingVectors(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStoreForVersionCheck(t)
	defer store.Close()

	provider := newTestProviderForVersionCheck()
	workspace := "test-workspace"

	// Write 3 memories with matching provider/version
	pipeline := NewWritePipelineWithEmbedder(store, provider)
	for i := 0; i < 3; i++ {
		_, err := pipeline.Write(ctx, WriteInput{
			Workspace: workspace,
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Test memory content %d", i),
			Source:    core.MemorySource{Type: core.SourceUserInput},
			Mode:      ExtractFast,
		})
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	check, err := CheckModelVersion(ctx, workspace, store, provider)
	if err != nil {
		t.Fatalf("CheckModelVersion failed: %v", err)
	}

	if check.HasMismatch {
		t.Errorf("expected no mismatch when all vectors match, got HasMismatch=true")
	}
	if check.ReembedRequired {
		t.Errorf("expected ReembedRequired=false when all vectors match")
	}
	if check.TotalVectors != 3 {
		t.Errorf("expected TotalVectors=3, got %d", check.TotalVectors)
	}
	if check.MismatchedVectors != 0 {
		t.Errorf("expected MismatchedVectors=0, got %d", check.MismatchedVectors)
	}
}

func TestCheckModelVersion_MismatchedVectors(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStoreForVersionCheck(t)
	defer store.Close()

	provider := newTestProviderForVersionCheck()
	workspace := "test-workspace"

	// Write memories and then directly update vector provenance to simulate mismatches
	pipeline := NewWritePipelineWithEmbedder(store, provider)
	for i := 0; i < 10; i++ {
		_, err := pipeline.Write(ctx, WriteInput{
			Workspace: workspace,
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Test memory content %d", i),
			Source:    core.MemorySource{Type: core.SourceUserInput},
			Mode:      ExtractFast,
		})
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	// Update 3 vectors to have old provider/version (30% mismatch - should require reembed)
	vectors, err := store.ListMemoryVectorRowsByWorkspace(ctx, workspace)
	if err != nil {
		t.Fatalf("ListMemoryVectorRowsByWorkspace: %v", err)
	}
	for i := 0; i < 3 && i < len(vectors); i++ {
		// Re-upsert the vector with old provider/version
		err = store.UpsertMemoryVector(ctx, vectors[i].MemoryID, workspace, "old-provider", "old-version", vectors[i].Embedding)
		if err != nil {
			t.Fatalf("UpsertMemoryVector: %v", err)
		}
	}

	check, err := CheckModelVersion(ctx, workspace, store, provider)
	if err != nil {
		t.Fatalf("CheckModelVersion failed: %v", err)
	}

	if !check.HasMismatch {
		t.Errorf("expected HasMismatch=true for mismatched vectors")
	}
	if !check.ReembedRequired {
		t.Errorf("expected ReembedRequired=true when >10%% mismatch (30%% in test)")
	}
	if check.TotalVectors != 10 {
		t.Errorf("expected TotalVectors=10, got %d", check.TotalVectors)
	}
	if check.MismatchedVectors != 3 {
		t.Errorf("expected MismatchedVectors=3, got %d", check.MismatchedVectors)
	}
	if check.CurrentProvider != provider.Name() {
		t.Errorf("expected CurrentProvider=%s, got %s", provider.Name(), check.CurrentProvider)
	}
	if check.CurrentModelVersion != provider.ModelVersion() {
		t.Errorf("expected CurrentModelVersion=%s, got %s", provider.ModelVersion(), check.CurrentModelVersion)
	}
}

func TestCheckModelVersion_SmallMismatch_NoReembedRequired(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStoreForVersionCheck(t)
	defer store.Close()

	provider := newTestProviderForVersionCheck()
	workspace := "test-workspace"

	// Write 20 memories
	pipeline := NewWritePipelineWithEmbedder(store, provider)
	for i := 0; i < 20; i++ {
		_, err := pipeline.Write(ctx, WriteInput{
			Workspace: workspace,
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Test memory content %d", i),
			Source:    core.MemorySource{Type: core.SourceUserInput},
			Mode:      ExtractFast,
		})
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	// Update 1 vector to have old provider/version (5% mismatch - should NOT require reembed)
	vectors, err := store.ListMemoryVectorRowsByWorkspace(ctx, workspace)
	if err != nil {
		t.Fatalf("ListMemoryVectorRowsByWorkspace: %v", err)
	}
	// Re-upsert the first vector with old provider/version
	err = store.UpsertMemoryVector(ctx, vectors[0].MemoryID, workspace, "old-provider", "old-version", vectors[0].Embedding)
	if err != nil {
		t.Fatalf("UpsertMemoryVector: %v", err)
	}

	check, err := CheckModelVersion(ctx, workspace, store, provider)
	if err != nil {
		t.Fatalf("CheckModelVersion failed: %v", err)
	}

	if !check.HasMismatch {
		t.Errorf("expected HasMismatch=true for 1 mismatched vector")
	}
	if check.ReembedRequired {
		t.Errorf("expected ReembedRequired=false when <10%% mismatch (5%% in test)")
	}
	if check.TotalVectors != 20 {
		t.Errorf("expected TotalVectors=20, got %d", check.TotalVectors)
	}
	if check.MismatchedVectors != 1 {
		t.Errorf("expected MismatchedVectors=1, got %d", check.MismatchedVectors)
	}
}

func TestCheckModelVersion_InvalidInputs(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStoreForVersionCheck(t)
	defer store.Close()

	provider := newTestProviderForVersionCheck()

	// Nil store
	_, err := CheckModelVersion(ctx, "workspace", nil, provider)
	if err == nil {
		t.Errorf("expected error for nil store")
	}

	// Nil provider
	_, err = CheckModelVersion(ctx, "workspace", store, nil)
	if err == nil {
		t.Errorf("expected error for nil provider")
	}

	// Empty workspace
	_, err = CheckModelVersion(ctx, "", store, provider)
	if err == nil {
		t.Errorf("expected error for empty workspace")
	}
}

func TestShouldWarnAboutVersionMismatch(t *testing.T) {
	tests := []struct {
		name              string
		check             *ModelVersionCheck
		expectedShouldWarn bool
	}{
		{
			name:              "nil check",
			check:             nil,
			expectedShouldWarn: false,
		},
		{
			name: "no mismatch",
			check: &ModelVersionCheck{
				HasMismatch:       false,
				MismatchedVectors: 0,
				TotalVectors:      100,
			},
			expectedShouldWarn: false,
		},
		{
			name: "small mismatch <10%",
			check: &ModelVersionCheck{
				CurrentProvider:      "test",
				HasMismatch:          true,
				MismatchedVectors:    5,
				TotalVectors:         100,
				ProviderDistribution: map[string]int{"test": 95, "old": 5},
			},
			expectedShouldWarn: false,
		},
		{
			name: "large mismatch >10%",
			check: &ModelVersionCheck{
				CurrentProvider:      "test",
				HasMismatch:          true,
				MismatchedVectors:    15,
				TotalVectors:         100,
				ProviderDistribution: map[string]int{"test": 85, "old": 15},
			},
			expectedShouldWarn: true,
		},
		{
			name: "complete provider change",
			check: &ModelVersionCheck{
				CurrentProvider:      "new-provider",
				HasMismatch:          true,
				MismatchedVectors:    100,
				TotalVectors:         100,
				ProviderDistribution: map[string]int{"old-provider": 100},
			},
			expectedShouldWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.check.ShouldWarnAboutVersionMismatch()
			if got != tt.expectedShouldWarn {
				t.Errorf("ShouldWarnAboutVersionMismatch() = %v, want %v", got, tt.expectedShouldWarn)
			}
		})
	}
}

func TestFormatWarningMessage(t *testing.T) {
	check := &ModelVersionCheck{
		CurrentProvider:      "onnx-minilm-l6-v2",
		CurrentModelVersion:  "minilm-l6-v2-fp32",
		HasMismatch:          true,
		MismatchedVectors:    30,
		TotalVectors:         100,
		ProviderDistribution: map[string]int{"onnx-minilm-l6-v2": 70, "local-hash": 30},
		VersionDistribution:  map[string]int{"onnx-minilm-l6-v2@minilm-l6-v2-fp32": 70, "local-hash@local-hash-v1": 30},
		ReembedRequired:      true,
		RecommendedAction:    "Run: agent-memory reembed --workspace test",
	}

	msg := check.FormatWarningMessage()
	if msg == "" {
		t.Errorf("expected non-empty warning message")
	}

	// Check that key information is present
	if !contains(msg, "Model version mismatch") {
		t.Errorf("message should contain 'Model version mismatch'")
	}
	if !contains(msg, "onnx-minilm-l6-v2") {
		t.Errorf("message should contain current provider")
	}
	if !contains(msg, "30") {
		t.Errorf("message should contain mismatched count")
	}
	if !contains(msg, "reembed") {
		t.Errorf("message should contain reembed recommendation")
	}

	// Nil check should return empty message
	var nilCheck *ModelVersionCheck
	if nilCheck.FormatWarningMessage() != "" {
		t.Errorf("nil check should return empty message")
	}

	// No mismatch should return empty message
	noMismatch := &ModelVersionCheck{HasMismatch: false}
	if noMismatch.FormatWarningMessage() != "" {
		t.Errorf("no mismatch should return empty message")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && 
		(s == substr || (len(s) > len(substr) && hasSubstring(s, substr)))
}

func hasSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
