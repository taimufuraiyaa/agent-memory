package modelgateway

import "fmt"

const RemoteProviderName = "openai-compatible"

type RuntimeProviderConfig struct {
	Name      string
	Endpoint  string
	APIKey    string
	Model     string
	Dimension int
	Retention string
}

// NewRuntimeProvider is the single composition boundary used by hosted
// binaries, preventing API and worker model policy from drifting apart.
func NewRuntimeProvider(config RuntimeProviderConfig) (Provider, error) {
	switch config.Name {
	case "local-minilm-scaffold":
		return NewEmbeddingProvider(DevelopmentProvider{}, config.Retention)
	case RemoteProviderName:
		return NewHTTPProvider(HTTPProviderConfig{
			Name: config.Name, Endpoint: config.Endpoint, APIKey: config.APIKey,
			Model: config.Model, Dimension: config.Dimension, Retention: config.Retention,
		})
	default:
		return nil, fmt.Errorf("unsupported hosted model provider %q", config.Name)
	}
}
