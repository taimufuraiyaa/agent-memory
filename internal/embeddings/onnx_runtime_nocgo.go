//go:build !cgo

package embeddings

import "errors"

func newORTMiniLMRuntime(string) (miniLMRuntime, error) {
	return nil, errors.New("ONNX runtime requires a CGO-enabled build")
}
