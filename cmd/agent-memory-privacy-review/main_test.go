package main

import (
	"bytes"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/privacyreviewevidence"
	"strings"
	"testing"
	"time"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var out, errOut bytes.Buffer
	published := false
	code := runWithDependencies([]string{"--input", "input.json", "--receipt", "receipt.json"}, &out, &errOut, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, time.Time) (privacyreviewevidence.Receipt, error) {
		return privacyreviewevidence.Receipt{Ready: true, SurfaceCount: 4, SurfacePassedCount: 4, ContractCount: 5, ContractPassedCount: 5, CheckCount: 8, PassedCount: 8}, nil
	}, publish: func(string, privacyreviewevidence.Receipt) error { published = true; return nil }})
	if code != 0 || !published || !strings.Contains(out.String(), `"surface_count":4`) || strings.Contains(out.String(), "review_id") {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
}
func TestRunReturnsThreeForValidUnready(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runWithDependencies([]string{"--input", "i", "--receipt", "r"}, &out, &errOut, dependencies{collect: func(string, time.Time) (privacyreviewevidence.Receipt, error) {
		return privacyreviewevidence.Receipt{Ready: false}, nil
	}, publish: func(string, privacyreviewevidence.Receipt) error { return nil }})
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
}
func TestRunRejectsArgumentsAndCollectionFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("code=%d", code)
	}
	out.Reset()
	errOut.Reset()
	code := runWithDependencies([]string{"--input", "i", "--receipt", "r"}, &out, &errOut, dependencies{collect: func(string, time.Time) (privacyreviewevidence.Receipt, error) {
		return privacyreviewevidence.Receipt{}, errors.New("bad evidence")
	}})
	if code != 1 || !strings.Contains(errOut.String(), "bad evidence") {
		t.Fatalf("code=%d err=%s", code, errOut.String())
	}
}
