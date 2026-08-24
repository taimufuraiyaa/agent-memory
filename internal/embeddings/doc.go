// Package embeddings provides embedding generation for agent-memory's semantic search.
//
// # Overview
//
// The embeddings package wraps local and optional cloud embedding providers,
// enabling semantic similarity search over memory content. It supports multiple
// providers with a unified interface and automatic fallback strategies.
//
// # Provider Architecture
//
// The package defines a Provider interface that all embedding implementations must satisfy:
//
//	type Provider interface {
//	    // Embed generates an embedding vector for the given text
//	    Embed(ctx context.Context, text string) ([]float32, error)
//
//	    // EmbedBatch generates embeddings for multiple texts
//	    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
//
//	    // Dimensions returns the embedding vector size
//	    Dimensions() int
//
//	    // ModelName returns the identifier of the embedding model
//	    ModelName() string
//	}
//
// This abstraction allows swapping providers without changing retrieval code.
//
// # Local vs Cloud Embeddings
//
// agent-memory is local-first by design, supporting both local and cloud providers:
//
//	Local Embeddings (Recommended):
//	  Provider: ONNX Runtime with local transformer models
//	  Models: Sentence-BERT variants (all-MiniLM-L6-v2, etc.)
//	  Pros:
//	    - Fully offline, no API calls
//	    - Deterministic and reproducible
//	    - Zero per-query cost
//	    - Fast inference on modern CPUs
//	    - Privacy-preserving (no data leaves machine)
//	  Cons:
//	    - Initial model download (~100MB)
//	    - Slightly lower quality vs large cloud models
//	    - CPU inference slower than GPU cloud endpoints
//
//	Cloud Embeddings (Optional):
//	  Providers: OpenAI (text-embedding-3-small), Cohere, etc.
//	  Pros:
//	    - Higher quality embeddings
//	    - Faster inference with GPU acceleration
//	    - No local model storage
//	  Cons:
//	    - Requires API key and internet
//	    - Per-query cost
//	    - Not deterministic (model updates)
//	    - Privacy: content sent to third-party
//
// Default: Local ONNX provider for privacy and cost. Configure via:
//
//	agent-memory config init
//	# Edit ~/.agent-memory/config.yaml:
//	embeddings:
//	  provider: onnx  # or "openai", "cohere"
//	  model: all-MiniLM-L6-v2
//
// # Model Management
//
// Local models are managed automatically:
//
//	Download:
//	  Models download on first use to ~/.agent-memory/models/
//	  Includes model weights, tokenizer vocab, and config files
//	  Uses fallback mirrors if primary download fails
//
//	Validation:
//	  Models are validated via SHA256 checksum
//	  Corrupted downloads are re-fetched automatically
//	  Validation runs on every store initialization
//
//	Upgrade:
//	  New agent-memory versions may include updated models
//	  Run: agent-memory upgrade --check to see available updates
//	  Run: agent-memory upgrade --models to download new models
//
//	Storage:
//	  Models are stored in ~/.agent-memory/models/<model-name>/
//	  Shared across all workspaces (single download)
//	  ~100-200MB per model (compressed)
//
// # Embedding Pipeline
//
// The embedding generation pipeline:
//
//  1. Text Preprocessing:
//     - Trim whitespace
//     - Truncate to model's max token limit (typically 512 tokens)
//     - Normalize Unicode
//
//  2. Tokenization:
//     - Split text into subword tokens (WordPiece, BPE, etc.)
//     - Convert tokens to input IDs via vocab
//     - Add special tokens ([CLS], [SEP])
//
//  3. Model Inference:
//     - Feed input IDs through transformer model
//     - Extract final layer hidden states
//     - Pool token embeddings (mean pooling, CLS token, etc.)
//
//  4. Normalization:
//     - Normalize embedding to unit length (L2 norm)
//     - Enables efficient cosine similarity via dot product
//
//  5. Caching (Optional):
//     - Cache embeddings for frequently accessed memories
//     - Reduces recomputation cost for repeated queries
//
// # Similarity Metrics
//
// agent-memory uses cosine similarity for semantic matching:
//
//	func cosineSimilarity(a, b []float32) float64 {
//	    // Assumes unit-normalized vectors
//	    dotProduct := 0.0
//	    for i := range a {
//	        dotProduct += float64(a[i] * b[i])
//	    }
//	    return dotProduct  // Range: [-1, 1]
//	}
//
// Cosine similarity properties:
//   - 1.0: Identical direction (perfect match)
//   - 0.0: Orthogonal (no similarity)
//   - -1.0: Opposite direction (inverse match)
//
// Typical thresholds in agent-memory:
//   - min_semantic_score: 0.3 (filter very irrelevant results)
//   - strong_recall: >0.6 (high confidence match)
//   - weak_recall: 0.3-0.6 (uncertain match)
//
// # Provider Implementations
//
// Currently supported providers:
//
//	ONNX (local/onnx_provider.go):
//	  Uses ONNX Runtime for local inference
//	  Models: Sentence-BERT variants exported to ONNX format
//	  Dimensions: 384 (all-MiniLM-L6-v2)
//	  Throughput: ~50-100 embeddings/sec on CPU
//
//	OpenAI (cloud/openai_provider.go):
//	  Uses OpenAI Embeddings API
//	  Models: text-embedding-3-small, text-embedding-ada-002
//	  Dimensions: 1536 (ada-002), 256-1536 (text-embedding-3-small)
//	  Throughput: API rate limit dependent
//
//	Mock (test/mock_provider.go):
//	  Returns random embeddings for testing
//	  Dimensions: Configurable
//	  Use: Integration tests only
//
// # Configuration
//
// Embedding configuration via config.yaml:
//
//	embeddings:
//	  provider: onnx           # "onnx", "openai", "cohere"
//	  model: all-MiniLM-L6-v2  # Model identifier
//	  dimensions: 384          # Optional: override auto-detection
//	  cache_size: 1000         # Optional: in-memory cache size
//	  batch_size: 32           # Optional: batch inference size
//
//	  # Provider-specific config
//	  onnx:
//	    threads: 4             # ONNX runtime thread count
//	    model_path: ""         # Optional: custom model path
//
//	  openai:
//	    api_key: ""            # Or use OPENAI_API_KEY env var
//	    timeout: 30s           # API request timeout
//
// # Performance
//
// Embedding generation performance varies by provider:
//
//	ONNX Local (CPU):
//	  Single embed: ~10-20ms
//	  Batch embed (32 items): ~200-400ms (~6-12ms per item)
//	  Throughput: 50-100 embeddings/sec
//
//	OpenAI API:
//	  Single embed: ~100-200ms (network latency)
//	  Batch embed (100 items): ~500-1000ms (~5-10ms per item)
//	  Throughput: Rate limit dependent (typically 3000 RPM)
//
// Optimization tips:
//   - Use EmbedBatch for multiple items (amortizes overhead)
//   - Enable in-memory caching for repeated queries
//   - For local: increase ONNX threads on multi-core CPUs
//   - For cloud: batch aggressively to reduce API calls
//
// # Error Handling
//
// Common embedding errors and recovery:
//
//	Model Not Found:
//	  Error: "model file not found at <path>"
//	  Fix: Run agent-memory upgrade --models
//
//	Network Timeout (cloud):
//	  Error: "context deadline exceeded"
//	  Fix: Check internet connection, increase timeout in config
//
//	API Key Invalid (cloud):
//	  Error: "authentication failed"
//	  Fix: Set correct API key in config or OPENAI_API_KEY env var
//
//	Out of Memory:
//	  Error: "failed to allocate tensor"
//	  Fix: Reduce batch_size in config or upgrade RAM
//
// All errors are wrapped with context using the error handling system
// in internal/core/errors.go.
//
// # Testing
//
// The embeddings package includes tests for all providers:
//   - Unit tests for each provider implementation
//   - Integration tests with real models (local only)
//   - Benchmark tests for throughput measurement
//   - Mock provider for dependent package tests
//
// Run tests: go test ./internal/embeddings/...
// Run benchmarks: go test -bench=. ./internal/embeddings/...
//
// # Usage Example
//
//	// Initialize local ONNX provider
//	provider, err := onnx.NewProvider(onnx.Config{
//	    ModelPath: "~/.agent-memory/models/all-MiniLM-L6-v2",
//	    Threads:   4,
//	})
//	if err != nil {
//	    return fmt.Errorf("failed to init embeddings: %w", err)
//	}
//	defer provider.Close()
//
//	// Generate single embedding
//	embedding, err := provider.Embed(ctx, "How do I configure the database?")
//	if err != nil {
//	    return fmt.Errorf("embed failed: %w", err)
//	}
//	fmt.Printf("Embedding dimensions: %d\n", len(embedding))
//
//	// Generate batch embeddings
//	texts := []string{
//	    "API uses JWT tokens",
//	    "Database runs on port 5432",
//	    "Redis cache for sessions",
//	}
//	embeddings, err := provider.EmbedBatch(ctx, texts)
//	if err != nil {
//	    return fmt.Errorf("batch embed failed: %w", err)
//	}
//
//	// Compute similarity
//	similarity := cosineSimilarity(embeddings[0], embeddings[1])
//	fmt.Printf("Similarity: %.2f\n", similarity)
package embeddings
