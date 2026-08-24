package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/alphaevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	published := false
	code := runWithDependencies(validArgs(), &stdout, &stderr, dependencies{
		collect: func(string, string, string, string, string, string, time.Time) (alphaevidence.Receipt, error) {
			return alphaevidence.Receipt{Input: alphaevidence.Input{Ready: true, AccountCount: 3, SourceCount: 8}, FormatCount: 4, StageCount: 11, SupportCaseCount: 2, CheckCount: 9, PassedCount: 9}, nil
		},
		publish: func(string, alphaevidence.Receipt) error { published = true; return nil },
	})
	if code != 0 || !published || !strings.Contains(stdout.String(), `"account_count":3`) || strings.Contains(stdout.String(), "cohort_id") {
		t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnready(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(), &stdout, &stderr, dependencies{
		collect: func(string, string, string, string, string, string, time.Time) (alphaevidence.Receipt, error) {
			return alphaevidence.Receipt{}, nil
		},
		publish: func(string, alphaevidence.Receipt) error { return nil },
	})
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
}

func TestRunRejectsArgumentsAndCollectionFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d", code)
	}
	code := runWithDependencies(validArgs(), &stdout, &stderr, dependencies{
		collect: func(string, string, string, string, string, string, time.Time) (alphaevidence.Receipt, error) {
			return alphaevidence.Receipt{}, errors.New("bad alpha evidence")
		},
	})
	if code != 1 || !strings.Contains(stderr.String(), "bad alpha evidence") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}

func validArgs() []string {
	return []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--journey", "j", "--input", "d", "--receipt", "o"}
}
