package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	amconfig "github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

// The terminal uses the same handlers as serve without opening a listener.
type tuiHandlerTransport struct {
	handler http.Handler
	mu      sync.RWMutex
	closed  bool
}
type tuiResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *tuiResponseWriter) Header() http.Header { return w.header }
func (w *tuiResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *tuiResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}
func (t *tuiHandlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return nil, errors.New("terminal service closed")
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	writer := &tuiResponseWriter{header: make(http.Header)}
	clone := request.Clone(request.Context())
	clone.RequestURI = clone.URL.RequestURI()
	clone.RemoteAddr = "127.0.0.1:0"
	clone.Host = "localhost"
	if clone.Body == nil {
		clone.Body = http.NoBody
	}
	t.handler.ServeHTTP(writer, clone)
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return &http.Response{StatusCode: writer.status, Header: writer.header, Body: io.NopCloser(bytes.NewReader(writer.body.Bytes())), Request: request}, nil
}
func newTUIHandlerClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: &tuiHandlerTransport{handler: handler}, Timeout: 2 * time.Minute}
}

func newLocalTUIConsole(ctx context.Context, config runtimeConfig, provider embeddings.Provider) (memorytui.ConsoleBackend, func() error, error) {
	if provider == nil {
		return nil, nil, errors.New("TUI embedding provider unavailable")
	}
	installed, err := amconfig.Load("")
	if err != nil {
		return nil, nil, err
	}
	if err := installed.Graph.Validate(installed.DataDir); err != nil {
		return nil, nil, err
	}
	scheduler := newServeScheduler(config, provider, io.Discard)
	service := &api.Service{Workspace: config.workspace, DBPath: config.dbPath, BaseDir: defaultAgentMemoryDataDir(), EmbeddingProvider: provider, Scheduler: scheduler, GraphRuntime: &installed.Graph}
	if err := api.ConfigureLocalRightsAttestation(ctx, service); err != nil {
		_ = service.Close()
		return nil, nil, err
	}
	if err := api.ConfigureLocalClientProfiles(service); err != nil {
		_ = service.Close()
		return nil, nil, err
	}
	backendClient := newTUIHandlerClient(api.NewMux(service))
	backend, err := memorytui.NewHTTPBackend("http://localhost", config.workspace, backendClient)
	if err != nil {
		_ = service.Close()
		return nil, nil, err
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	// Run the optional native graph worker when the installed backend provides
	// it; older graph backends keep their existing daemon-owned execution.
	if worker, ok := any(scheduler).(interface {
		runNativeGraphWorker(context.Context, amconfig.GraphConfig)
	}); ok {
		go func() { defer close(workerDone); worker.runNativeGraphWorker(workerCtx, installed.Graph) }()
	} else {
		close(workerDone)
	}
	return backend, func() error {
		cancelWorker()
		<-workerDone
		transport := backendClient.Transport.(*tuiHandlerTransport)
		transport.mu.Lock()
		transport.closed = true
		transport.mu.Unlock()
		return service.Close()
	}, nil
}
