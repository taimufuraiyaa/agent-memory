// Package harnessmodel decides which model provider serves a harness call and accounts
// for what it cost. Routing considers live access, whether the provider may be sent the
// data, whether the call fits, expected cost, observed latency and provider-reported
// cache evidence. A recommendation from Jev can reorder eligible providers but never
// make an ineligible one eligible, and every fallback stays inside the eligible set.
package harnessmodel

import (
	"errors"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// Class orders data sensitivity. A provider may be sent data up to its MaxClass.
type Class int

const (
	ClassPublic Class = iota
	ClassInternal
	ClassSensitive
	ClassRestricted
)

func (c Class) valid() bool { return c >= ClassPublic && c <= ClassRestricted }

// MaxPricePerMTok bounds a price so token counts up to harness.MaxUsageTokens multiplied
// by it can be summed in int64 without overflow: 2^31 * 1e9 * 3 terms < 2^63.
const MaxPricePerMTok int64 = 1_000_000_000

const perMillion = 1_000_000

// Pricing is micro currency units per million tokens. Cached input is charged at its
// own rate, which must not exceed the uncached rate; a provider without caching sets it
// equal to the input rate.
type Pricing struct {
	InputPerMTok       int64
	CachedInputPerMTok int64
	OutputPerMTok      int64
}

var ErrInvalid = errors.New("invalid model profile")

func (p Pricing) Validate() error {
	for _, v := range []int64{p.InputPerMTok, p.CachedInputPerMTok, p.OutputPerMTok} {
		if v < 0 || v > MaxPricePerMTok {
			return fmt.Errorf("%w: price out of range", ErrInvalid)
		}
	}
	if p.CachedInputPerMTok > p.InputPerMTok {
		return fmt.Errorf("%w: cached input cannot cost more than uncached input", ErrInvalid)
	}
	return nil
}

// Cost is the deterministic charge for one call's usage, rounded up to a whole micro
// unit. Usage must already be validated by the harness contract (non-negative, bounded,
// cached no more than input); anything else costs nothing here so a hostile report can
// neither overflow nor go negative.
func (p Pricing) Cost(u harness.Usage) int64 {
	if p.Validate() != nil || u.InputTokens < 0 || u.OutputTokens < 0 || u.CachedInputTokens < 0 ||
		u.InputTokens > harness.MaxUsageTokens || u.OutputTokens > harness.MaxUsageTokens || u.CachedInputTokens > u.InputTokens {
		return 0
	}
	uncached := int64(u.InputTokens - u.CachedInputTokens)
	total := uncached*p.InputPerMTok + int64(u.CachedInputTokens)*p.CachedInputPerMTok + int64(u.OutputTokens)*p.OutputPerMTok
	cost := (total + perMillion - 1) / perMillion
	if cost > harness.MaxCostMicros {
		return harness.MaxCostMicros
	}
	return cost
}

// CacheSavings is what prompt-prefix reuse saved on one call, and it is claimed only
// when the request carried a stable prefix identity AND the provider itself reported
// cached input tokens. Retrieval or query caching is never counted here, and nothing is
// inferred from price alone.
func (p Pricing) CacheSavings(u harness.Usage, prefixID string) int64 {
	if prefixID == "" || u.CachedInputTokens <= 0 || p.Validate() != nil || u.CachedInputTokens > u.InputTokens {
		return 0
	}
	saved := int64(u.CachedInputTokens) * (p.InputPerMTok - p.CachedInputPerMTok)
	return saved / perMillion
}

// Profile is the operator's static description of one provider. It is configuration,
// not proof of access: live access is checked separately for every selection.
type Profile struct {
	Provider   harness.ProviderID
	Capability harness.CapabilityID
	Pricing    Pricing
	// MaxClass is the most sensitive data this provider may be sent.
	MaxClass Class
	// ContextTokens is the provider's window; MaxOutputTokens its output cap.
	ContextTokens   int
	MaxOutputTokens int
}

func (p Profile) Validate() error {
	if p.Provider == "" || p.Capability == "" || !p.MaxClass.valid() || p.ContextTokens < 1 || p.MaxOutputTokens < 1 || p.MaxOutputTokens > p.ContextTokens {
		return fmt.Errorf("%w: %s", ErrInvalid, p.Provider)
	}
	return p.Pricing.Validate()
}
