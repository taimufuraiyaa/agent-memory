package engine

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

type SecurityPolicy struct {
	EnablePII          bool
	MaxContentChars    int
	MaxWritesPerMinute int
	AllowlistPatterns  []string
	OverrideTags       []string
}

type SecurityEvent struct {
	Workspace string
	Category  string
	Reason    string
}

type SecurityValidationInput struct {
	Workspace string
	Content   string
	Tags      []string
}

type securityLimiter struct {
	mu     sync.Mutex
	writes map[string][]time.Time
}

type RegexSecurityFilter struct {
	policy    SecurityPolicy
	secrets   []*regexp.Regexp
	pii       []*regexp.Regexp
	poison    []*regexp.Regexp
	allowlist []*regexp.Regexp
	onAnomaly func(SecurityEvent)
	limiter   securityLimiter
	nowFn     func() time.Time
}

func DefaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{
		EnablePII:          true,
		MaxContentChars:    2000,
		MaxWritesPerMinute: 100,
		OverrideTags:       []string{"allow-sensitive", "security-override"},
	}
}

func NewRegexSecurityFilter() *RegexSecurityFilter {
	return NewRegexSecurityFilterWithPolicy(DefaultSecurityPolicy(), nil)
}

func NewRegexSecurityFilterWithPolicy(policy SecurityPolicy, onAnomaly func(SecurityEvent)) *RegexSecurityFilter {
	if policy.MaxContentChars <= 0 {
		policy.MaxContentChars = 2000
	}
	if policy.MaxWritesPerMinute <= 0 {
		policy.MaxWritesPerMinute = 100
	}
	f := &RegexSecurityFilter{
		policy:    policy,
		onAnomaly: onAnomaly,
		secrets:   SecretPatterns,
		pii:       PIISecretPatterns,
		poison: []*regexp.Regexp{
			regexp.MustCompile(`(?i)ignore (all )?previous instructions`),
			regexp.MustCompile(`(?i)reveal (the )?system prompt`),
			regexp.MustCompile(`(?i)exfiltrate|steal credentials|bypass guardrails`),
		},
		limiter: securityLimiter{
			writes: map[string][]time.Time{},
		},
		nowFn: time.Now,
	}
	for _, raw := range policy.AllowlistPatterns {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if re, err := regexp.Compile(raw); err == nil {
			f.allowlist = append(f.allowlist, re)
		}
	}
	return f
}

func (f *RegexSecurityFilter) Validate(_ context.Context, in SecurityValidationInput) error {
	content := strings.TrimSpace(in.Content)
	if len(content) > f.policy.MaxContentChars {
		return errors.New("validation rejected: content too large")
	}
	if f.isOverride(in.Tags) || f.isAllowlisted(content) {
		return nil
	}
	if !f.allowByRate(in.Workspace) {
		return errors.New("validation rejected: rate limit exceeded")
	}
	if f.matchAny(f.secrets, content) {
		return errors.New("validation rejected: secret detected")
	}
	if f.policy.EnablePII && f.matchAny(f.pii, content) {
		return errors.New("validation rejected: pii detected")
	}
	if f.matchAny(f.poison, content) {
		if f.onAnomaly != nil {
			f.onAnomaly(SecurityEvent{
				Workspace: in.Workspace,
				Category:  "poisoning_anomaly",
				Reason:    "poisoning pattern detected",
			})
		}
		return errors.New("validation rejected: poisoning pattern detected")
	}
	return nil
}

func (f *RegexSecurityFilter) isOverride(tags []string) bool {
	allowed := make(map[string]struct{}, len(f.policy.OverrideTags))
	for _, t := range f.policy.OverrideTags {
		allowed[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, t := range tags {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(t))]; ok {
			return true
		}
	}
	return false
}

func (f *RegexSecurityFilter) isAllowlisted(content string) bool {
	for _, re := range f.allowlist {
		if re.MatchString(content) {
			return true
		}
	}
	return false
}

func (f *RegexSecurityFilter) matchAny(patterns []*regexp.Regexp, content string) bool {
	for _, p := range patterns {
		if p.MatchString(content) {
			return true
		}
	}
	return false
}

func (f *RegexSecurityFilter) allowByRate(workspace string) bool {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "_default"
	}
	now := f.nowFn().UTC()
	cutoff := now.Add(-1 * time.Minute)
	f.limiter.mu.Lock()
	defer f.limiter.mu.Unlock()
	history := f.limiter.writes[workspace]
	next := make([]time.Time, 0, len(history)+1)
	for _, ts := range history {
		if ts.After(cutoff) {
			next = append(next, ts)
		}
	}
	if len(next) >= f.policy.MaxWritesPerMinute {
		f.limiter.writes[workspace] = next
		return false
	}
	next = append(next, now)
	f.limiter.writes[workspace] = next
	return true
}
