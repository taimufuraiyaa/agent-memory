package harnessworker

import "sync"

// Snapshot is the set of file revisions the workers of one parent have read. The first read of
// a file fixes the revision every sibling is working from; a later read that sees another
// revision means the file changed under them, and is reported so the caller can stop or replan.
type Snapshot struct {
	mu   sync.Mutex
	revs map[string]string
	max  int
}

// NewSnapshot returns an empty snapshot holding at most max files (default 1024).
func NewSnapshot(max int) *Snapshot {
	if max <= 0 {
		max = 1024
	}
	return &Snapshot{revs: map[string]string{}, max: max}
}

// Observe records a read and reports whether the file differs from what was first read.
func (s *Snapshot) Observe(path, revision string) (changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	known, ok := s.revs[path]
	switch {
	case ok:
		return known != revision
	case len(s.revs) < s.max:
		s.revs[path] = revision
	}
	return false
}

// Revision is the revision first read for a path.
func (s *Snapshot) Revision(path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rev, ok := s.revs[path]
	return rev, ok
}

// Len is how many files the snapshot holds.
func (s *Snapshot) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.revs)
}
