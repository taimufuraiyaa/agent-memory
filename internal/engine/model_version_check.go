package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// ModelVersionCheck represents the result of checking model version consistency.
type ModelVersionCheck struct {
	CurrentProvider      string         `json:"current_provider"`
	CurrentModelVersion  string         `json:"current_model_version"`
	ProviderDistribution map[string]int `json:"provider_distribution"`
	VersionDistribution  map[string]int `json:"version_distribution"`
	HasMismatch          bool           `json:"has_mismatch"`
	MismatchedVectors    int            `json:"mismatched_vectors"`
	TotalVectors         int            `json:"total_vectors"`
	ReembedRequired      bool           `json:"reembed_required"`
	RecommendedAction    string         `json:"recommended_action"`
}

// CheckModelVersion verifies that all vectors in a workspace use the current provider and model version.
// Returns a report indicating whether re-embedding is needed.
func CheckModelVersion(ctx context.Context, workspace string, store *sqlite.Store, provider embeddings.Provider) (*ModelVersionCheck, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}

	currentProvider := provider.Name()
	currentModelVersion := provider.ModelVersion()

	// Get provider distribution
	providerDist, err := store.CountMemoryVectorsByProvider(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("count vectors by provider: %w", err)
	}

	// Get all vectors to check model versions
	vectors, err := store.ListMemoryVectorRowsByWorkspace(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list vectors: %w", err)
	}

	// Build version distribution and count mismatches
	versionDist := make(map[string]int)
	mismatchedVectors := 0
	totalVectors := len(vectors)

	for _, vec := range vectors {
		versionKey := vec.EmbeddingProvider + "@" + vec.EmbeddingModelVersion
		versionDist[versionKey]++

		// Check if this vector needs re-embedding
		if vec.EmbeddingProvider != currentProvider || vec.EmbeddingModelVersion != currentModelVersion {
			mismatchedVectors++
		}
	}

	hasMismatch := mismatchedVectors > 0
	reembedRequired := hasMismatch && mismatchedVectors > (totalVectors/10) // Reembed if >10% mismatch

	var recommendedAction string
	if !hasMismatch {
		recommendedAction = "No action required - all vectors use current provider and model version"
	} else if reembedRequired {
		recommendedAction = fmt.Sprintf("Run: agent-memory reembed --workspace %s", workspace)
	} else {
		recommendedAction = fmt.Sprintf("Optional: %d/%d vectors use outdated provider/version. Run reembed to update.", mismatchedVectors, totalVectors)
	}

	return &ModelVersionCheck{
		CurrentProvider:      currentProvider,
		CurrentModelVersion:  currentModelVersion,
		ProviderDistribution: providerDist,
		VersionDistribution:  versionDist,
		HasMismatch:          hasMismatch,
		MismatchedVectors:    mismatchedVectors,
		TotalVectors:         totalVectors,
		ReembedRequired:      reembedRequired,
		RecommendedAction:    recommendedAction,
	}, nil
}

// ShouldWarnAboutVersionMismatch determines if a warning should be shown to the user.
// Returns true if more than 10% of vectors are mismatched or if a complete provider change is detected.
func (c *ModelVersionCheck) ShouldWarnAboutVersionMismatch() bool {
	if c == nil || !c.HasMismatch {
		return false
	}

	// Warn if more than 10% of vectors are mismatched
	if c.MismatchedVectors > (c.TotalVectors / 10) {
		return true
	}

	// Warn if there's a complete provider change (current provider has 0 vectors)
	if count, ok := c.ProviderDistribution[c.CurrentProvider]; !ok || count == 0 {
		return true
	}

	return false
}

// FormatWarningMessage returns a human-readable warning message for display.
func (c *ModelVersionCheck) FormatWarningMessage() string {
	if c == nil || !c.HasMismatch {
		return ""
	}

	var b strings.Builder
	b.WriteString("⚠️  Model version mismatch detected\n\n")

	b.WriteString(fmt.Sprintf("Current: %s@%s\n", c.CurrentProvider, c.CurrentModelVersion))
	b.WriteString(fmt.Sprintf("Vectors: %d mismatched out of %d total (%.1f%%)\n\n",
		c.MismatchedVectors, c.TotalVectors,
		float64(c.MismatchedVectors)*100/float64(c.TotalVectors)))

	b.WriteString("Provider distribution:\n")
	for provider, count := range c.ProviderDistribution {
		percentage := float64(count) * 100 / float64(c.TotalVectors)
		b.WriteString(fmt.Sprintf("  %s: %d (%.1f%%)\n", provider, count, percentage))
	}

	b.WriteString("\nVersion distribution:\n")
	for version, count := range c.VersionDistribution {
		percentage := float64(count) * 100 / float64(c.TotalVectors)
		b.WriteString(fmt.Sprintf("  %s: %d (%.1f%%)\n", version, count, percentage))
	}

	b.WriteString(fmt.Sprintf("\n%s\n", c.RecommendedAction))

	return b.String()
}
