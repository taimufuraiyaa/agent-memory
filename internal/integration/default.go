package integration

func NewDefaultRegistry() (*Registry, error) {
	registry := NewRegistry()
	for _, adapter := range []Adapter{NewCodexAdapter(), NewClaudeAdapter()} {
		if err := registry.Register(adapter); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
