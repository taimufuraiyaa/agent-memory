//go:build unix

package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestNativeGraphRealRunnerCancellationPreservesServiceAndNextWorkspace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, configuration, runtime := nativeWorkerFixture(t)
	nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Synthetic first workspace awaits cancellation.")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// One workspace waits indefinitely; the next uses the compiled test helper
	// to produce bound artifacts. Both run through the actual process runner.
	script := "#!/bin/sh\nset -eu\n" +
		"if /usr/bin/grep -q '\"workspace_id\":\"native-worker-test\"' \"$3\"; then\n" +
		"  printf '%s\\n' \"$$\" > \"$PWD/adapter.pid\"\n" +
		"  /bin/sleep 60 & child=$!\n" +
		"  trap 'exit 0' TERM\n" +
		"  wait \"$child\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exec " + cancellationShellQuote(testBinary) + " -test.run='^TestNativeRealCancellationAdapterHelper$' -- \"$1\" \"$3\"\n"
	if err := os.WriteFile(runtime.Executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime.TimeoutSeconds, runtime.CancelGraceSeconds = 30, 1
	worker := NewNativeGraphWorker(runtime)
	worker.heartbeat = 10 * time.Millisecond
	worker.batch = 1
	worker.maxWait = time.Millisecond
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer service.Close()
	done := make(chan error, 1)
	go func() { done <- worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID) }()

	adapterPID := waitNativeCancellationAdapter(t, runtime.JobRoot, done)
	adapterGroup, err := syscall.Getpgid(adapterPID)
	if err != nil {
		t.Fatal(err)
	}
	parentGroup, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if adapterGroup != adapterPID || adapterGroup == parentGroup {
		t.Fatalf("adapter group is not isolated: adapter_pid=%d adapter_group=%d parent_group=%d", adapterPID, adapterGroup, parentGroup)
	}
	status := nativeWorkerStatus(t, store, configuration)
	if status.CurrentJob == nil {
		t.Fatalf("adapter ran without a claimed job: %+v", status)
	}
	if err := store.CancelGraphJob(ctx, configuration.Scope, status.CurrentJob.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled real adapter reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("real adapter did not stop after durable operator cancellation")
	}
	status = nativeWorkerStatus(t, store, configuration)
	if status.ActiveRevisionID != "" || status.PendingRecords != 1 || status.LastJobState != core.GraphJobCancelled {
		t.Fatalf("cancelled real adapter activated or acknowledged evidence: %+v", status)
	}
	if err := syscall.Kill(adapterPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("cancelled adapter leader remains alive: pid=%d err=%v", adapterPID, err)
	}
	response, err := service.Client().Get(service.URL)
	if err != nil {
		t.Fatalf("parent service stopped when its adapter was cancelled: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || ctx.Err() != nil {
		t.Fatalf("operator cancellation escaped job scope: status=%d context=%v", response.StatusCode, ctx.Err())
	}

	nextStore, nextConfiguration, _ := nativeWorkerFixture(t)
	nextConfiguration.ID, nextConfiguration.Scope.WorkspaceID = "next-default", "next-native-workspace"
	if err := nextStore.UpsertGraphConfiguration(ctx, nextConfiguration); err != nil {
		t.Fatal(err)
	}
	nativeWorkerMemory(t, nextStore, nextConfiguration.Scope.WorkspaceID, "Synthetic next workspace indexes after cancellation.")
	if err := worker.RunWorkspace(ctx, nextStore, nextConfiguration.Scope.WorkspaceID); err != nil {
		t.Fatalf("same worker could not index its next workspace: %v", err)
	}
	nextStatus := nativeWorkerStatus(t, nextStore, nextConfiguration)
	if !nextStatus.Fresh || nextStatus.ActiveRevisionID == "" || nextStatus.PendingRecords != 0 {
		t.Fatalf("next workspace failed to publish a complete revision: %+v", nextStatus)
	}
}

func waitNativeCancellationAdapter(t *testing.T, root string, done <-chan error) int {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			markers, err := filepath.Glob(filepath.Join(root, ".graph-job-*", "adapter.pid"))
			if err != nil {
				t.Fatal(err)
			}
			if len(markers) == 1 {
				contents, err := os.ReadFile(markers[0])
				if err == nil {
					pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
					if err == nil && pid > 0 {
						return pid
					}
				}
			}
		case err := <-done:
			t.Fatalf("worker exited before controlled adapter started: %v", err)
		case <-deadline.C:
			t.Fatal("controlled adapter did not start")
		}
	}
}

// This test is entered only by the controlled shell adapter's compiled helper.
// It produces the same validated fixture as fake-runner tests, across a real
// process boundary and without a Python runtime or any model calls.
func TestNativeRealCancellationAdapterHelper(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || len(os.Args) != separator+3 {
		return
	}
	var envelope struct {
		JobRoot string         `json:"job_root"`
		Request map[string]any `json:"request"`
	}
	request, err := os.ReadFile(os.Args[separator+2])
	if err == nil {
		err = json.Unmarshal(request, &envelope)
	}
	if err != nil {
		fmt.Fprintln(os.Stdout, `{"state":"failed","reason_code":"fixture_request_failed"}`)
		os.Exit(2)
	}
	runner := &nativeArtifactRunner{root: envelope.JobRoot}
	result, err := runner.Run(context.Background(), LocalGraphCommand(os.Args[separator+1]), envelope.Request)
	if err == nil {
		err = os.Rename(filepath.Join(result.JobDir, "output"), filepath.Join(envelope.JobRoot, "output"))
	}
	if err != nil {
		fmt.Fprintln(os.Stdout, `{"state":"failed","reason_code":"fixture_output_failed"}`)
		os.Exit(2)
	}
	_ = os.RemoveAll(result.JobDir)
	fmt.Fprintln(os.Stdout, `{"state":"completed"}`)
	os.Exit(0)
}

func cancellationShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
