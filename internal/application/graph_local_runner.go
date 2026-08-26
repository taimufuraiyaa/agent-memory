package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
)

type LocalGraphCommand string

const (
	LocalGraphReadiness   LocalGraphCommand = "readiness"
	LocalGraphFullIndex   LocalGraphCommand = "full-index"
	LocalGraphIncremental LocalGraphCommand = "incremental-update"
	LocalGraphInspect     LocalGraphCommand = "inspect-artifacts"
)

type LocalGraphRunResult struct {
	State       string
	ReasonCode  string
	JobDir      string
	Response    map[string]any
	Duration    time.Duration
	OutputBytes int
}

type LocalGraphRunner struct {
	configuration config.GraphConfig
	now           func() time.Time
}

func NewLocalGraphRunner(configuration config.GraphConfig) *LocalGraphRunner {
	return &LocalGraphRunner{configuration: configuration, now: time.Now}
}

func (r *LocalGraphRunner) Run(ctx context.Context, command LocalGraphCommand, request any) (LocalGraphRunResult, error) {
	started := r.now()
	if !r.configuration.Enabled {
		return LocalGraphRunResult{State: "disabled", ReasonCode: "graph_index_disabled"}, nil
	}
	if !validLocalGraphCommand(command) {
		return LocalGraphRunResult{}, fmt.Errorf("unsupported local graph command")
	}
	if !filepath.IsAbs(r.configuration.Executable) {
		return LocalGraphRunResult{}, fmt.Errorf("graph adapter executable must be absolute")
	}
	info, err := os.Lstat(r.configuration.Executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return LocalGraphRunResult{State: "unavailable", ReasonCode: "adapter_unavailable"}, nil
	}
	jobDir, requestPath, err := r.createJobRequest(command, request)
	if err != nil {
		return LocalGraphRunResult{}, err
	}
	result := LocalGraphRunResult{JobDir: jobDir}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(r.configuration.TimeoutSeconds)*time.Second)
	defer cancel()
	args := []string{string(command)}
	if command != LocalGraphReadiness {
		args = append(args, "--request", requestPath)
	}
	child := exec.Command(r.configuration.Executable, args...)
	child.Dir = jobDir
	child.Env = r.adapterEnvironment(jobDir)
	configureGraphProcessGroup(child)
	stdout := newGraphCappedBuffer(r.configuration.MaxOutputBytes)
	stderr := newGraphCappedBuffer(r.configuration.MaxOutputBytes)
	child.Stdout, child.Stderr = stdout, stderr
	if err := child.Start(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LocalGraphRunResult{State: "unavailable", ReasonCode: "adapter_unavailable", JobDir: jobDir}, nil
		}
		return LocalGraphRunResult{}, fmt.Errorf("start graph adapter: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	failureCode := ""
	for {
		select {
		case waitErr = <-waited:
			goto finished
		case <-runCtx.Done():
			failureCode = "cancelled"
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				failureCode = "deadline_exceeded"
			}
			terminateGraphProcessGroup(child.Process.Pid, time.Duration(r.configuration.CancelGraceSeconds)*time.Second)
			waitErr = <-waited
			goto finished
		case <-ticker.C:
			if stdout.Exceeded() || stderr.Exceeded() {
				failureCode = "output_limit_exceeded"
			} else if bytesUsed, sizeErr := graphDirectoryBytes(jobDir, r.configuration.MaxDiskBytes); sizeErr != nil || bytesUsed > r.configuration.MaxDiskBytes {
				failureCode = "disk_limit_exceeded"
			} else if memory, cpu, usageErr := graphProcessGroupUsage(child.Process.Pid); usageErr == nil &&
				(memory > r.configuration.MaxMemoryBytes || cpu > time.Duration(r.configuration.MaxCPUSeconds)*time.Second) {
				if memory > r.configuration.MaxMemoryBytes {
					failureCode = "memory_limit_exceeded"
				} else {
					failureCode = "cpu_limit_exceeded"
				}
			}
			if failureCode != "" {
				terminateGraphProcessGroup(child.Process.Pid, time.Duration(r.configuration.CancelGraceSeconds)*time.Second)
				waitErr = <-waited
				goto finished
			}
		}
	}

