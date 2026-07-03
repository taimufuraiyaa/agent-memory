package sqlite

import (
	"math"
	"sync"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

// TurbovecIndex is a fast, in-memory, quantized vector cache/index.
// It keeps pre-rotated (FWHT) and 8-bit quantized vectors to speed up searches.
type TurbovecIndex struct {
	mu      sync.RWMutex
	vectors map[string]*embeddings.QuantizedVector
}

// NewTurbovecIndex builds a new in-memory turbovec index.
func NewTurbovecIndex() *TurbovecIndex {
	return &TurbovecIndex{
		vectors: make(map[string]*embeddings.QuantizedVector),
	}
}

// Upsert adds or updates a vector in the index.
func (idx *TurbovecIndex) Upsert(id string, vec []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	qvec, err := embeddings.QuantizeTurbo(vec)
	if err != nil {
		return err
	}
	idx.vectors[id] = qvec
	return nil
}

// Get retrieves a quantized vector by memory ID.
func (idx *TurbovecIndex) Get(id string) (*embeddings.QuantizedVector, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	qvec, ok := idx.vectors[id]
	return qvec, ok
}

// Delete removes a vector from the index.
func (idx *TurbovecIndex) Delete(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	delete(idx.vectors, id)
}

// Clear clears all vectors in the index.
func (idx *TurbovecIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.vectors = make(map[string]*embeddings.QuantizedVector)
}

// SearchQuantized computes cosine similarity in the quantized space.
func (idx *TurbovecIndex) SearchQuantized(queryVec []float32, candidates []string) []VectorScore {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(candidates) == 0 || len(idx.vectors) == 0 {
		return nil
	}

	qQuery, err := embeddings.QuantizeTurbo(queryVec)
	if err != nil {
		return nil
	}

	scores := make([]VectorScore, 0, len(candidates))
	for _, id := range candidates {
		qvec, ok := idx.vectors[id]
		if !ok {
			continue
		}

		score := computeQuantizedCosineSimilarity(qQuery, qvec)
		scores = append(scores, VectorScore{
			MemoryID: id,
			Score:    score,
		})
	}

	return scores
}

func computeQuantizedCosineSimilarity(q1, q2 *embeddings.QuantizedVector) float64 {
	if len(q1.Bytes) != len(q2.Bytes) || len(q1.Bytes) == 0 {
		return 0
	}
	var dot, qnorm, enorm float64
	for i := 0; i < len(q1.Bytes); i++ {
		v1 := float64(q1.Bytes[i])
		v2 := float64(q2.Bytes[i])
		dot += v1 * v2
		qnorm += v1 * v1
		enorm += v2 * v2
	}
	if qnorm == 0 || enorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(qnorm) * math.Sqrt(enorm))
}
