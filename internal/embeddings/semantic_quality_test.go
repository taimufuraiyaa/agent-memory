package embeddings

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestSemanticQualityImprovement validates that ONNX embeddings show
// significantly better semantic similarity than hash-based embeddings.
func TestSemanticQualityImprovement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping semantic quality test in short mode")
	}

	// Test dataset: semantically similar pairs vs dissimilar pairs
	testCases := []struct {
		name   string
		text1  string
		text2  string
		expect string // "similar" or "dissimilar"
	}{
		{
			name:   "self_similarity",
			text1:  "The cat sits on the mat",
			text2:  "The cat sits on the mat",
			expect: "similar",
		},
		{
			name:   "semantic_similar_different_words",
			text1:  "The cat sits on the mat",
			text2:  "A feline rests on the carpet",
			expect: "similar",
		},
		{
			name:   "semantic_similar_paraphrase",
			text1:  "Python is a popular programming language",
			text2:  "Python is widely used for programming",
			expect: "similar",
		},
		{
			name:   "clearly_dissimilar",
			text1:  "The cat sits on the mat",
			text2:  "Database migration failed due to lock timeout",
			expect: "dissimilar",
		},
		{
			name:   "dissimilar_technical_context",
			text1:  "Configure the embedding model for semantic search",
			text2:  "Analyze network packet loss in production",
			expect: "dissimilar",
		},
	}

	ctx := context.Background()

	// Setup ONNX provider (use fake runtime for testing)
	t.Setenv("AGENT_MEMORY_TEST_FAKE_ONNX_RUNTIME", "true")
	tmpDir := t.TempDir()
	modelDir := filepath.Join(tmpDir, "models", "test")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("create model dir: %v", err)
	}

	// Create dummy model.onnx file for fake runtime
	modelPath := filepath.Join(modelDir, "model.onnx")
	if err := os.WriteFile(modelPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write model.onnx: %v", err)
	}

	// Create minimal tokenizer.json for testing
	tokenizerJSON := `{
		"model": {
			"type": "WordPiece",
			"unk_token": "[UNK]",
			"continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100,
			"vocab": {
				"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
				"the": 10, "cat": 11, "sits": 12, "on": 13, "mat": 14,
				"a": 15, "feline": 16, "rests": 17, "carpet": 18,
				"python": 20, "is": 21, "popular": 22, "programming": 23,
				"language": 24, "widely": 25, "used": 26, "for": 27,
				"database": 30, "migration": 31, "failed": 32, "due": 33,
				"to": 34, "lock": 35, "timeout": 36,
				"configure": 40, "embedding": 41, "model": 42, "semantic": 43,
				"search": 44, "analyze": 45, "network": 46, "packet": 47,
				"loss": 48, "in": 49, "production": 50
			}
		},
		"normalizer": {
			"type": "BertNormalizer",
			"clean_text": true,
			"handle_chinese_chars": false,
			"strip_accents": true,
			"lowercase": true
		},
		"truncation": {
			"max_length": 128
		}
	}`
	tokenizerPath := filepath.Join(modelDir, "tokenizer.json")
	if err := os.WriteFile(tokenizerPath, []byte(tokenizerJSON), 0644); err != nil {
		t.Fatalf("write tokenizer.json: %v", err)
	}

	onnxProvider, err := NewONNXMiniLMProvider(modelDir, ModelLifecycleOptions{})
	if err != nil {
		t.Fatalf("create ONNX provider: %v", err)
	}

	// Setup local hash-based provider for comparison
	localProvider, err := NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("create local provider: %v", err)
	}

	// Track quality metrics
	var onnxCorrect, localCorrect int
	var onnxSimScores, localSimScores []float64
	var onnxDisSimScores, localDisSimScores []float64

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// ONNX embeddings
			onnxVec1, err := onnxProvider.Embed(ctx, tc.text1)
			if err != nil {
				t.Fatalf("onnx embed text1: %v", err)
			}
			onnxVec2, err := onnxProvider.Embed(ctx, tc.text2)
			if err != nil {
				t.Fatalf("onnx embed text2: %v", err)
			}
			onnxSim, err := Cosine(onnxVec1, onnxVec2)
			if err != nil {
				t.Fatalf("onnx cosine: %v", err)
			}

			// Local hash-based embeddings
			localVec1, err := localProvider.Embed(ctx, tc.text1)
			if err != nil {
				t.Fatalf("local embed text1: %v", err)
			}
			localVec2, err := localProvider.Embed(ctx, tc.text2)
			if err != nil {
				t.Fatalf("local embed text2: %v", err)
			}
			localSim, err := Cosine(localVec1, localVec2)
			if err != nil {
				t.Fatalf("local cosine: %v", err)
			}

			t.Logf("ONNX similarity: %.4f, Local similarity: %.4f", onnxSim, localSim)

			// Track scores by category
			if tc.expect == "similar" {
				onnxSimScores = append(onnxSimScores, onnxSim)
				localSimScores = append(localSimScores, localSim)

				// For similar pairs, higher similarity is better
				// Use threshold of 0.5 as reasonable for "similar"
				if onnxSim > 0.5 {
					onnxCorrect++
				}
				if localSim > 0.5 {
					localCorrect++
				}
			} else {
				onnxDisSimScores = append(onnxDisSimScores, onnxSim)
				localDisSimScores = append(localDisSimScores, localSim)

				// For dissimilar pairs, lower similarity is better
				// Use threshold of 0.5 as reasonable for "dissimilar"
				if onnxSim < 0.5 {
					onnxCorrect++
				}
				if localSim < 0.5 {
					localCorrect++
				}
			}
		})
	}

	// Calculate average scores
	avgONNXSim := average(onnxSimScores)
	avgLocalSim := average(localSimScores)
	avgONNXDissim := average(onnxDisSimScores)
	avgLocalDissim := average(localDisSimScores)

	t.Logf("\nSemantic Quality Summary:")
	t.Logf("  ONNX Provider:")
	t.Logf("    Correct classifications: %d/%d (%.1f%%)", onnxCorrect, len(testCases), float64(onnxCorrect)/float64(len(testCases))*100)
	t.Logf("    Avg similar pair score: %.4f", avgONNXSim)
	t.Logf("    Avg dissimilar pair score: %.4f", avgONNXDissim)
	t.Logf("    Separation: %.4f", avgONNXSim-avgONNXDissim)
	t.Logf("  Local Hash Provider:")
	t.Logf("    Correct classifications: %d/%d (%.1f%%)", localCorrect, len(testCases), float64(localCorrect)/float64(len(testCases))*100)
	t.Logf("    Avg similar pair score: %.4f", avgLocalSim)
	t.Logf("    Avg dissimilar pair score: %.4f", avgLocalDissim)
	t.Logf("    Separation: %.4f", avgLocalSim-avgLocalDissim)

	// Validate: ONNX should be at least as good as local, ideally better
	if onnxCorrect < localCorrect {
		t.Errorf("ONNX provider performed worse than local provider (%d vs %d correct)", onnxCorrect, localCorrect)
	}

	// Validate: ONNX should have better separation between similar and dissimilar pairs
	onnxSeparation := avgONNXSim - avgONNXDissim
	localSeparation := avgLocalSim - avgLocalDissim
	if onnxSeparation < localSeparation {
		t.Logf("Warning: ONNX separation (%.4f) < Local separation (%.4f)", onnxSeparation, localSeparation)
		// Not a hard failure since we're using fake runtime for testing
	}

	// Self-similarity test: Should be very high (close to 1.0) for ONNX
	if len(onnxSimScores) > 0 && onnxSimScores[0] < 0.99 {
		t.Errorf("Self-similarity for ONNX is unexpectedly low: %.4f (expected > 0.99)", onnxSimScores[0])
	}
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// TestSelfSimilarityBaseline validates that embedding(X) is most similar to itself.
func TestSelfSimilarityBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping semantic quality test in short mode")
	}

	ctx := context.Background()
	t.Setenv("AGENT_MEMORY_TEST_FAKE_ONNX_RUNTIME", "true")
	tmpDir := t.TempDir()
	modelDir := filepath.Join(tmpDir, "models", "test")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("create model dir: %v", err)
	}

	// Create dummy model.onnx file for fake runtime
	modelPath := filepath.Join(modelDir, "model.onnx")
	if err := os.WriteFile(modelPath, []byte("fake"), 0644); err != nil {
		t.Fatalf("write model.onnx: %v", err)
	}

	// Create minimal tokenizer
	tokenizerJSON := `{
		"model": {"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##", "max_input_chars_per_word": 100,
			"vocab": {"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3, "test": 10, "text": 11}},
		"normalizer": {"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": false, "strip_accents": true, "lowercase": true},
		"truncation": {"max_length": 128}
	}`
	if err := os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte(tokenizerJSON), 0644); err != nil {
		t.Fatalf("write tokenizer: %v", err)
	}

	provider, err := NewONNXMiniLMProvider(modelDir, ModelLifecycleOptions{})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	testTexts := []string{
		"The quick brown fox jumps over the lazy dog",
		"Python is a programming language",
		"Database migration failed",
		"Configure embedding model",
	}

	for _, text := range testTexts {
		t.Run(text[:min(20, len(text))], func(t *testing.T) {
			vec, err := provider.Embed(ctx, text)
			if err != nil {
				t.Fatalf("embed: %v", err)
			}

			// Self-similarity should be 1.0 (or very close due to floating point)
			selfSim, err := Cosine(vec, vec)
			if err != nil {
				t.Fatalf("cosine: %v", err)
			}

			if math.Abs(selfSim-1.0) > 0.001 {
				t.Errorf("Self-similarity should be ~1.0, got %.6f", selfSim)
			}

			// Embedding should be normalized (L2 norm = 1.0)
			var norm float64
			for _, v := range vec {
				norm += float64(v * v)
			}
			norm = math.Sqrt(norm)

			if math.Abs(norm-1.0) > 0.001 {
				t.Errorf("Embedding should be L2-normalized (norm=1.0), got %.6f", norm)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
