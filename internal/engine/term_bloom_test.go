package engine

import (
	"fmt"
	"math"
	"math/bits"
	"testing"
)

func TestTermBloomContainsEveryInsertedTermAndRejectsDefiniteMisses(t *testing.T) {
	filter, err := NewTermBloom(1000, 0.01)
	if err != nil {
		t.Fatalf("NewTermBloom: %v", err)
	}
	inserted := []string{"bloom", "sqlite", "orders.api", "retry-policy"}
	for _, term := range inserted {
		filter.Add(term)
	}
	for _, term := range inserted {
		if !filter.MightContain(term) {
			t.Fatalf("inserted term %q became a false negative", term)
		}
	}
	if filter.MightContain("definitely-not-inserted") {
		t.Fatal("unexpected false positive in deterministic small fixture")
	}
}

func BenchmarkTermBloomProbe(b *testing.B) {
	for _, capacity := range []int64{1_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("capacity_%d", capacity), func(b *testing.B) {
			filter, err := NewTermBloom(capacity, 0.01)
			if err != nil {
				b.Fatalf("NewTermBloom: %v", err)
			}
			for i := int64(0); i < capacity; i++ {
				filter.Add(fmt.Sprintf("term-%d", i))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = filter.MightContain("definite-miss")
			}
		})
	}
}

func TestTermBloomRoundTripChecksumAndVersions(t *testing.T) {
	filter, err := NewTermBloom(128, 0.01)
	if err != nil {
		t.Fatalf("NewTermBloom: %v", err)
	}
	filter.Add("sqlite")
	bitmap := filter.Bitmap()
	loaded, err := LoadTermBloom(bitmap, filter.BitCount(), filter.HashCount())
	if err != nil {
		t.Fatalf("LoadTermBloom: %v", err)
	}
	if !loaded.MightContain("sqlite") {
		t.Fatal("round-tripped filter lost inserted term")
	}
	if TermBloomChecksum(bitmap) == "" {
		t.Fatal("expected stable bitmap checksum")
	}
	if TermBloomFormatVersion == "" || TermBloomHashVersion == "" {
		t.Fatal("filter and hash versions must be explicit")
	}
}

func TestTermBloomSizingTargetsConfiguredFalsePositiveRate(t *testing.T) {
	filter, err := NewTermBloom(10_000, 0.01)
	if err != nil {
		t.Fatalf("NewTermBloom: %v", err)
	}
	bitsPerItem := float64(filter.BitCount()) / 10_000
	if bitsPerItem < 9 || bitsPerItem > 11 {
		t.Fatalf("unexpected bits per item %.2f", bitsPerItem)
	}
	if filter.HashCount() < 6 || filter.HashCount() > 8 {
		t.Fatalf("unexpected hash count %d", filter.HashCount())
	}
	if estimated := filter.EstimatedFalsePositiveProbability(10_000); math.Abs(estimated-0.01) > 0.003 {
		t.Fatalf("estimated false positive probability %.6f is outside target", estimated)
	}
}

func TestTermBloomProbesDistinctNoCollapse(t *testing.T) {
	// Composite capacities are the degenerate-prone ones (h2 may share a
	// factor with m); 8/64/1024 cover the powers of two production sizing
	// produces. For every term, the k probes must land on k distinct slots.
	capacities := []uint64{6, 10, 100, 6800, 8, 64, 1024}
	const termsPerCapacity = 2000
	for _, m := range capacities {
		k := uint64(8)
		if k > m {
			k = m
		}
		for i := 0; i < termsPerCapacity; i++ {
			term := fmt.Sprintf("term-%d-%d", m, i)
			h1, h2 := termBloomProbeHashes(term, m)
			if h2%m == 0 || gcd64(h2, m) != 1 {
				t.Fatalf("m=%d term %q: h2=%d is not coprime to m", m, term, h2)
			}
			seen := make(map[uint64]bool, k)
			for j := uint64(0); j < k; j++ {
				pos := (h1 + j*h2) % m
				if seen[pos] {
					t.Fatalf("m=%d term %q: probe %d collided at slot %d", m, term, j, pos)
				}
				seen[pos] = true
			}
		}
	}
}

func TestTermBloomGuardFixesDegenerateHashPair(t *testing.T) {
	// Find a term whose raw secondary hash is divisible by m; under the old
	// formula every probe collapses onto the single slot h1 mod m.
	const m = uint64(6)
	var term string
	var rawH1, rawH2 uint64
	found := false
	for i := 0; i < 10_000; i++ {
		candidate := fmt.Sprintf("degenerate-%d", i)
		h1, h2 := termBloomHashes(candidate)
		if h2%m == 0 {
			term, rawH1, rawH2, found = candidate, h1, h2, true
			break
		}
	}
	if !found {
		t.Fatal("setup failed: no term with h2 divisible by 6 found")
	}
	if rawH2%m != 0 {
		t.Fatalf("setup broken: term %q h2=%d not divisible by %d", term, rawH2, m)
	}
	// Under exact mod-m arithmetic the old formula collapses every probe
	// onto the single slot h1 mod m (the raw uint64 multiply in the old code
	// could overflow, which only perturbs the collapse further).
	probe0 := rawH1 % m
	for i := uint64(1); i < 6; i++ {
		if (rawH1+i*(rawH2%m))%m != probe0 {
			t.Fatalf("setup broken: expected old probes to collapse, term %q", term)
		}
	}

	h1, h2 := termBloomProbeHashes(term, m)
	if h1 != rawH1%m {
		t.Fatalf("guard changed h1's probe base: got %d want %d", h1, rawH1%m)
	}
	if h2%m == 0 || gcd64(h2, m) != 1 {
		t.Fatalf("guard produced non-coprime h2=%d for m=%d", h2, m)
	}
	seen := make(map[uint64]bool, 6)
	for i := uint64(0); i < 6; i++ {
		pos := (h1 + i*h2) % m
		if seen[pos] {
			t.Fatalf("guarded probes still collapse at slot %d", pos)
		}
		seen[pos] = true
	}

	// End to end through the filter: adding the degenerate term must set
	// exactly hashCount distinct bits and stay findable.
	filter, err := LoadTermBloom(make([]byte, 1), 6, 6)
	if err != nil {
		t.Fatalf("LoadTermBloom: %v", err)
	}
	filter.Add(term)
	setBits := 0
	for _, b := range filter.bitmap {
		setBits += bits.OnesCount8(b)
	}
	if setBits != 6 {
		t.Fatalf("Add set %d bits for m=6 k=6; expected 6 distinct slots", setBits)
	}
	if !filter.MightContain(term) {
		t.Fatal("inserted degenerate term became a false negative")
	}
}

func TestNewTermBloomRejectsSingleBitFilter(t *testing.T) {
	// Sizing that yields a raw bit count of 1 (capacity 1 with a loose FPP
	// target) must be rejected: a one-slot filter cannot host the coprime
	// probe guard and every query would be a false positive.
	if _, err := NewTermBloom(1, 0.9); err == nil {
		t.Fatal("expected NewTermBloom to reject a filter sized to 1 bit")
	}
}

func TestLoadTermBloomRejectsMalformedBitmap(t *testing.T) {
	if _, err := LoadTermBloom([]byte{0x01}, 16, 7); err == nil {
		t.Fatal("expected bitmap length mismatch to fail")
	}
	if _, err := NewTermBloom(0, 0.01); err == nil {
		t.Fatal("expected zero capacity to fail")
	}
	if _, err := NewTermBloom(10, 1); err == nil {
		t.Fatal("expected invalid false positive target to fail")
	}
}
