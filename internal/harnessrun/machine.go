package harnessrun

import (
	"fmt"
	"time"
)

// transitions is the complete state machine. Terminal states have no exits.
// running may return to queued only for crash recovery and clarification resume.
var transitions = map[State][]State{
	StateQueued:         {StateRunning, StateCancelled, StateFailed},
	StateRunning:        {StateNeedsAttention, StateCancelling, StateCompleted, StatePartial, StateFailed, StateQueued},
	StateNeedsAttention: {StateQueued, StateCancelled, StateFailed},
	StateCancelling:     {StateCancelled, StateFailed},
}

func canTransition(from, to State) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// move applies one state change: it validates the edge, bumps the generation,
// stamps the time, clears attention when leaving needs-attention, and records a
// content-free event.
func (r *Run) move(to State, code string, now time.Time) error {
	if !canTransition(r.State, to) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidState, r.State, to)
	}
	if code != "" && !codeRE.MatchString(code) {
		return fmt.Errorf("%w: reason code", ErrInvalid)
	}
	r.State = to
	if to != StateNeedsAttention {
		r.Attention = nil
	}
	if to.Terminal() {
		r.Pending = nil
	}
	r.Code = code
	r.touch(now)
	r.addEvent("state", code, now)
	return nil
}

func (r *Run) touch(now time.Time) {
	r.Generation++
	r.UpdatedAt = now
}

// addEvent appends to the bounded event ring; the oldest event is dropped first.
func (r *Run) addEvent(kind, code string, now time.Time) {
	if r.NextSeq == 0 {
		r.NextSeq = 1
	}
	r.Events = append(r.Events, Event{Seq: r.NextSeq, At: now, Kind: kind, State: r.State, Turn: r.Checkpoint.Turn, Code: code})
	r.NextSeq++
	if len(r.Events) > MaxEvents {
		r.Events = append([]Event(nil), r.Events[len(r.Events)-MaxEvents:]...)
	}
}

// addChunk keeps bounded context: the goal chunk is permanent and the newest
// MaxChunks others survive.
func (r *Run) addChunk(kind, text string, turn int) Chunk {
	chunk := Chunk{ID: fmt.Sprintf("c%d", r.NextSeq), Kind: kind, Turn: turn, Text: sanitize(text, MaxChunkBytes)}
	r.Chunks = append(r.Chunks, chunk)
	others := 0
	for _, c := range r.Chunks {
		if c.Kind != "goal" {
			others++
		}
	}
	for others > MaxChunks {
		for i, c := range r.Chunks {
			if c.Kind != "goal" {
				r.Chunks = append(r.Chunks[:i:i], r.Chunks[i+1:]...)
				others--
				break
			}
		}
	}
	return chunk
}

// addArtifact records a bounded result; at most MaxArtifacts are kept.
func (r *Run) addArtifact(kind, text string, turn int, digest func(string) string) {
	text = sanitize(text, MaxArtifactBytes)
	artifact := Artifact{ID: fmt.Sprintf("a%d", r.NextSeq), Kind: kind, Turn: turn, Bytes: len(text), SHA256: digest(text), Text: text}
	r.Artifacts = append(r.Artifacts, artifact)
	if len(r.Artifacts) > MaxArtifacts {
		r.Artifacts = append([]Artifact(nil), r.Artifacts[len(r.Artifacts)-MaxArtifacts:]...)
	}
}

// remember stores an idempotency record, evicting the oldest beyond the bound.
func (r *Run) remember(key string, record idemRecord) {
	if r.Idem == nil {
		r.Idem = make(map[string]idemRecord)
	}
	r.Idem[key] = record
	for len(r.Idem) > MaxIdempotency {
		var oldest string
		var oldestAt time.Time
		for k, v := range r.Idem {
			if oldest == "" || v.At.Before(oldestAt) {
				oldest, oldestAt = k, v.At
			}
		}
		delete(r.Idem, oldest)
	}
}
