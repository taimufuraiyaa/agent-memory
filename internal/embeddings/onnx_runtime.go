package embeddings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var ortInitMu sync.Mutex

type miniLMRuntime interface {
	Embed(ctx context.Context, input TokenizedInput) ([]float32, error)
	Close() error
}

type ortMiniLMRuntime struct {
	modelPath string

	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
}

func newORTMiniLMRuntime(modelDir string) (miniLMRuntime, error) {
	if strings.TrimSpace(modelDir) == "" {
		return nil, errors.New("model dir is required")
	}
	return &ortMiniLMRuntime{
		modelPath: filepath.Join(modelDir, "model.onnx"),
	}, nil
}

func (r *ortMiniLMRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == nil {
		return nil
	}
	err := r.session.Destroy()
	r.session = nil
	return err
}

func (r *ortMiniLMRuntime) Embed(ctx context.Context, input TokenizedInput) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session, err := r.ensureSession()
	if err != nil {
		return nil, err
	}

	shape := ort.NewShape(1, int64(len(input.InputIDs)))
	inputIDs, err := ort.NewTensor(shape, input.InputIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer inputIDs.Destroy()

	attentionMask, err := ort.NewTensor(shape, input.AttentionMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer attentionMask.Destroy()

	tokenTypeIDs, err := ort.NewTensor(shape, input.TokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer tokenTypeIDs.Destroy()

	outputs := []ort.Value{nil}
	if err := session.Run([]ort.Value{inputIDs, attentionMask, tokenTypeIDs}, outputs); err != nil {
		return nil, fmt.Errorf("run onnx session: %w", err)
	}
	defer func() {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
	}()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	outputTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output tensor type: %T", outputs[0])
	}
	return meanPoolHiddenState(outputTensor.GetData(), outputTensor.GetShape(), input.AttentionMask)
}

func (r *ortMiniLMRuntime) ensureSession() (*ort.DynamicAdvancedSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != nil {
		return r.session, nil
	}
	if err := ensureORTEnvironment(r.modelPath); err != nil {
		return nil, err
	}

	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create session options: %w", err)
	}
	defer options.Destroy()

	if setErr := options.SetLogSeverityLevel(ort.LoggingLevelError); setErr != nil {
		return nil, fmt.Errorf("set session log level: %w", setErr)
	}

	session, err := ort.NewDynamicAdvancedSession(
		r.modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		options,
	)
	if err != nil {
		return nil, fmt.Errorf("create onnx session: %w", err)
	}
	r.session = session
	return r.session, nil
}

func ensureORTEnvironment(modelPath string) error {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()
	if ort.IsInitialized() {
		return nil
	}
	sharedPath, err := resolveONNXRuntimeSharedLibrary(modelPath)
	if err != nil {
		return err
	}
	ort.SetSharedLibraryPath(sharedPath)
	if err := ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
		return fmt.Errorf("initialize onnx runtime with %q: %w", sharedPath, err)
	}
	return nil
}

func resolveONNXRuntimeSharedLibrary(modelPath string) (string, error) {
	for _, envVar := range []string{"AGENT_MEMORY_ONNX_RUNTIME_PATH", "ONNXRUNTIME_SHARED_LIBRARY_PATH"} {
		if value := strings.TrimSpace(os.Getenv(envVar)); value != "" {
			return value, nil
		}
	}

	modelDir := filepath.Dir(modelPath)
	dataDir := filepath.Dir(filepath.Dir(modelDir))
	candidates := make([]string, 0, 16)
	for _, dir := range []string{
		modelDir,
		filepath.Join(modelDir, "onnxruntime"),
		filepath.Join(modelDir, "lib"),
		filepath.Join(dataDir, "onnxruntime"),
		filepath.Join(dataDir, "onnxruntime", "lib"),
		filepath.Join(dataDir, "lib"),
	} {
		candidates = append(candidates, concreteRuntimeLibraryCandidates(dir)...)
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate, nil
		}
	}

	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll", nil
	case "darwin":
		return "libonnxruntime.dylib", nil
	default:
		return "libonnxruntime.so", nil
	}
}

func concreteRuntimeLibraryCandidates(dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}

	names := []string{}
	switch runtime.GOOS {
	case "windows":
		names = append(names, "onnxruntime.dll")
	case "darwin":
		names = append(names, "libonnxruntime.dylib", "onnxruntime.dylib")
	default:
		names = append(names, "libonnxruntime.so", "onnxruntime.so")
	}

	out := make([]string, 0, len(names)+2)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if fileInfo, err := os.Stat(path); err == nil && !fileInfo.IsDir() {
			out = append(out, path)
		}
	}
	for _, pattern := range []string{
		filepath.Join(dir, "libonnxruntime.so.*"),
		filepath.Join(dir, "libonnxruntime.*.dylib"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if fileInfo, err := os.Stat(match); err == nil && !fileInfo.IsDir() {
				out = append(out, match)
			}
		}
	}
	return out
}

func meanPoolHiddenState(data []float32, shape []int64, attentionMask []int64) ([]float32, error) {
	if len(shape) != 3 {
		return nil, fmt.Errorf("unexpected output rank: %v", shape)
	}
	if shape[0] != 1 {
		return nil, fmt.Errorf("unexpected batch size: %d", shape[0])
	}
	seqLen := int(shape[1])
	hiddenSize := int(shape[2])
	if hiddenSize != MiniLMDimension {
		return nil, fmt.Errorf("unexpected hidden size: %d", hiddenSize)
	}
	if seqLen <= 0 || seqLen > len(attentionMask) {
		return nil, fmt.Errorf("unexpected sequence length: %d", seqLen)
	}
	if want := seqLen * hiddenSize; len(data) != want {
		return nil, fmt.Errorf("unexpected output size: got %d want %d", len(data), want)
	}

	pooled := make([]float32, hiddenSize)
	var weight float32
	for tokenIndex := range seqLen {
		if attentionMask[tokenIndex] == 0 {
			continue
		}
		base := tokenIndex * hiddenSize
		for dim := range hiddenSize {
			pooled[dim] += data[base+dim]
		}
		weight++
	}
	if weight == 0 {
		return nil, errors.New("attention mask produced empty pooling window")
	}
	for dim := range pooled {
		pooled[dim] /= weight
	}
	return normalize(pooled), nil
}
