// Package customembedder provides an example embedding plugin that uses a custom provider.
package customembedder

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/plugin"
)

// SimpleHashEmbedder is an example embedding provider that uses hash-based embeddings.
// NOTE: This is a toy example for demonstration. Do NOT use in production.
// Use proper embedding models like OpenAI, Cohere, or sentence-transformers.
type SimpleHashEmbedder struct {
	dimensions int
	model      string
}

// NewSimpleHashEmbedder creates a new hash-based embedder.
func NewSimpleHashEmbedder(dimensions int) *SimpleHashEmbedder {
	return &SimpleHashEmbedder{
		dimensions: dimensions,
		model:      "simple-hash-v1",
	}
}

// Embed generates embeddings using a deterministic hash function.
// This is a toy implementation for demonstration only.
func (e *SimpleHashEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return make([]float32, e.dimensions), nil
	}

	// Normalize text
	text = strings.ToLower(strings.TrimSpace(text))

	// Generate hash
	hash := sha256.Sum256([]byte(text))

	// Create embedding vector
	embedding := make([]float32, e.dimensions)

	// Use hash bytes to generate vector components
	for i := 0; i < e.dimensions; i++ {
		// Use different parts of hash for each dimension
		offset := (i * 8) % len(hash)
		value := binary.BigEndian.Uint64(hash[offset:])

		// Normalize to [-1, 1]
		normalized := float64(value) / float64(math.MaxUint64)
		embedding[i] = float32(normalized*2.0 - 1.0)
	}

	// Normalize vector to unit length
	magnitude := float32(0.0)
	for _, v := range embedding {
		magnitude += v * v
	}
	magnitude = float32(math.Sqrt(float64(magnitude)))

	if magnitude > 0 {
		for i := range embedding {
			embedding[i] /= magnitude
		}
	}

	return embedding, nil
}

// EmbedBatch generates embeddings for multiple texts.
func (e *SimpleHashEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		embeddings[i] = emb
	}
	return embeddings, nil
}

// Dimension returns the embedding dimensions.
func (e *SimpleHashEmbedder) Dimension() int {
	return e.dimensions
}

// Name returns the model name.
func (e *SimpleHashEmbedder) Name() string {
	return e.model
}

// ModelVersion returns the model version.
func (e *SimpleHashEmbedder) ModelVersion() string {
	return "simple-hash-v1"
}

// CustomEmbedderPlugin wraps the hash embedder as a plugin.
type CustomEmbedderPlugin struct {
	*plugin.BaseEmbeddingPlugin
}

// NewCustomEmbedderPlugin creates a new custom embedder plugin.
func NewCustomEmbedderPlugin(dimensions int) *CustomEmbedderPlugin {
	provider := NewSimpleHashEmbedder(dimensions)

	base := plugin.NewBaseEmbeddingPlugin(
		"custom-embedder",
		"1.0.0",
		"Example hash-based embedding provider (demo only)",
		provider,
	)

	return &CustomEmbedderPlugin{
		BaseEmbeddingPlugin: base,
	}
}

// Initialize initializes the plugin with configuration.
// Config options:
//   - dimensions (int): Embedding dimensions (default: 384)
func (p *CustomEmbedderPlugin) Initialize(ctx context.Context, config map[string]any) error {
	// Call base initialization
	if err := p.BaseEmbeddingPlugin.Initialize(ctx, config); err != nil {
		return err
	}

	// Get dimensions from config
	if dims, ok := config["dimensions"].(int); ok {
		if dims < 1 || dims > 4096 {
			return fmt.Errorf("dimensions must be between 1 and 4096")
		}
		// Note: Would need to recreate provider with new dimensions
		// Kept simple for example
	}

	return nil
}

// RealWorldEmbedder is a placeholder for a real-world embedding provider.
// Example: OpenAI, Cohere, HuggingFace, etc.
type RealWorldEmbedder struct {
	apiKey     string
	model      string
	dimensions int
	// client     *SomeAPIClient
}

// NewRealWorldEmbedder creates a real-world embedder.
func NewRealWorldEmbedder(apiKey, model string) *RealWorldEmbedder {
	return &RealWorldEmbedder{
		apiKey:     apiKey,
		model:      model,
		dimensions: 1536, // e.g., OpenAI text-embedding-3-small
	}
}

// Embed generates embeddings using an external API.
func (e *RealWorldEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// In a real implementation:
	// 1. Call external API with text
	// 2. Handle rate limiting and retries
	// 3. Parse response and extract embeddings
	// 4. Return float32 slice

	// Placeholder
	return nil, fmt.Errorf("not implemented: integrate with your embedding API")

	/* Example implementation:
	req := &EmbeddingRequest{
		Input: text,
		Model: e.model,
	}

	resp, err := e.client.CreateEmbedding(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("API error: %w", err)
	}

	return resp.Data[0].Embedding, nil
	*/
}

// EmbedBatch generates embeddings for multiple texts using an external API.
func (e *RealWorldEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	// In a real implementation:
	// 1. Call batch embedding API
	// 2. Handle rate limiting
	// 3. Return all embeddings

	// Placeholder
	return nil, fmt.Errorf("not implemented: integrate with your embedding API")
}

// Dimension returns the embedding dimensions.
func (e *RealWorldEmbedder) Dimension() int {
	return e.dimensions
}

// Name returns the model name.
func (e *RealWorldEmbedder) Name() string {
	return e.model
}

// ModelVersion returns the model version.
func (e *RealWorldEmbedder) ModelVersion() string {
	return "external-api-v1"
}

// Ensure interfaces are implemented
var (
	_ embeddings.Provider    = (*SimpleHashEmbedder)(nil)
	_ embeddings.Provider    = (*RealWorldEmbedder)(nil)
	_ plugin.EmbeddingPlugin = (*CustomEmbedderPlugin)(nil)
)
