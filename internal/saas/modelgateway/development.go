package modelgateway

import (
	"context"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
)

const DevelopmentDimensions = 384

// DevelopmentProvider is deterministic and dependency-free. Production
// configuration rejects it; it exists to make local SaaS flows reproducible.
type DevelopmentProvider struct{}

func (DevelopmentProvider) Name() string         { return "local-minilm-scaffold" }
func (DevelopmentProvider) ModelVersion() string { return "local-hash-v1" }
func (DevelopmentProvider) Dimension() int       { return DevelopmentDimensions }
func (DevelopmentProvider) Embed(_ context.Context, value string) ([]float32, error) {
	vector := make([]float32, DevelopmentDimensions)
	for _, token := range strings.Fields(strings.ToLower(value)) {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(token))
		sum := hash.Sum64()
		index := int(sum % DevelopmentDimensions)
		sign := float32(1)
		if sum&(1<<63) != 0 {
			sign = -1
		}
		vector[index] += sign
	}
	var squared float64
	for _, component := range vector {
		squared += float64(component * component)
	}
	if squared == 0 {
		vector[0] = 1
		return vector, nil
	}
	norm := float32(math.Sqrt(squared))
	for index := range vector {
		vector[index] /= norm
	}
	return vector, nil
}
func (p DevelopmentProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		vector, err := p.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[index] = vector
	}
	return vectors, nil
}

var hostedSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<private>.*?</private>`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\b(password|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+`),
}

type ContentRedactor struct{}

func (ContentRedactor) Redact(value string) string {
	for _, pattern := range hostedSecretPatterns {
		value = pattern.ReplaceAllString(value, "[REDACTED_SECRET]")
	}
	return value
}
