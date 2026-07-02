# Custom Embedder Plugin

An example embedding plugin that demonstrates how to integrate custom embedding providers.

## Overview

This example includes two embedding provider implementations:

1. **SimpleHashEmbedder**: A toy hash-based embedder (demo only, not for production)
2. **RealWorldEmbedder**: A template for integrating real embedding APIs

## SimpleHashEmbedder (Demo)

A deterministic hash-based embedding generator for testing and demonstrations.

⚠️ **WARNING**: This is a toy implementation. Do NOT use in production. Use proper embedding models.

### Features

- Deterministic embeddings from text hashes
- Configurable dimensions
- Zero external dependencies
- Normalized unit vectors
- Fast generation

### Usage

```go
package main

import (
    "context"
    "github.com/taimufuraiyaa/agent-memory/examples/plugins/custom-embedder"
    "github.com/taimufuraiyaa/agent-memory/internal/plugin"
)

func main() {
    // Create plugin with 384 dimensions
    embedderPlugin := customembedder.NewCustomEmbedderPlugin(384)
    
    // Register with global registry
    registry := plugin.GetRegistry()
    err := registry.Register(embedderPlugin, plugin.PluginMetadata{
        Name:        "custom-embedder",
        Version:     "1.0.0",
        Type:        plugin.PluginTypeEmbedding,
        Description: "Hash-based embedding provider (demo)",
        Author:      "agent-memory",
        License:     "MIT",
    })
    if err != nil {
        panic(err)
    }
    
    // Initialize
    err = embedderPlugin.Initialize(context.Background(), map[string]any{
        "dimensions": 384,
    })
    if err != nil {
        panic(err)
    }
    
    // Get provider
    provider := embedderPlugin.Provider()
    
    // Generate embeddings
    embedding, err := provider.Embed(context.Background(), "test text")
    if err != nil {
        panic(err)
    }
    
    println("Generated embedding with", len(embedding), "dimensions")
}
```

## RealWorldEmbedder (Template)

A template for integrating real embedding APIs (OpenAI, Cohere, HuggingFace, etc.).

### Supported Providers

You can integrate any embedding provider:

- **OpenAI**: text-embedding-3-small, text-embedding-3-large
- **Cohere**: embed-english-v3.0, embed-multilingual-v3.0
- **HuggingFace**: sentence-transformers, custom models
- **Google**: Vertex AI text embeddings
- **Azure**: Azure OpenAI embeddings
- **Anthropic**: Claude embeddings (when available)
- **Local Models**: Ollama, llama.cpp, FastEmbed

### Example: OpenAI Integration

```go
package main

import (
    "context"
    "github.com/openai/openai-go"
    "github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

// OpenAIEmbedder wraps OpenAI embedding API.
type OpenAIEmbedder struct {
    client     *openai.Client
    model      string
    dimensions int
}

func NewOpenAIEmbedder(apiKey, model string) *OpenAIEmbedder {
    return &OpenAIEmbedder{
        client:     openai.NewClient(apiKey),
        model:      model,
        dimensions: 1536, // for text-embedding-3-small
    }
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    resp, err := e.client.Embeddings.Create(ctx, openai.EmbeddingCreateParams{
        Input: openai.F(text),
        Model: openai.F(e.model),
    })
    if err != nil {
        return nil, err
    }
    
    // Convert []float64 to []float32
    embedding := make([]float32, len(resp.Data[0].Embedding))
    for i, v := range resp.Data[0].Embedding {
        embedding[i] = float32(v)
    }
    
    return embedding, nil
}

func (e *OpenAIEmbedder) Dimensions() int {
    return e.dimensions
}

func (e *OpenAIEmbedder) Model() string {
    return e.model
}

// Verify interface implementation
var _ embeddings.Provider = (*OpenAIEmbedder)(nil)
```

### Example: Cohere Integration

