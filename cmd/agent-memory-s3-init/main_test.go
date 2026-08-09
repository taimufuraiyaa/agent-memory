package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/minio/minio-go/v7"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type recordingBuckets struct {
	existing map[string]bool
	created  map[string]minio.MakeBucketOptions
}

func (r *recordingBuckets) BucketExists(_ context.Context, name string) (bool, error) {
	return r.existing[name], nil
}

func (r *recordingBuckets) MakeBucket(_ context.Context, name string, options minio.MakeBucketOptions) error {
	if r.created == nil {
		r.created = map[string]minio.MakeBucketOptions{}
	}
	r.created[name] = options
	return nil
}

func TestEnsureBucketsCreatesOnlyMissingBucketsAndLocksAudit(t *testing.T) {
	client := &recordingBuckets{existing: map[string]bool{"agent-memory-vault": true}}
	if err := ensureBuckets(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(client.created) != 3 {
		t.Fatalf("created buckets = %v", client.created)
	}
	if _, ok := client.created["agent-memory-vault"]; ok {
		t.Fatal("existing vault bucket was recreated")
	}
	if !client.created["agent-memory-audit"].ObjectLocking {
		t.Fatal("audit bucket was created without Object Lock")
	}
	for _, name := range []string{"agent-memory-quarantine", "agent-memory-exports"} {
		if client.created[name].ObjectLocking {
			t.Fatalf("ordinary bucket %s unexpectedly enabled Object Lock", name)
		}
	}
}

func TestLoadConfigRejectsMissingOrUnsafeEndpoint(t *testing.T) {
	getenv := func(name string) string {
		values := map[string]string{
			"AGENT_MEMORY_OBJECT_ENDPOINT":   "http://minio:4566",
			"AGENT_MEMORY_OBJECT_ACCESS_KEY": "test",
			"AGENT_MEMORY_OBJECT_SECRET_KEY": "test",
		}
		return values[name]
	}
	if _, err := loadConfig(getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("empty configuration was accepted")
	}
	if _, err := loadConfig(func(name string) string {
		if name == "AGENT_MEMORY_OBJECT_ENDPOINT" {
			return "file:///tmp/object-store"
		}
		return "test"
	}); err == nil {
		t.Fatal("non-HTTP object endpoint was accepted")
	}
}

func TestProbeHealthRequiresRunningS3Service(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://localhost:4566/_localstack/health" {
			t.Fatalf("health URL = %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"services":{"s3":"running"}}`))}, nil
	})}
	if err := probeHealth(context.Background(), "http://localhost:4566/_localstack/health", client); err != nil {
		t.Fatal(err)
	}
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"services":{"s3":"stopped"}}`))}, nil
	})
	if err := probeHealth(context.Background(), "http://localhost:4566/_localstack/health", client); err == nil {
		t.Fatal("stopped S3 service was considered healthy")
	}
}
