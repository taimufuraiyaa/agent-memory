package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

func TestJevOnboardingRequiresExplicitApproval(t *testing.T) {
	store := jevconfig.NewTokenStore(t.TempDir())
	var output bytes.Buffer
	called := false
	err := promptJevSetup(context.Background(), store, strings.NewReader("\n"), &output, func() (string, error) {
		called = true
		return "private-token", nil
	})
	if err != nil || called {
		t.Fatalf("default-no approval failed: called=%v err=%v", called, err)
	}
	configured, err := store.Configured(context.Background())
	if err != nil || configured {
		t.Fatalf("decline changed credentials: %v %v", configured, err)
	}
}

func TestJevOnboardingEOFAndInvalidTokenDoNotConfigure(t *testing.T) {
	store := jevconfig.NewTokenStore(t.TempDir())
	if err := promptJevSetup(context.Background(), store, strings.NewReader(""), &bytes.Buffer{}, func() (string, error) {
		return "unused", nil
	}); err != nil {
		t.Fatalf("EOF should skip: %v", err)
	}
	var output bytes.Buffer
	err := promptJevSetup(context.Background(), store, strings.NewReader("y\n"), &output, func() (string, error) {
		return "invalid\ntoken", nil
	})
	if err == nil || strings.Contains(err.Error()+output.String(), "invalid\ntoken") {
		t.Fatalf("invalid token was accepted or exposed: %v", err)
	}
	configured, err := store.Configured(context.Background())
	if err != nil || configured {
		t.Fatalf("invalid token was stored: configured=%v err=%v", configured, err)
	}
}

func TestJevOnboardingStoresApprovedTokenWithoutEcho(t *testing.T) {
	store := jevconfig.NewTokenStore(t.TempDir())
	var output bytes.Buffer
	err := promptJevSetup(context.Background(), store, strings.NewReader("yes\n"), &output, func() (string, error) {
		return "private-token", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, present, err := store.Load(context.Background())
	if err != nil || !present || got != "private-token" {
		t.Fatalf("approved credential missing: present=%v err=%v", present, err)
	}
	if strings.Contains(output.String(), "private-token") {
		t.Fatal("secret echoed to output")
	}
}

func TestJevOnboardingNeverReplacesExistingToken(t *testing.T) {
	store := jevconfig.NewTokenStore(t.TempDir())
	if err := store.Save(context.Background(), "original-token"); err != nil {
		t.Fatal(err)
	}
	called := false
	err := promptJevSetup(context.Background(), store, strings.NewReader("yes\n"), &bytes.Buffer{}, func() (string, error) {
		called = true
		return "replacement-token", nil
	})
	if err != nil || called {
		t.Fatalf("existing credential prompt occurred: called=%v err=%v", called, err)
	}
	got, _, _ := store.Load(context.Background())
	if got != "original-token" {
		t.Fatal("existing credential was replaced")
	}
}

func TestJevOnboardingErrorDoesNotExposeSecret(t *testing.T) {
	store := jevconfig.NewTokenStore(t.TempDir())
	var output bytes.Buffer
	err := promptJevSetup(context.Background(), store, strings.NewReader("y\n"), &output, func() (string, error) {
		return "", errors.New("private-token")
	})
	if err == nil || strings.Contains(err.Error()+output.String(), "private-token") {
		t.Fatalf("secret exposed by error: %v", err)
	}
}

func TestJevOnboardingGate(t *testing.T) {
	if !shouldPromptJevSetup(true, true, "text", false, false) {
		t.Fatal("interactive text setup should offer Jev")
	}
	for _, tc := range []struct {
		in, out, dry, hooks bool
		format              string
	}{
		{false, true, false, false, "text"},
		{true, false, false, false, "text"},
		{true, true, false, false, "json"},
		{true, true, true, false, "text"},
		{true, true, false, true, "text"},
	} {
		if shouldPromptJevSetup(tc.in, tc.out, tc.format, tc.dry, tc.hooks) {
			t.Fatalf("unexpected prompt: %+v", tc)
		}
	}
}