```go
package main

import (
    "context"
    cohere "github.com/cohere-ai/cohere-go/v2"
)

type CohereEmbedder struct {
    client     *cohere.Client
    model      string
    dimensions int
}

func NewCohereEmbedder(apiKey, model string) *CohereEmbedder {
    return &CohereEmbedder{
        client:     cohere.NewClient(cohere.WithToken(apiKey)),
        model:      model,
        dimensions: 1024, // for embed-english-v3.0
    }
}

func (e *CohereEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    resp, err := e.client.Embed(ctx, &cohere.EmbedRequest{
        Texts:     []string{text},
        Model:     &e.model,
        InputType: cohere.EmbedInputTypeSearchDocument.Ptr(),
    })
    if err != nil {
        return nil, err
    }
    
    // Convert to float32
    embedding := make([]float32, len(resp.Embeddings[0]))
    for i, v := range resp.Embeddings[0] {
        embedding[i] = float32(v)
    }
    
    return embedding, nil
}

func (e *CohereEmbedder) Dimensions() int {
    return e.dimensions
}

func (e *CohereEmbedder) Model() string {
    return e.model
}
```

### Example: Local Model (Ollama)

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

type OllamaEmbedder struct {
    baseURL    string
    model      string
    dimensions int
}

func NewOllamaEmbedder(baseURL, model string, dimensions int) *OllamaEmbedder {
    return &OllamaEmbedder{
        baseURL:    baseURL,
        model:      model,
        dimensions: dimensions,
    }
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    reqBody := map[string]interface{}{
        "model":  e.model,
        "prompt": text,
    }
    
    data, err := json.Marshal(reqBody)
    if err != nil {
        return nil, err
    }
    
    resp, err := http.Post(e.baseURL+"/api/embeddings", "application/json", bytes.NewReader(data))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result struct {
        Embedding []float64 `json:"embedding"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }
    
    // Convert to float32
    embedding := make([]float32, len(result.Embedding))
    for i, v := range result.Embedding {
        embedding[i] = float32(v)
    }
    
    return embedding, nil
}

func (e *OllamaEmbedder) Dimensions() int {
    return e.dimensions
}

func (e *OllamaEmbedder) Model() string {
    return e.model
}
```

## Plugin Registration

Once you have an embedder implementation, wrap it in a plugin:

```go
func CreateEmbeddingPlugin(provider embeddings.Provider) plugin.Plugin {
    return plugin.NewBaseEmbeddingPlugin(
        "my-embedder",
        "1.0.0",
        "My custom embedding provider",
        provider,
    )
}

// Register
registry := plugin.GetRegistry()
err := registry.Register(
    CreateEmbeddingPlugin(myProvider),
    plugin.PluginMetadata{
        Name:        "my-embedder",
        Version:     "1.0.0",
        Type:        plugin.PluginTypeEmbedding,
        Description: "Custom embedding provider",
        Author:      "Your Name",
        License:     "MIT",
    },
)
```

## Configuration

Pass API keys and options via Initialize():

```go
config := map[string]any{
    "apiKey":     "sk-...",
    "model":      "text-embedding-3-small",
    "dimensions": 1536,
    "timeout":    30, // seconds
}

err := plugin.Initialize(context.Background(), config)
```

## Best Practices

1. **Error Handling**: Handle API errors, rate limits, and retries
2. **Context Support**: Respect context cancellation and timeouts
3. **Batching**: Support batch embedding for efficiency
4. **Caching**: Cache embeddings to reduce API calls
5. **Monitoring**: Track API usage, latency, and errors
6. **Security**: Secure API key storage and rotation
7. **Testing**: Mock API calls in tests

## Performance Considerations

- Use connection pooling for HTTP clients
- Implement exponential backoff for retries
- Cache frequently used embeddings
- Consider batch API calls for multiple texts
- Monitor API rate limits and costs

## Testing

```go
func TestCustomEmbedder(t *testing.T) {
    embedder := NewSimpleHashEmbedder(384)
    
    embedding, err := embedder.Embed(context.Background(), "test")
    require.NoError(t, err)
    require.Len(t, embedding, 384)
    
    // Verify unit vector
    var magnitude float32
    for _, v := range embedding {
        magnitude += v * v
    }
    magnitude = float32(math.Sqrt(float64(magnitude)))
    require.InDelta(t, 1.0, magnitude, 0.001)
}
```
