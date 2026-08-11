package main

import (
	"bytes"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/gamedayevidence"
	"strings"
	"testing"
	"time"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var out, errOut bytes.Buffer
	published := false
	args := []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "d", "--receipt", "o"}
	code := runWithDependencies(args, &out, &errOut, dependencies{collect: func(string, string, string, string, string, time.Time) (gamedayevidence.Receipt, error) {
		return gamedayevidence.Receipt{Ready: true, DrillCount: 7, CheckCount: 49, PassedCount: 49, BundleCheckCount: 8, BundlePassedCount: 8}, nil
	}, publish: func(string, gamedayevidence.Receipt) error { published = true; return nil }})
	if code != 0 || !published || !strings.Contains(out.String(), `"drill_count":7`) || strings.Contains(out.String(), "bundle_id") {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
}
func TestRunReturnsThreeForValidUnready(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "d", "--receipt", "o"}
	code := runWithDependencies(args, &out, &errOut, dependencies{collect: func(string, string, string, string, string, time.Time) (gamedayevidence.Receipt, error) {
		return gamedayevidence.Receipt{}, nil
	}, publish: func(string, gamedayevidence.Receipt) error { return nil }})
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
}
func TestRunRejectsArgumentsAndCollectionFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("code=%d", code)
	}
	args := []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "d", "--receipt", "o"}
	code := runWithDependencies(args, &out, &errOut, dependencies{collect: func(string, string, string, string, string, time.Time) (gamedayevidence.Receipt, error) {
		return gamedayevidence.Receipt{}, errors.New("bad evidence")
	}})
	if code != 1 || !strings.Contains(errOut.String(), "bad evidence") {
		t.Fatalf("code=%d err=%s", code, errOut.String())
	}
}
