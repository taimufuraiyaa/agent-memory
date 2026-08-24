package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

const (
	maximumPrivateKeyBytes  = 16 << 10
	maximumApprovalValidity = 90 * 24 * time.Hour
)

func main() {
	os.Exit(run(os.Args[1:], time.Now, os.Stdout, os.Stderr))
}

func run(args []string, now func() time.Time, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-release-approval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	privateKeyPath := flags.String("private-key", "", "Path to an owner-only Ed25519 PKCS#8 PEM private key")
	printPublicKey := flags.Bool("print-public-key", false, "Print the raw Ed25519 public key in base64 for a trust bundle")
	gate := flags.String("gate", "", "Release gate: private_beta|public_beta|ga|external_evidence")
	control := flags.String("control", "", "Approval control")
	decision := flags.String("decision", "", "Decision: approved|rejected")
	owner := flags.String("owner", "", "Accountable owner identifier")
	keyID := flags.String("key-id", "", "Trusted public-key identifier")
	evidencePath := flags.String("evidence", "", "Path to the reviewed evidence file")
	evidenceRef := flags.String("evidence-ref", "", "Content-free durable evidence URI")
	validFor := flags.Duration("valid-for", 7*24*time.Hour, "Approval validity duration (maximum 2160h)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*privateKeyPath) == "" {
		fmt.Fprintln(stderr, "an owner-only private key is required")
		return 2
	}
	privateKey, err := loadPrivateKey(*privateKeyPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer clear(privateKey)
	if *printPublicKey {
		publicKey := privateKey.Public().(ed25519.PublicKey)
		fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(publicKey))
		return 0
	}
	if strings.TrimSpace(*evidencePath) == "" || *validFor <= 0 || *validFor > maximumApprovalValidity {
		fmt.Fprintln(stderr, "private key, evidence file, and a validity duration up to 2160h are required")
		return 2
	}
	digest, err := hashRegularFile(*evidencePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	issuedAt := now().UTC()
	approval := readiness.SignedApproval{
		Schema: readiness.ApprovalArtifactSchema, Gate: strings.TrimSpace(*gate), Control: strings.TrimSpace(*control),
		Decision: strings.TrimSpace(*decision), Owner: strings.TrimSpace(*owner), KeyID: strings.TrimSpace(*keyID),
		EvidenceRef: strings.TrimSpace(*evidenceRef), EvidenceSHA256: digest,
		IssuedAt: issuedAt.Format(time.RFC3339Nano), ExpiresAt: issuedAt.Add(*validFor).Format(time.RFC3339Nano),
	}
	payload, err := readiness.CanonicalApprovalPayload(approval)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(approval); err != nil {
		fmt.Fprintln(stderr, "encode approval artifact")
		return 1
	}
	return 0
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	return loadPrivateKeyWithHook(path, nil)
}

func loadPrivateKeyWithHook(path string, afterOpen func()) (ed25519.PrivateKey, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() {
		return nil, errors.New("private key must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("read private key")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, errors.New("inspect private key")
	}
	if err := validateOpenedRegularFile(validated, opened, maximumPrivateKeyBytes, true); err != nil {
		return nil, err
	}
	if afterOpen != nil {
		afterOpen()
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumPrivateKeyBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return nil, errors.New("read private key")
	}
	if err := validateUnchangedOpenedPath(path, opened, file); err != nil {
		return nil, errors.New("private key changed while reading")
	}
	block, rest := pem.Decode(contents)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("private key must contain one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse PKCS#8 private key")
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("private key must use Ed25519")
	}
	return privateKey, nil
}

func hashRegularFile(path string) (string, error) {
	return hashRegularFileWithHook(path, nil)
}

func hashRegularFileWithHook(path string, afterOpen func()) (string, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() {
		return "", errors.New("evidence must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open evidence file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || validateOpenedRegularFile(validated, opened, 0, false) != nil {
		return "", errors.New("evidence file changed or is invalid")
	}
	if afterOpen != nil {
		afterOpen()
	}
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil || written != opened.Size() {
		return "", errors.New("hash evidence file")
	}
	if err := validateUnchangedOpenedPath(path, opened, file); err != nil {
		return "", errors.New("evidence file changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateOpenedRegularFile(validated, opened os.FileInfo, maximumBytes int64, ownerOnly bool) error {
	if validated == nil || opened == nil || !validated.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || validated.Size() != opened.Size() || !validated.ModTime().Equal(opened.ModTime()) {
		return errors.New("file changed between validation and open")
	}
	if opened.Size() <= 0 || maximumBytes > 0 && opened.Size() > maximumBytes {
		return errors.New("file size is invalid")
	}
	if ownerOnly && opened.Mode().Perm()&0o077 != 0 {
		return errors.New("private key permissions must deny group and other access")
	}
	return nil
}

func validateUnchangedOpenedPath(path string, opened os.FileInfo, file *os.File) error {
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return errors.New("opened file changed")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || opened.Size() != pathAfterRead.Size() || !opened.ModTime().Equal(pathAfterRead.ModTime()) {
		return errors.New("file path changed")
	}
	return nil
}