finished:
	result.Duration = r.now().Sub(started)
	result.OutputBytes = stdout.Len()
	if failureCode != "" {
		result.State, result.ReasonCode = "failed", failureCode
		if failureCode == "cancelled" {
			result.State = "cancelled"
		}
		return result, nil
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		result.State, result.ReasonCode = "failed", "output_limit_exceeded"
		return result, nil
	}
	if err := json.Unmarshal(stdout.Bytes(), &result.Response); err != nil {
		result.State, result.ReasonCode = "failed", "invalid_adapter_output"
		return result, nil
	}
	if state, ok := result.Response["state"].(string); ok && state != "" {
		result.State = state
	} else if status, ok := result.Response["status"].(string); ok && status != "" {
		result.State = status
	} else if waitErr == nil {
		result.State = "completed"
	}
	if reason, ok := result.Response["reason_code"].(string); ok {
		result.ReasonCode = reason
	}
	if waitErr != nil && result.State == "" {
		result.State, result.ReasonCode = "failed", "adapter_failed"
	}
	return result, nil
}

func (r *LocalGraphRunner) createJobRequest(command LocalGraphCommand, request any) (string, string, error) {
	if !filepath.IsAbs(r.configuration.JobRoot) {
		return "", "", fmt.Errorf("graph job root must be absolute")
	}
	if err := os.MkdirAll(r.configuration.JobRoot, 0o700); err != nil {
		return "", "", err
	}
	if err := os.Chmod(r.configuration.JobRoot, 0o700); err != nil {
		return "", "", err
	}
	rootInfo, err := os.Lstat(r.configuration.JobRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("graph job root is unsafe")
	}
	jobDir, err := os.MkdirTemp(r.configuration.JobRoot, ".graph-job-")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(jobDir, 0o700); err != nil {
		return "", "", err
	}
	requestPath := filepath.Join(jobDir, "request.json")
	payload, err := json.Marshal(struct {
		ContractVersion string            `json:"contract_version"`
		Command         LocalGraphCommand `json:"command"`
		JobRoot         string            `json:"job_root"`
		Request         any               `json:"request,omitempty"`
	}{"graph-adapter/v1", command, jobDir, request})
	if err != nil {
		return "", "", err
	}
	if int64(len(payload)) > r.configuration.MaxRequestBytes {
		return "", "", fmt.Errorf("graph adapter request exceeds policy")
	}
	file, err := os.OpenFile(requestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return "", "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	return jobDir, requestPath, nil
}

func (r *LocalGraphRunner) adapterEnvironment(jobDir string) []string {
	environment := []string{"HOME=" + jobDir, "TMPDIR=" + jobDir, "PYTHONUNBUFFERED=1", "NO_COLOR=1"}
	for _, name := range r.configuration.CredentialEnv {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func validLocalGraphCommand(command LocalGraphCommand) bool {
	switch command {
	case LocalGraphReadiness, LocalGraphFullIndex, LocalGraphIncremental, LocalGraphInspect:
		return true
	default:
		return false
	}
}

type graphCappedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
}

func newGraphCappedBuffer(limit int64) *graphCappedBuffer { return &graphCappedBuffer{limit: limit} }

func (b *graphCappedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - int64(b.buffer.Len())
	if remaining <= 0 {
		b.exceeded = true
		return len(payload), nil
	}
	write := payload
	if int64(len(write)) > remaining {
		write = write[:remaining]
		b.exceeded = true
	}
	_, _ = b.buffer.Write(write)
	return len(payload), nil
}

func (b *graphCappedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}
func (b *graphCappedBuffer) Len() int       { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.Len() }
func (b *graphCappedBuffer) Exceeded() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.exceeded }

func graphDirectoryBytes(root string, stopAfter int64) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in graph job directory")
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			if total > stopAfter {
				return io.EOF
			}
		}
		return nil
	})
	if errors.Is(err, io.EOF) {
		return total, nil
	}
	return total, err
}

func parseGraphCPUTime(value string) (time.Duration, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid CPU time")
	}
	var seconds float64
	for _, part := range parts {
		number, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, err
		}
		seconds = seconds*60 + number
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
