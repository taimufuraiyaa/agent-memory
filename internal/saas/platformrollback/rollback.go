// Package platformrollback verifies live staging restoration after automatic rollback.
package platformrollback

import (
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"os"
	"strings"
	"time"
)

const ReceiptSchemaV1 = "agent-memory-staging-rollback-verification-receipt-v1"

type Outcome string

const (
	OutcomeRestored      Outcome = "restored"
	OutcomeImageMismatch Outcome = "image_mismatch"
	OutcomeNotReady      Outcome = "not_ready"
	OutcomeUnavailable   Outcome = "unavailable"
)

type LiveDeployment struct {
	Image           string
	Revision        string
	DesiredReplicas int
	ReadyReplicas   int
}

type Snapshot struct {
	KubernetesContext string
	CollectedAt       time.Time
	Deployments       map[DeploymentName]LiveDeployment
}

type DeploymentResult struct {
	Name     DeploymentName `json:"name"`
	Outcome  Outcome        `json:"outcome"`
	Revision string         `json:"revision"`
}

type Receipt struct {
	Schema                     string             `json:"schema"`
	Ready                      bool               `json:"ready"`
	Environment                string             `json:"environment"`
	Namespace                  string             `json:"namespace"`
	KubernetesContext          string             `json:"kubernetes_context"`
	BaselineReleaseID          string             `json:"baseline_release_id"`
	BaselineReceiptSHA256      string             `json:"baseline_receipt_sha256"`
	FailedAttemptReleaseID     string             `json:"failed_attempt_release_id"`
	FailedAttemptReceiptSHA256 string             `json:"failed_attempt_receipt_sha256"`
	CollectedAt                time.Time          `json:"collected_at"`
	Deployments                []DeploymentResult `json:"deployments"`
}

type Assessment struct {
	Ready              bool
	DeploymentCount    int
	RestoredCount      int
	ImageMismatchCount int
	NotReadyCount      int
	UnavailableCount   int
}

func LoadReceipt(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", fmt.Errorf("load rollback verification receipt: %w", err)
	}
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, "", err
	}
	return receipt, digest, nil
}

func Evaluate(pair Pair, snapshot Snapshot) (Receipt, error) {
	if !contextPattern.MatchString(snapshot.KubernetesContext) || snapshot.KubernetesContext != pair.Baseline.KubernetesContext || snapshot.CollectedAt.IsZero() || snapshot.CollectedAt.Before(pair.Attempt.CompletedAt) {
		return Receipt{}, errors.New("rollback live snapshot identity is invalid")
	}
	receipt := Receipt{
		Schema:                     ReceiptSchemaV1,
		Ready:                      true,
		Environment:                stagingEnvironment,
		Namespace:                  stagingNamespace,
		KubernetesContext:          snapshot.KubernetesContext,
		BaselineReleaseID:          pair.Baseline.ReleaseID,
		BaselineReceiptSHA256:      pair.BaselineReceiptSHA256,
		FailedAttemptReleaseID:     pair.Attempt.ReleaseID,
		FailedAttemptReceiptSHA256: pair.AttemptReceiptSHA256,
		CollectedAt:                snapshot.CollectedAt.UTC(),
		Deployments:                make([]DeploymentResult, 0, len(requiredDeployments)),
	}
	for _, name := range requiredDeployments {
		live, exists := snapshot.Deployments[name]
		outcome := evaluateDeployment(name, live, exists, pair.Baseline.Images)
		if outcome != OutcomeRestored {
			receipt.Ready = false
		}
		revision := live.Revision
		if !revisionPattern.MatchString(revision) {
			revision = "unavailable"
		}
		receipt.Deployments = append(receipt.Deployments, DeploymentResult{Name: name, Outcome: outcome, Revision: revision})
	}
	return receipt, nil
}

func evaluateDeployment(name DeploymentName, live LiveDeployment, exists bool, baseline ReleaseImages) Outcome {
	if !exists || !imagePattern.MatchString(live.Image) || !revisionPattern.MatchString(live.Revision) || live.DesiredReplicas < 1 || live.DesiredReplicas > 9999 || live.ReadyReplicas < 0 || live.ReadyReplicas > 9999 {
		return OutcomeUnavailable
	}
	if live.Image != baselineImageFor(name, baseline) {
		return OutcomeImageMismatch
	}
	if live.ReadyReplicas != live.DesiredReplicas {
		return OutcomeNotReady
	}
	return OutcomeRestored
}

func baselineImageFor(name DeploymentName, images ReleaseImages) string {
	switch name {
	case DeploymentAPI:
		return images.API
	case DeploymentWorker:
		return images.Worker
	case DeploymentReconciler:
		return images.Reconciler
	default:
		return ""
	}
}

func Assess(receipt Receipt) Assessment {
	assessment := Assessment{Ready: receipt.Ready, DeploymentCount: len(receipt.Deployments)}
	for _, deployment := range receipt.Deployments {
		switch deployment.Outcome {
		case OutcomeRestored:
			assessment.RestoredCount++
		case OutcomeImageMismatch:
			assessment.ImageMismatchCount++
		case OutcomeNotReady:
			assessment.NotReadyCount++
		case OutcomeUnavailable:
			assessment.UnavailableCount++
		}
	}
	return assessment
}

func validateReceipt(receipt Receipt) error {
	if receipt.Schema != ReceiptSchemaV1 || receipt.Environment != stagingEnvironment || receipt.Namespace != stagingNamespace ||
		!contextPattern.MatchString(receipt.KubernetesContext) || !releaseIDPattern.MatchString(receipt.BaselineReleaseID) ||
		!releaseIDPattern.MatchString(receipt.FailedAttemptReleaseID) || receipt.BaselineReleaseID == receipt.FailedAttemptReleaseID ||
		!regexpDigest(receipt.BaselineReceiptSHA256) || !regexpDigest(receipt.FailedAttemptReceiptSHA256) || receipt.CollectedAt.IsZero() ||
		len(receipt.Deployments) != len(requiredDeployments) {
		return errors.New("rollback verification receipt identity is invalid")
	}
	allRestored := true
	for index, expected := range requiredDeployments {
		deployment := receipt.Deployments[index]
		if deployment.Name != expected {
			return errors.New("rollback verification deployments are not canonical")
		}
		switch deployment.Outcome {
		case OutcomeRestored, OutcomeImageMismatch, OutcomeNotReady:
			if !revisionPattern.MatchString(deployment.Revision) {
				return errors.New("rollback verification deployment revision is invalid")
			}
		case OutcomeUnavailable:
			if deployment.Revision != "unavailable" && !revisionPattern.MatchString(deployment.Revision) {
				return errors.New("rollback verification unavailable revision is invalid")
			}
		default:
			return errors.New("rollback verification deployment outcome is invalid")
		}
		if deployment.Outcome != OutcomeRestored {
			allRestored = false
		}
	}
	if receipt.Ready != allRestored {
		return errors.New("rollback verification readiness contradicts deployments")
	}
	return nil
}

func regexpDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("rollback verification receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("rollback verification receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect rollback verification receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-rollback-verification-*")
}
