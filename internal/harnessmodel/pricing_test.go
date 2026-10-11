package harnessmodel

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// $2.50 per million input tokens, $0.25 cached, $10 output, in micro units.
var price = Pricing{InputPerMTok: 2_500_000, CachedInputPerMTok: 250_000, OutputPerMTok: 10_000_000}

func TestCostIsExactAndRoundsUp(t *testing.T) {
	for name, tc := range map[string]struct {
		usage harness.Usage
		want  int64
	}{
		"zero":            {harness.Usage{}, 0},
		"input only":      {harness.Usage{InputTokens: 1000}, 2500},
		"output only":     {harness.Usage{OutputTokens: 1000}, 10000},
		"cached discount": {harness.Usage{InputTokens: 1000, CachedInputTokens: 1000}, 250},
		"mixed":           {harness.Usage{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 100}, 600*2500/1000 + 400*250/1000 + 1000},
		"one token":       {harness.Usage{InputTokens: 1}, 3},
		"fraction":        {harness.Usage{CachedInputTokens: 0, OutputTokens: 1}, 10},
	} {
		if got := price.Cost(tc.usage); got != tc.want {
			t.Errorf("%s = %d, want %d", name, got, tc.want)
		}
	}
	if got := (Pricing{InputPerMTok: 1, CachedInputPerMTok: 1, OutputPerMTok: 1}).Cost(harness.Usage{InputTokens: 1}); got != 1 {
		t.Errorf("a tiny charge must round up to one micro unit, got %d", got)
	}
}

func TestCostMatchesExactArithmeticAndNeverOverflows(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	exact := func(p Pricing, u harness.Usage) int64 {
		total := new(big.Int)
		total.Add(total, new(big.Int).Mul(big.NewInt(int64(u.InputTokens-u.CachedInputTokens)), big.NewInt(p.InputPerMTok)))
		total.Add(total, new(big.Int).Mul(big.NewInt(int64(u.CachedInputTokens)), big.NewInt(p.CachedInputPerMTok)))
		total.Add(total, new(big.Int).Mul(big.NewInt(int64(u.OutputTokens)), big.NewInt(p.OutputPerMTok)))
		total.Add(total, big.NewInt(perMillion-1))
		total.Div(total, big.NewInt(perMillion))
		if total.Cmp(big.NewInt(harness.MaxCostMicros)) > 0 {
			return harness.MaxCostMicros
		}
		return total.Int64()
	}
	for trial := 0; trial < 5000; trial++ {
		in := rng.Intn(harness.MaxUsageTokens)
		if trial%3 == 0 {
			in = harness.MaxUsageTokens - rng.Intn(10)
		}
		cached := 0
		if in > 0 {
			cached = rng.Intn(in + 1)
		}
		out := rng.Intn(harness.MaxUsageTokens)
		p := Pricing{InputPerMTok: rng.Int63n(MaxPricePerMTok + 1), OutputPerMTok: rng.Int63n(MaxPricePerMTok + 1)}
		if trial%4 == 0 {
			p = Pricing{InputPerMTok: MaxPricePerMTok, OutputPerMTok: MaxPricePerMTok}
		}
		p.CachedInputPerMTok = rng.Int63n(p.InputPerMTok + 1)
		u := harness.Usage{InputTokens: in, CachedInputTokens: cached, OutputTokens: out}
		got := p.Cost(u)
		if got < 0 || got > harness.MaxCostMicros || got != exact(p, u) {
			t.Fatalf("trial %d: cost %d, exact %d for %+v at %+v", trial, got, exact(p, u), u, p)
		}
	}
	extreme := Pricing{InputPerMTok: MaxPricePerMTok, CachedInputPerMTok: MaxPricePerMTok, OutputPerMTok: MaxPricePerMTok}
	if got := extreme.Cost(harness.Usage{InputTokens: harness.MaxUsageTokens, OutputTokens: harness.MaxUsageTokens}); got != harness.MaxCostMicros {
		t.Fatalf("the largest possible charge must clamp, got %d", got)
	}
}

func TestACostIsMonotonicInUsage(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for trial := 0; trial < 2000; trial++ {
		u := harness.Usage{InputTokens: rng.Intn(1_000_000), OutputTokens: rng.Intn(1_000_000)}
		more := harness.Usage{InputTokens: u.InputTokens + rng.Intn(1000), OutputTokens: u.OutputTokens + rng.Intn(1000)}
		if price.Cost(more) < price.Cost(u) {
			t.Fatalf("more usage cost less: %+v vs %+v", more, u)
		}
	}
}

