package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
)

const (
	TermBloomFormatVersion = "bloom-v1"
	// v2: termBloomProbeHashes guarantees h2 is coprime to the bit count so
	// probes never collapse; persisted snapshots built with v1 probing are
	// rejected by the HashVersion gate and must be rebuilt.
	TermBloomHashVersion = "sha256-double-v2"
)

// TermBloom is a standard, non-counting Bloom filter for normalized terms.
type TermBloom struct {
	bitmap    []byte
	bitCount  uint64
	hashCount uint32
}

// NewTermBloom sizes a filter for the expected distinct capacity and target FPP.
func NewTermBloom(capacity int64, targetFPP float64) (*TermBloom, error) {
	if capacity <= 0 {
		return nil, errors.New("Bloom capacity must be positive")
	}
	if targetFPP <= 0 || targetFPP >= 1 {
		return nil, errors.New("Bloom false-positive probability must be between 0 and 1")
	}
	n := float64(capacity)
	m := uint64(math.Ceil(-n * math.Log(targetFPP) / math.Pow(math.Ln2, 2)))
	if m < 2 {
		// A one-slot filter cannot host the coprime probe guard: no probe
		// offset is non-zero mod 1, so every query would be a false positive.
		return nil, errors.New("Bloom bit count must be at least 2")
	}
	if remainder := m % 8; remainder != 0 {
		m += 8 - remainder
	}
	k := uint32(math.Round((float64(m) / n) * math.Ln2))
	if k == 0 {
		k = 1
	}
	return &TermBloom{
		bitmap:    make([]byte, m/8),
		bitCount:  m,
		hashCount: k,
	}, nil
}

// LoadTermBloom validates and loads a persisted immutable bitmap snapshot.
func LoadTermBloom(bitmap []byte, bitCount int64, hashCount int) (*TermBloom, error) {
	if bitCount <= 0 || hashCount <= 0 {
		return nil, errors.New("Bloom bit count and hash count must be positive")
	}
	expectedBytes := (uint64(bitCount) + 7) / 8
	if uint64(len(bitmap)) != expectedBytes {
		return nil, errors.New("Bloom bitmap length does not match bit count")
	}
	return &TermBloom{
		bitmap:    append([]byte(nil), bitmap...),
		bitCount:  uint64(bitCount),
		hashCount: uint32(hashCount),
	}, nil
}

// Add inserts one already-normalized term.
func (b *TermBloom) Add(term string) {
	if b == nil || b.bitCount == 0 {
		return
	}
	h1, h2 := termBloomProbeHashes(term, b.bitCount)
	for i := uint32(0); i < b.hashCount; i++ {
		position := (h1 + uint64(i)*h2) % b.bitCount
		b.bitmap[position/8] |= byte(1 << (position % 8))
	}
}

// MightContain returns false only for a definite miss.
func (b *TermBloom) MightContain(term string) bool {
	if b == nil || b.bitCount == 0 {
		return false
	}
	h1, h2 := termBloomProbeHashes(term, b.bitCount)
	for i := uint32(0); i < b.hashCount; i++ {
		position := (h1 + uint64(i)*h2) % b.bitCount
		if b.bitmap[position/8]&byte(1<<(position%8)) == 0 {
			return false
		}
	}
	return true
}

func (b *TermBloom) Bitmap() []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b.bitmap...)
}

func (b *TermBloom) BitCount() int64 {
	if b == nil {
		return 0
	}
	return int64(b.bitCount)
}

func (b *TermBloom) HashCount() int {
	if b == nil {
		return 0
	}
	return int(b.hashCount)
}

func (b *TermBloom) EstimatedFalsePositiveProbability(itemCount int64) float64 {
	if b == nil || b.bitCount == 0 || itemCount <= 0 {
		return 0
	}
	k := float64(b.hashCount)
	m := float64(b.bitCount)
	n := float64(itemCount)
	return math.Pow(1-math.Exp(-k*n/m), k)
}

func TermBloomChecksum(bitmap []byte) string {
	sum := sha256.Sum256(bitmap)
	return hex.EncodeToString(sum[:])
}

func termBloomHashes(term string) (uint64, uint64) {
	sum := sha256.Sum256([]byte(term))
	h1 := binary.LittleEndian.Uint64(sum[0:8])
	h2 := binary.LittleEndian.Uint64(sum[8:16])
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	return h1, h2
}

// termBloomProbeHashes returns the double-hash pair used to probe a filter
// with m bits. h1 is only reduced mod m (probe positions depend solely on
// h1 mod m, so the double-hash base offset is preserved), while h2 is
// deterministically re-derived until it is coprime to m. That guarantees the
// k probes (h1 + i*h2) mod m land on k distinct slots: without the guard, a
// term whose h2 shares a factor with m (in particular h2 % m == 0) collapses
// every probe onto a small subset of slots and drives its false-positive rate
// toward 1. Reducing both hashes also keeps the probe arithmetic free of
// uint64 overflow for any m < 2^61, and because the re-derivation happens
// exactly here, Add and MightContain always agree on the probe positions.
func termBloomProbeHashes(term string, m uint64) (uint64, uint64) {
	h1, h2 := termBloomHashes(term)
	if m < 2 {
		// One-slot (or empty) filters have no degenerate probe pattern.
		return h1, h2
	}
	h1 %= m
	h2 %= m
	for h2%m == 0 || gcd64(h2, m) != 1 {
		// Adding 1 mod m cycles through every residue, so a value coprime to
		// m is guaranteed to be reached for any m >= 2.
		h2 = (h2 + 1) % m
	}
	return h1, h2
}

func gcd64(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
