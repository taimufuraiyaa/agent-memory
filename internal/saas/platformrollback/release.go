package platformrollback

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	releaseSchemaV1       = "agent-memory-kubernetes-release-receipt-v1"
	maximumReleaseBytes   = 64 << 10
	stagingNamespace      = "agent-memory-staging"
	stagingEnvironment    = "staging"
	productionEnvironment = "production"
	releaseOutcomePassed  = "passed"
	releaseOutcomeFailed  = "failed"
)

var (
	releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	contextPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,252}$`)
	imagePattern     = regexp.MustCompile(`^[^\s@]+(?:/[^\s@]+)*@sha256:[a-f0-9]{64}$`)
	revisionPattern  = regexp.MustCompile(`^[1-9][0-9]*$`)
)

type DeploymentName string

const (
	DeploymentAPI        DeploymentName = "agent-memory-api"
	DeploymentWorker     DeploymentName = "agent-memory-worker"
	DeploymentReconciler DeploymentName = "agent-memory-reconciler"
)

var requiredDeployments = []DeploymentName{DeploymentAPI, DeploymentWorker, DeploymentReconciler}

type ReleaseReceipt struct {
	Schema            string              `json:"schema"`
	Environment       string              `json:"environment"`
	Namespace         string              `json:"namespace"`
	KubernetesContext string              `json:"kubernetes_context"`
	ReleaseID         string              `json:"release_id"`
	StartedAt         time.Time           `json:"started_at"`
	CompletedAt       time.Time           `json:"completed_at"`
	Outcome           string              `json:"outcome"`
	Images            ReleaseImages       `json:"images"`
	Migration         ReleaseStage        `json:"migration"`
	Rollouts          ReleaseStage        `json:"rollouts"`
	Deployments       []ReleaseDeployment `json:"deployments"`
	Rollback          ReleaseRollback     `json:"rollback"`
}

type ReleaseImages struct {
	API        string `json:"api"`
	Worker     string `json:"worker"`
	Reconciler string `json:"reconciler"`
	Migrate    string `json:"migrate"`
}

type ReleaseStage struct {
	Outcome string `json:"outcome"`
}

type ReleaseDeployment struct {
	Name     DeploymentName `json:"name"`
	Revision string         `json:"revision"`
}

type ReleaseRollback struct {
	Attempted bool `json:"attempted"`
	Succeeded bool `json:"succeeded"`
}

type Pair struct {
	Baseline              ReleaseReceipt
	Attempt               ReleaseReceipt
	BaselineReceiptSHA256 string
	AttemptReceiptSHA256  string
}

func LoadPair(baselinePath, attemptPath string) (Pair, error) {
	baseline, baselineDigest, err := LoadPassedRelease(baselinePath)
	if err != nil {
		return Pair{}, fmt.Errorf("load baseline release receipt: %w", err)
	}
	attempt, attemptDigest, err := loadRelease(attemptPath)
	if err != nil {
		return Pair{}, fmt.Errorf("load failed release receipt: %w", err)
	}
	pair := Pair{Baseline: baseline, Attempt: attempt, BaselineReceiptSHA256: baselineDigest, AttemptReceiptSHA256: attemptDigest}
	if err := validatePair(pair); err != nil {
		return Pair{}, err
	}
	return pair, nil
}

func LoadPassedRelease(path string) (ReleaseReceipt, string, error) {
	return LoadPassedReleaseForEnvironment(path, stagingEnvironment)
}

// LoadPassedReleaseForEnvironment loads a passed release for one exact
// supported target. Rollback-pair verification remains staging-only.
func LoadPassedReleaseForEnvironment(path, environment string) (ReleaseReceipt, string, error) {
	if environment != stagingEnvironment && environment != productionEnvironment {
		return ReleaseReceipt{}, "", errors.New("release environment is unsupported")
	}
	receipt, digest, err := loadReleaseForEnvironment(path, environment)
	if err != nil {
		return ReleaseReceipt{}, "", err
	}
	if receipt.Outcome != releaseOutcomePassed || receipt.Migration.Outcome != "complete" || receipt.Rollouts.Outcome != "healthy" || receipt.Rollback.Attempted || receipt.Rollback.Succeeded {
		return ReleaseReceipt{}, "", fmt.Errorf("%s release receipt is not passed", environment)
	}
	return receipt, digest, nil
}

func loadRelease(path string) (ReleaseReceipt, string, error) {
	return loadReleaseForEnvironment(path, stagingEnvironment)
}

func loadReleaseForEnvironment(path, environment string) (ReleaseReceipt, string, error) {
	var receipt ReleaseReceipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return ReleaseReceipt{}, "", err
	}
	if err := validateReleaseShape(receipt, environment); err != nil {
		return ReleaseReceipt{}, "", err
	}
	return receipt, digest, nil
}

func validateReleaseShape(receipt ReleaseReceipt, environment string) error {
	if receipt.Schema != releaseSchemaV1 || receipt.Environment != environment || receipt.Namespace != "agent-memory-"+environment || !contextPattern.MatchString(receipt.KubernetesContext) || !releaseIDPattern.MatchString(receipt.ReleaseID) {
		return errors.New("release receipt identity is invalid")
	}
	if receipt.StartedAt.IsZero() || receipt.CompletedAt.IsZero() || receipt.CompletedAt.Before(receipt.StartedAt) {
		return errors.New("release receipt window is invalid")
	}
	if !validImages(receipt.Images) || !validReleaseDeployments(receipt.Deployments) {
		return errors.New("release receipt deployment evidence is invalid")
	}
	return nil
}

func validatePair(pair Pair) error {
	baseline, attempt := pair.Baseline, pair.Attempt
	if attempt.Outcome != releaseOutcomeFailed || attempt.Migration.Outcome != "complete" || attempt.Rollouts.Outcome != "failed" || !attempt.Rollback.Attempted || !attempt.Rollback.Succeeded {
		return errors.New("rollback attempt release is invalid")
	}
	if attempt.KubernetesContext != baseline.KubernetesContext || attempt.Namespace != baseline.Namespace || attempt.StartedAt.Before(baseline.CompletedAt) {
		return errors.New("rollback release pair is not ordered in one staging target")
	}
	if baseline.Images.API == attempt.Images.API && baseline.Images.Worker == attempt.Images.Worker && baseline.Images.Reconciler == attempt.Images.Reconciler {
		return errors.New("rollback attempt did not change a workload image")
	}
	return nil
}

func validImages(images ReleaseImages) bool {
	return imagePattern.MatchString(images.API) && imagePattern.MatchString(images.Worker) && imagePattern.MatchString(images.Reconciler) && imagePattern.MatchString(images.Migrate)
}

func validReleaseDeployments(deployments []ReleaseDeployment) bool {
	if len(deployments) != len(requiredDeployments) {
		return false
	}
	seen := make(map[DeploymentName]bool, len(deployments))
	for _, deployment := range deployments {
		if !revisionPattern.MatchString(deployment.Revision) || seen[deployment.Name] {
			return false
		}
		seen[deployment.Name] = true
	}
	for _, deployment := range requiredDeployments {
		if !seen[deployment] {
			return false
		}
	}
	return true
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("release receipt path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumReleaseBytes {
		return "", errors.New("release receipt must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open release receipt")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("release receipt changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumReleaseBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumReleaseBytes {
		return "", errors.New("read release receipt")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("release receipt JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("release receipt contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("release receipt changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("release receipt changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
