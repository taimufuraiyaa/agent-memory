package harnessrun

// MutateForTest changes a stored run under the run's lock, so a test can recreate a state
// that would otherwise need an exact interleaving or a crash. It is compiled only into
// tests.
func (m *Manager) MutateForTest(id string, change func(*Run)) (Run, error) {
	return m.mutate(id, func(r *Run) (bool, error) {
		change(r)
		return true, nil
	})
}
