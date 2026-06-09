package embeddings

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// MiniLMDimension is the target output size for all-MiniLM-L6-v2.
	MiniLMDimension = 384
)

// LocalProvider is a deterministic local embedding provider scaffold.
// It preserves contract shape while ONNX inference is wired in future tasks.
type LocalProvider struct {
	modelDir string
}

// NewLocalProvider constructs a local provider with model directory checks.
func NewLocalProvider(modelDir string) (*LocalProvider, error) {
	if strings.TrimSpace(modelDir) == "" {
		return nil, errors.New("model dir is required")
	}
	if err := ensureModelDir(modelDir); err != nil {
		return nil, err
	}
	auto := parseBoolEnv(os.Getenv("AGENT_MEMORY_MODEL_AUTODOWNLOAD"))
	strict := parseBoolEnv(os.Getenv("AGENT_MEMORY_MODEL_STRICT"))
	if auto || strict {
		err := EnsureMiniLMModel(modelDir, ModelLifecycleOptions{
			AutoDownload: auto,
			URLs: map[string]string{
				"model.onnx":     strings.TrimSpace(os.Getenv("AGENT_MEMORY_MINILM_ONNX_URL")),
				"tokenizer.json": strings.TrimSpace(os.Getenv("AGENT_MEMORY_MINILM_TOKENIZER_URL")),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return &LocalProvider{modelDir: modelDir}, nil
}

func (p *LocalProvider) Name() string          { return "local-minilm-scaffold" }
func (p *LocalProvider) ModelVersion() string  { return "local-hash-v1" }
func (p *LocalProvider) Dimension() int        { return MiniLMDimension }
func (p *LocalProvider) ModelDir() string      { return p.modelDir }

// Embed returns a normalized deterministic vector with MiniLM-compatible dimensions.
func (p *LocalProvider) Embed(_ context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("text is required")
	}
	return deterministicEmbedding(text), nil
}

// EmbedBatch embeds each input deterministically.
func (p *LocalProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		v, err := p.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func ensureModelDir(modelDir string) error {
	info, err := os.Stat(modelDir)
	if err != nil {
		return fmt.Errorf("model dir check failed: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("model path is not a directory: %s", modelDir)
	}
	return nil
}

func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

var tokenSplitRE = regexp.MustCompile(`[^a-z0-9_]+`)

func deterministicEmbedding(text string) []float32 {
	vec := make([]float32, MiniLMDimension)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		tokens = []string{"_empty_"}
	}
	for _, tok := range tokens {
		addTokenVector(vec, tok, 1.0)
	}
	// Add a tiny whole-text signal for tie-breaking deterministic behavior.
	addTokenVector(vec, strings.ToLower(strings.TrimSpace(text)), 0.05)
	return normalize(vec)
}

func tokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	raw := tokenSplitRE.Split(text, -1)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

func addTokenVector(vec []float32, token string, weight float32) {
	for i := 0; i < 3; i++ {
		h := fnvHash32(token, i)
		idx := int(h % uint32(len(vec)))
		sign := float32(1)
		if h&1 == 1 {
			sign = -1
		}
		vec[idx] += sign * weight
	}
}

func fnvHash32(token string, salt int) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(salt)*0x9e3779b1)
	_, _ = h.Write(b[:])
	return h.Sum32()
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

// DefaultModelDir returns the expected model location under ~/.agent-memory.
func DefaultModelDir(homeDir string) string {
	return filepath.Join(homeDir, ".agent-memory", "models", "all-MiniLM-L6-v2")
}
