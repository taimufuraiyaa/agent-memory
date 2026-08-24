package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/architectureevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	published := false
	code := runWithDependencies([]string{"--inventory", "i", "--input", "d", "--receipt", "o"}, &stdout, &stderr, dependencies{
		collect: func(string, string, time.Time) (architectureevidence.Receipt, error) {
			return architectureevidence.Receipt{Input: architectureevidence.Input{Ready: true, IndependentFailureDomainCount: 2}, ComponentCount: 8, ComponentDomainReviewCount: 48, DataFlowCount: 8, IntegrationCount: 3, CheckCount: 10, PassedCount: 10}, nil
		},
		publish: func(string, architectureevidence.Receipt) error { published = true; return nil },
	})
	if code != 0 || !published || !strings.Contains(stdout.String(), `"component_count":8`) || strings.Contains(stdout.String(), "review_id") {
		t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnready(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies([]string{"--inventory", "i", "--input", "d", "--receipt", "o"}, &stdout, &stderr, dependencies{collect: func(string, string, time.Time) (architectureevidence.Receipt, error) {
		return architectureevidence.Receipt{}, nil
	}, publish: func(string, architectureevidence.Receipt) error { return nil }})
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
}

func TestRunRejectsArgumentsAndCollectionFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d", code)
	}
	code := runWithDependencies([]string{"--inventory", "i", "--input", "d", "--receipt", "o"}, &stdout, &stderr, dependencies{collect: func(string, string, time.Time) (architectureevidence.Receipt, error) {
		return architectureevidence.Receipt{}, errors.New("bad architecture evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "bad architecture evidence") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
