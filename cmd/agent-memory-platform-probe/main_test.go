package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformprobe"
)

const (
	testRequestID = "8c73a4f1-027e-4ea5-95c8-75eb9a847ac4"
	testTraceID   = "0123456789abcdef0123456789abcdef"
)

func TestRunCollectsReadyContentFreeStagingReceipt(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 30, 0, 0, time.UTC)
	edge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_edge/health/ready" || r.Header.Get("X-Request-ID") != testRequestID || r.Header.Get("X-Trace-ID") != testTraceID {
			t.Errorf("unexpected edge request: path=%s headers=%v", r.URL.Path, r.Header)
		}
		w.Header().Set("X-Request-ID", testRequestID)
		w.Header().Set("X-Trace-ID", testTraceID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","private":"response body must be discarded"}`))
	}))
	defer edge.Close()
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/evidence/requests/"+testRequestID {
			t.Errorf("unexpected internal path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(platformprobe.Observation{
			RequestID: testRequestID, TraceID: testTraceID, Service: "api", Operation: "GET:/health/ready",
			Status: 200, Outcome: "success", ObservedAt: now,
		})
	}))
	defer internal.Close()

	receiptPath := filepath.Join(t.TempDir(), "probe.json")
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(t, edge.URL, internal.URL, receiptPath), &stdout, &stderr, dependencies{
		client: edge.Client(), now: func() time.Time { return now }, ids: func() (string, string) { return testRequestID, testTraceID }, sleep: func(time.Duration) {},
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	if !receipt.Ready || len(receipt.Checks) != 3 || receipt.ReleaseID != "baseline-release" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.ReceiptWritten || result.PassedCount != 3 || result.CheckCount != 3 {
		t.Fatalf("unexpected report: %+v", result)
	}
	contents, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{edge.URL, internal.URL, "private", "response body", "authorization", "tenant", "customer"} {
		if strings.Contains(string(contents), forbidden) || strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("probe evidence leaked %q", forbidden)
		}
	}
}

func TestRunWritesUnreadyReceiptWhenObservationIsMissing(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 30, 0, 0, time.UTC)
	edge := correlatedEdgeServer()
	defer edge.Close()
	internal := httptest.NewServer(http.NotFoundHandler())
	defer internal.Close()
	receiptPath := filepath.Join(t.TempDir(), "probe.json")
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(t, edge.URL, internal.URL, receiptPath), &stdout, &stderr, dependencies{
		client: edge.Client(), now: func() time.Time { return now }, ids: func() (string, string) { return testRequestID, testTraceID }, sleep: func(time.Duration) {},
	})
	if code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	if receipt.Ready || checkOutcome(receipt, platformprobe.CheckTelemetryObservation) != platformprobe.OutcomeFailed {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestRunRejectsUnsafeURLsInvalidReleaseAndRedirectFollowing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithDependencies(nil, &stdout, &stderr, dependencies{}); code != 2 {
		t.Fatalf("missing args code=%d, want 2", code)
	}

	release := writeRelease(t)
	receipt := filepath.Join(t.TempDir(), "probe.json")
	for name, urls := range map[string][2]string{
		"http edge":       {"http://staging.example", "http://127.0.0.1:8080"},
		"edge path":       {"https://staging.example/customer", "http://127.0.0.1:8080"},
		"public http api": {"https://staging.example", "http://public.example"},
	} {
		t.Run(name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			args := []string{"--release", release, "--edge-url", urls[0], "--internal-url", urls[1], "--receipt", receipt}
			if code := runWithDependencies(args, &stdout, &stderr, dependencies{}); code != 2 {
				t.Fatalf("unsafe URL code=%d, want 2", code)
			}
		})
	}

	invalidRelease := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidRelease, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies([]string{"--release", invalidRelease, "--edge-url", "https://staging.example", "--internal-url", "http://127.0.0.1:8080", "--receipt", receipt}, &stdout, &stderr, dependencies{}); code != 1 {
		t.Fatalf("invalid release code=%d, want 1", code)
	}

	followed := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	internal := httptest.NewServer(http.NotFoundHandler())
	defer internal.Close()
	stdout.Reset()
	stderr.Reset()
	redirectReceipt := filepath.Join(t.TempDir(), "redirect.json")
	code := runWithDependencies(arguments(t, redirect.URL, internal.URL, redirectReceipt), &stdout, &stderr, dependencies{
		client: redirect.Client(), now: time.Now, ids: func() (string, string) { return testRequestID, testTraceID }, sleep: func(time.Duration) {},
	})
	if code != 3 || followed {
		t.Fatalf("redirect code=%d followed=%v stderr=%s", code, followed, stderr.String())
	}
}

func correlatedEdgeServer() *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-ID", testRequestID)
		w.Header().Set("X-Trace-ID", testTraceID)
		w.WriteHeader(http.StatusOK)
	}))
}

func arguments(t *testing.T, edgeURL, internalURL, receipt string) []string {
	t.Helper()
	return []string{"--release", writeRelease(t), "--edge-url", edgeURL, "--internal-url", internalURL, "--receipt", receipt}
}

func writeRelease(t *testing.T) string {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	value := map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging",
		"namespace": "agent-memory-staging", "kubernetes_context": "staging-context",
		"release_id": "baseline-release", "started_at": "2026-08-09T17:00:00Z", "completed_at": "2026-08-09T17:10:00Z", "outcome": "passed",
		"images":    map[string]string{"api": "registry.example/api@" + digest, "worker": "registry.example/worker@" + digest, "reconciler": "registry.example/reconciler@" + digest, "migrate": "registry.example/migrate@" + digest},
		"migration": map[string]string{"outcome": "complete"}, "rollouts": map[string]string{"outcome": "healthy"},
		"deployments": []map[string]string{{"name": "agent-memory-api", "revision": "7"}, {"name": "agent-memory-worker", "revision": "7"}, {"name": "agent-memory-reconciler", "revision": "7"}},
		"rollback":    map[string]bool{"attempted": false, "succeeded": false},
	}
	contents, _ := json.Marshal(value)
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadReceipt(t *testing.T, path string) platformprobe.Receipt {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt platformprobe.Receipt
	if err := json.Unmarshal(contents, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func checkOutcome(receipt platformprobe.Receipt, id platformprobe.CheckID) platformprobe.Outcome {
	for _, check := range receipt.Checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}
