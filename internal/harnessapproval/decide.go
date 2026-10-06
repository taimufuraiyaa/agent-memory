package harnessapproval

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"
)

// codeAlphabet leaves out characters that are easy to confuse when read aloud or copied.
const codeAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// Code is the short code a person types to approve. It is derived from the approval and
// its digest, shown only next to the reviewed change, and longer for strict approvals.
// It is not a secret: it proves a person read the prompt and typed an answer, which a
// piped "yes" or a held Enter key cannot do.
func Code(r Record) string {
	sum := sha256.Sum256([]byte("harness-approval-code\x00" + r.ID + "\x00" + r.Digest))
	n := 4
	if r.Friction == FrictionStrict {
		n = 8
	}
	chars := make([]byte, n)
	for i := range chars {
		chars[i] = codeAlphabet[int(sum[i])%len(codeAlphabet)]
	}
	if n == 8 {
		return string(chars[:4]) + "-" + string(chars[4:])
	}
	return string(chars)
}

func normalizeCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.ToLower(strings.TrimSpace(code)))
}

// Decision is a person's answer to one approval.
type Decision struct {
	Approve bool
	// Stop ends the run along with a denial.
	Stop bool
	// Code must match Code(record) to approve.
	Code string
	// Via names the channel, for the audit, such as "terminal".
	Via string
}

// Decide records a person's answer to a pending approval. A wrong code is counted and
// the approval is denied after MaxCodeFailures of them.
func (s *Store) Decide(ctx context.Context, id string, d Decision) (Record, error) {
	if !nameRE.MatchString(d.Via) {
		return Record{}, fmt.Errorf("%w: channel", ErrInvalid)
	}
	var out Record
	var failure error
	err := s.mutating(ctx, func(dir string) error {
		record, err := s.read(dir, id)
		if err != nil {
			return err
		}
		record, err = s.expireIfDue(dir, record)
		if err != nil {
			return err
		}
		if record.State == StateExpired {
			out, failure = record, ErrExpired
			return nil
		}
		if record.State != StatePending {
			out, failure = record, ErrNotLive
			return nil
		}
		now := s.now().UTC()
		switch {
		case !d.Approve:
			record.State, record.Stop, record.DecidedAt, record.DecidedVia, record.Reason = StateDenied, d.Stop, now, d.Via, "denied_by_person"
			record.clearContent()
			if err := s.write(dir, record); err != nil {
				return err
			}
			out = record
			return s.audit(dir, record, "denied", stopDetail(d.Stop))
		case subtle.ConstantTimeCompare([]byte(normalizeCode(d.Code)), []byte(normalizeCode(Code(record)))) != 1:
			record.CodeFailures++
			if record.CodeFailures >= MaxCodeFailures {
				record.State, record.DecidedAt, record.DecidedVia, record.Reason = StateDenied, now, d.Via, "too_many_codes"
				record.clearContent()
			}
			if err := s.write(dir, record); err != nil {
				return err
			}
			out, failure = record, ErrCode
			return s.audit(dir, record, "code_rejected", "")
		}
		record.State, record.DecidedAt, record.DecidedVia = StateApproved, now, d.Via
		if err := s.write(dir, record); err != nil {
			return err
		}
		out = record
		return s.audit(dir, record, "approved", "")
	})
	if err != nil {
		return Record{}, err
	}
	return out.public(), failure
}

func stopDetail(stop bool) string {
	if stop {
		return "stop"
	}
	return ""
}

// Consume turns an approved record into a consumed one, exactly once, and returns the
// exact arguments to replay. The run and digest must match what was approved. Content is
// removed from disk in the same step, so a crash after this call can lose a result but
// can never run the same approval again.
func (s *Store) Consume(ctx context.Context, id, runID, digest string) (Record, error) {
	var out Record
	var failure error
	err := s.mutating(ctx, func(dir string) error {
		record, err := s.read(dir, id)
		if err != nil {
			return err
		}
		record, err = s.expireIfDue(dir, record)
		if err != nil {
			return err
		}
		switch {
		case record.State == StateExpired:
			failure = ErrExpired
			return nil
		case record.State != StateApproved:
			failure = ErrNotLive
			return nil
		case record.RunID != runID || subtle.ConstantTimeCompare([]byte(record.Digest), []byte(digest)) != 1:
			failure = ErrNotUsable
			return nil
		}
		replay := record
		replay.Arguments = append([]byte(nil), record.Arguments...)
		record.State, record.ConsumedAt = StateConsumed, s.now().UTC()
		record.clearContent()
		if err := s.write(dir, record); err != nil {
			return err
		}
		out = replay
		out.State, out.ConsumedAt = record.State, record.ConsumedAt
		return s.audit(dir, record, "consumed", "")
	})
	if err != nil {
		return Record{}, err
	}
	if failure != nil {
		return Record{}, failure
	}
	return out, nil
}

// Resolve ends a live approval for a reason other than a person's answer: the grant was
// revoked, the approval was superseded or it expired. It is idempotent for the same
// target and drops the content.
func (s *Store) Resolve(ctx context.Context, id string, to State, reason string) (Record, error) {
	switch to {
	case StateRevoked, StateExpired, StateSuperseded, StateDenied:
	default:
		return Record{}, fmt.Errorf("%w: state", ErrInvalid)
	}
	if !reasonRE.MatchString(reason) {
		return Record{}, fmt.Errorf("%w: reason", ErrInvalid)
	}
	var out Record
	var failure error
	err := s.mutating(ctx, func(dir string) error {
		record, err := s.read(dir, id)
		if err != nil {
			return err
		}
		if record.State == to {
			out = record
			return nil
		}
		if !record.State.Live() {
			out, failure = record, ErrNotLive
			return nil
		}
		record.State, record.Reason = to, reason
		record.clearContent()
		if err := s.write(dir, record); err != nil {
			return err
		}
		out = record
		return s.audit(dir, record, string(to), reason)
	})
	if err != nil {
		return Record{}, err
	}
	return out.public(), failure
}

// Finish records what happened when a consumed approval was applied.
func (s *Store) Finish(ctx context.Context, id, outcome string) error {
	if !reasonRE.MatchString(outcome) {
		return fmt.Errorf("%w: outcome", ErrInvalid)
	}
	return s.mutating(ctx, func(dir string) error {
		record, err := s.read(dir, id)
		if err != nil {
			return err
		}
		if record.State != StateConsumed || record.Outcome != "" {
			return ErrNotLive
		}
		record.Outcome = outcome
		if err := s.write(dir, record); err != nil {
			return err
		}
		return s.audit(dir, record, "finished", outcome)
	})
}

// Note appends an audit event for an approval, such as an undo by the approval command.
func (s *Store) Note(ctx context.Context, id, event, detail string) error {
	if !reasonRE.MatchString(event) || (detail != "" && !reasonRE.MatchString(detail)) {
		return fmt.Errorf("%w: event", ErrInvalid)
	}
	return s.mutating(ctx, func(dir string) error {
		record, err := s.read(dir, id)
		if err != nil {
			return err
		}
		return s.audit(dir, record, event, detail)
	})
}
