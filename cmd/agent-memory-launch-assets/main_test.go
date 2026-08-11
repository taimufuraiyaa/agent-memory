package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchassetevidence"
)

func TestRunReportsAggregateReadyAndUnreadyWithoutAssetDetails(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{
				now: func() time.Time { return time.Unix(1, 0) },
				collect: func(string, string, string, string, string, time.Time) (launchassetevidence.Receipt, error) {
					return launchassetevidence.Receipt{
						Input:          launchassetevidence.Input{Ready: ready},
						Schema:         launchassetevidence.ReceiptSchemaV1,
						AssetCount:     7,
						LiveAssetCount: 7,
						CheckCount:     9,
						PassedCount:    9,
					}, nil
				},
			})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 {
				t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
			}
			for _, forbidden := range []string{"public_url_sha256", "owner_group", "asset_results", "review_id"} {
				if strings.Contains(stdout.String(), forbidden) {
					t.Fatalf("aggregate report leaked %q: %s", forbidden, stdout.String())
				}
			}
		})
	}
}

func TestRunRejectsMissingArgumentsAndCollectorFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d", code)
	}
	stderr.Reset()
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{
		collect: func(string, string, string, string, string, time.Time) (launchassetevidence.Receipt, error) {
			return launchassetevidence.Receipt{}, errors.New("failed")
		},
	})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "e", "--receipt", receipt}
}
