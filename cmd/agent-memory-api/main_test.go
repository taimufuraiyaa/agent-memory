package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/config"
)

type readinessPingerFunc func(context.Context) error

func (f readinessPingerFunc) Ping(ctx context.Context) error { return f(ctx) }

type readinessBucketFunc func(context.Context, string) (bool, error)

func (f readinessBucketFunc) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return f(ctx, bucket)
}

type readinessQueueFunc func(context.Context) error

func (f readinessQueueFunc) FlushWithContext(ctx context.Context) error { return f(ctx) }

func TestIdentityBoundaryUsesDevelopmentAuthenticatorOnlyInDevelopment(t *testing.T) {
	authenticator, profiles, err := newIdentityBoundary(context.Background(), config.Config{
		Environment: config.Development, IdentityMode: config.IdentityDevelopment,
		DevAuthToken: "development-token", DevSubject: "development|member",
		DevEmail: "member@example.test", DevDisplayName: "Member",
	})
	if err != nil || authenticator == nil || profiles == nil {
		t.Fatalf("development identity boundary authenticator=%v profiles=%v err=%v", authenticator, profiles, err)
	}
}

func TestIdentityBoundaryFailsClosedOnOIDCDiscoveryFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "identity unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, _, err := newIdentityBoundary(context.Background(), config.Config{
		Environment: config.Staging, IdentityMode: config.IdentityOIDC,
		OIDCIssuer: server.URL, OIDCAudience: "agent-memory-web",
	})
	if err == nil {
		t.Fatal("expected managed identity discovery failure")
	}
}

func TestIdentityBoundaryUsesOIDCWhenExplicitlySelectedInDevelopment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "identity unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, _, err := newIdentityBoundary(context.Background(), config.Config{
		Environment: config.Development, IdentityMode: config.IdentityOIDC,
		DevAuthToken: "would-be-unsafe-fallback", DevSubject: "development|member", DevEmail: "member@example.test",
		OIDCIssuer: server.URL, OIDCAudience: "agent-memory-local",
	})
	if err == nil {
		t.Fatal("explicit local OIDC mode fell back to development identity")
	}
}

func TestCheckDependenciesRequiresDatabaseObjectBucketAndQueue(t *testing.T) {
	ctx := context.Background()
	database := readinessPingerFunc(func(context.Context) error { return nil })
	objects := readinessBucketFunc(func(_ context.Context, bucket string) (bool, error) {
		return bucket == "agent-memory-quarantine", nil
	})
	queue := readinessQueueFunc(func(context.Context) error { return nil })
	if err := checkDependencies(ctx, database, objects, queue); err != nil {
		t.Fatalf("healthy dependencies rejected: %v", err)
	}

	failures := []struct {
		name     string
		database readinessPinger
		objects  readinessBucketChecker
		queue    readinessQueue
	}{
		{name: "database", database: readinessPingerFunc(func(context.Context) error { return errors.New("down") }), objects: objects, queue: queue},
		{name: "object_error", database: database, objects: readinessBucketFunc(func(context.Context, string) (bool, error) { return false, errors.New("down") }), queue: queue},
		{name: "object_missing", database: database, objects: readinessBucketFunc(func(context.Context, string) (bool, error) { return false, nil }), queue: queue},
		{name: "queue", database: database, objects: objects, queue: readinessQueueFunc(func(context.Context) error { return errors.New("down") })},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			if err := checkDependencies(ctx, failure.database, failure.objects, failure.queue); err == nil {
				t.Fatal("unhealthy dependencies reported ready")
			}
		})
	}
}
