package engine

import (
	"fmt"
	"math"
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
