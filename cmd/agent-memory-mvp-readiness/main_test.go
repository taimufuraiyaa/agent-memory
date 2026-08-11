package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/mvpreadinessevidence"
)

func TestRunPublishesReadyAggregateReceipt(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "receipt.json")
	var stdout, stderr bytes.Buffer
	exit := runWithDependencies(validArgs(destination), &stdout, &stderr, dependencies{
		now: func() time.Time { return time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC) },
		collect: func(_, _, _, _, _, _ string, _ time.Time) (mvpreadinessevidence.Receipt, error) {
			return mvpreadinessevidence.Receipt{Schema: mvpreadinessevidence.ReceiptSchemaV1, Ready: true, CanonicalControlCount: 57, FoundationalControlCount: 49, VerifiedFoundationalCount: 49, FinalMVPControlCount: 8, Gates: make([]mvpreadinessevidence.Gate, 8)}, nil
		},
	})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt not securely published: info=%v err=%v", info, err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"verified_foundational_count":49`)) || bytes.Contains(stdout.Bytes(), []byte("evidence_ref")) {
		t.Fatalf("unexpected aggregate output: %s", stdout.String())
	}
}

func TestRunReturnsThreeForValidUnreadyAndOneForInvalidEvidence(t *testing.T) {
	for name, test := range map[string]struct {
		receipt    mvpreadinessevidence.Receipt
		collectErr error
		want       int
	}{
		"unready": {receipt: mvpreadinessevidence.Receipt{Schema: mvpreadinessevidence.ReceiptSchemaV1, Ready: false}, want: 3},
		"invalid": {collectErr: errors.New("invalid evidence"), want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runWithDependencies(validArgs(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{
				collect: func(_, _, _, _, _, _ string, _ time.Time) (mvpreadinessevidence.Receipt, error) {
					return test.receipt, test.collectErr
				},
			})
			if exit != test.want {
				t.Fatalf("exit=%d want=%d stderr=%s", exit, test.want, stderr.String())
			}
		})
	}
}

func TestRunRejectsMissingArguments(t *testing.T) {
	if exit := runWithDependencies(nil, &bytes.Buffer{}, &bytes.Buffer{}, dependencies{}); exit != 2 {
		t.Fatalf("exit=%d want=2", exit)
	}
}

func validArgs(destination string) []string {
	return []string{"--catalog", "catalog.json", "--index", "index.json", "--artifacts-root", "artifacts", "--trust", "trust.json", "--approvals-dir", "approvals", "--input", "input.json", "--receipt", destination}
}
