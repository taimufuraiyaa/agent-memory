package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

func TestRunSignsCanonicalEvidenceBoundApproval(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "owner-key.pem")
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(directory, "review.pdf")
	if err := os.WriteFile(evidencePath, []byte("reviewed release evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := run([]string{
		"--private-key", keyPath,
		"--gate", "private_beta",
		"--control", "security_review",
		"--decision", "approved",
		"--owner", "security-review",
		"--key-id", "security-2026",
		"--evidence", evidencePath,
		"--evidence-ref", "report://security/2026-08",
		"--valid-for", "168h",
	}, func() time.Time { return now }, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	var approval readiness.SignedApproval
	if err := json.Unmarshal(stdout.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	if approval.EvidenceSHA256 != "51e0f7171360a525432fd18b5af9c59e9aa7bd421cebee93a8d4ddc48629989a" || approval.ExpiresAt != now.Add(168*time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("unexpected artifact: %+v", approval)
	}
	bundle := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{KeyID: "security-2026", Owner: "security-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"private_beta"}, Controls: []string{"security_review"}}}}
	report, err := readiness.VerifyApprovals("private_beta", []string{"security_review"}, bundle, []readiness.SignedApproval{approval}, now)
	if err != nil || !report.Ready {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRunRejectsInsecurePrivateKeyPermissions(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "owner-key.pem")
	if err := os.WriteFile(keyPath, []byte("not needed"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(directory, "review.txt")
	if err := os.WriteFile(evidencePath, []byte("review"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := run([]string{"--private-key", keyPath, "--gate", "ga", "--control", "security_review", "--decision", "approved", "--owner", "security-review", "--key-id", "security-2026", "--evidence", evidencePath, "--evidence-ref", "report://security/review"}, time.Now, &stdout, &stderr)
	if exit != 1 || !strings.Contains(stderr.String(), "permissions") || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunPrintsRawPublicKeyForTrustBundle(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "owner-key.pem")
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"--private-key", keyPath, "--print-public-key"}, time.Now, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != base64.StdEncoding.EncodeToString(public) {
		t.Fatalf("unexpected public key %q", stdout.String())
	}
}

func TestSignerFileIdentityChangeIsRejected(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "owner-key.pem")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	validated, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOpenedRegularFile(validated, opened, maximumPrivateKeyBytes, true); err == nil {
		t.Fatal("a key replaced between path validation and open must fail closed")
	}
}

func TestLoadPrivateKeyRejectsPostOpenPathReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "owner-key.pem")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = loadPrivateKeyWithHook(path, func() {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("a private-key path replaced after open must fail closed")
	}
}

func TestHashRegularFileRejectsPostOpenPathReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "review.txt")
	if err := os.WriteFile(path, []byte("original review"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := hashRegularFileWithHook(path, func() {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement review"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("an evidence path replaced after open must fail closed")
	}
}