func TestHostileUsageCostsNothingRatherThanOverflowing(t *testing.T) {
	for name, u := range map[string]harness.Usage{
		"negative input":  {InputTokens: -5},
		"negative output": {OutputTokens: -1},
		"negative cached": {InputTokens: 10, CachedInputTokens: -1},
		"cached > input":  {InputTokens: 10, CachedInputTokens: 11},
		"huge input":      {InputTokens: harness.MaxUsageTokens + 1},
		"huge output":     {OutputTokens: harness.MaxUsageTokens + 1},
	} {
		if got := price.Cost(u); got != 0 {
			t.Errorf("%s cost %d", name, got)
		}
	}
	if got := (Pricing{InputPerMTok: -1}).Cost(harness.Usage{InputTokens: 5}); got != 0 {
		t.Errorf("an invalid price charged %d", got)
	}
}

func TestPricingValidation(t *testing.T) {
	for name, p := range map[string]Pricing{
		"negative input":     {InputPerMTok: -1},
		"negative cached":    {CachedInputPerMTok: -1},
		"negative output":    {OutputPerMTok: -1},
		"over the bound":     {InputPerMTok: MaxPricePerMTok + 1, CachedInputPerMTok: 1},
		"cached above input": {InputPerMTok: 100, CachedInputPerMTok: 101},
		"output over bound":  {InputPerMTok: 1, CachedInputPerMTok: 1, OutputPerMTok: MaxPricePerMTok + 1},
	} {
		if p.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := price.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Pricing{}).Validate(); err != nil {
		t.Fatalf("a free provider is valid: %v", err)
	}
}

func TestCacheSavingsAreClaimedOnlyWhenQualified(t *testing.T) {
	cached := harness.Usage{InputTokens: 2000, CachedInputTokens: 1500, OutputTokens: 50}
	const prefix = "0123456789abcdef"
	want := int64(1500) * (2_500_000 - 250_000) / perMillion
	if got := price.CacheSavings(cached, prefix); got != want || want <= 0 {
		t.Fatalf("qualified savings = %d, want %d", got, want)
	}
	for name, tc := range map[string]struct {
		usage  harness.Usage
		prefix string
	}{
		"no prefix identity":     {cached, ""},
		"provider reported none": {harness.Usage{InputTokens: 2000, CachedInputTokens: 0}, prefix},
		"cached exceeds input":   {harness.Usage{InputTokens: 10, CachedInputTokens: 11}, prefix},
		"negative cached":        {harness.Usage{InputTokens: 10, CachedInputTokens: -3}, prefix},
	} {
		if got := price.CacheSavings(tc.usage, tc.prefix); got != 0 {
			t.Errorf("%s claimed %d", name, got)
		}
	}
	if got := (Pricing{InputPerMTok: 100, CachedInputPerMTok: 100}).CacheSavings(cached, prefix); got != 0 {
		t.Errorf("a provider with no discount saved %d", got)
	}
	// Savings can never exceed the cost the cached tokens would have had uncached.
	if saved, full := price.CacheSavings(cached, prefix), price.Cost(harness.Usage{InputTokens: 1500}); saved > full {
		t.Fatalf("saved %d of a %d charge", saved, full)
	}
}

func TestProfileValidation(t *testing.T) {
	ok := Profile{Provider: "p", Capability: "generation", Pricing: price, MaxClass: ClassInternal, ContextTokens: 8000, MaxOutputTokens: 1000}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Profile){
		"no provider":     func(p *Profile) { p.Provider = "" },
		"no capability":   func(p *Profile) { p.Capability = "" },
		"bad class":       func(p *Profile) { p.MaxClass = 9 },
		"negative class":  func(p *Profile) { p.MaxClass = -1 },
		"no window":       func(p *Profile) { p.ContextTokens = 0 },
		"no output":       func(p *Profile) { p.MaxOutputTokens = 0 },
		"output > window": func(p *Profile) { p.MaxOutputTokens = 9000 },
		"bad price":       func(p *Profile) { p.Pricing.CachedInputPerMTok = p.Pricing.InputPerMTok + 1 },
	} {
		p := ok
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
