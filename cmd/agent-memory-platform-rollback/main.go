package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	reportSchemaV1            = "agent-memory-staging-rollback-verification-report-v1"
	maximumKubectlOutputBytes = 4 << 10
)

type kubectlRunner interface {
	Run(context.Context, ...string) (string, error)
}

type commandRunner struct {
	binary string
}

type report struct {
	Schema             string `json:"schema"`
	Ready              bool   `json:"ready"`
	ReceiptWritten     bool   `json:"receipt_written"`
	DeploymentCount    int    `json:"deployment_count"`
	RestoredCount      int    `json:"restored_count"`
	ImageMismatchCount int    `json:"image_mismatch_count"`
	NotReadyCount      int    `json:"not_ready_count"`
	UnavailableCount   int    `json:"unavailable_count"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithRunner(args, stdout, stderr, nil)
}

func runWithRunner(args []string, stdout, stderr io.Writer, injected kubectlRunner) int {
	flags := flag.NewFlagSet("agent-memory-platform-rollback", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baselinePath := flags.String("baseline", "", "Path to the passed staging release receipt")
	attemptPath := flags.String("failed-attempt", "", "Path to the failed rollback-succeeded staging release receipt")
	receiptPath := flags.String("receipt", "", "New path for the rollback verification receipt")
	kubectlBinary := flags.String("kubectl", "kubectl", "kubectl executable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*baselinePath, *attemptPath, *receiptPath, *kubectlBinary) {
		fmt.Fprintln(stderr, "baseline, failed-attempt, receipt, and kubectl are required")
		return 2
	}
	pair, err := platformrollback.LoadPair(*baselinePath, *attemptPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	runner := injected
	if runner == nil {
		runner = commandRunner{binary: strings.TrimSpace(*kubectlBinary)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snapshot, err := collectSnapshot(ctx, runner)
	if err != nil {
		fmt.Fprintln(stderr, "collect Kubernetes rollback state")
		return 1
	}
	receipt, err := platformrollback.Evaluate(pair, snapshot)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := platformrollback.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := platformrollback.Assess(receipt)
	result := report{
		Schema:             reportSchemaV1,
		Ready:              assessment.Ready,
		ReceiptWritten:     true,
		DeploymentCount:    assessment.DeploymentCount,
		RestoredCount:      assessment.RestoredCount,
		ImageMismatchCount: assessment.ImageMismatchCount,
		NotReadyCount:      assessment.NotReadyCount,
		UnavailableCount:   assessment.UnavailableCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode rollback verification report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}

func collectSnapshot(ctx context.Context, runner kubectlRunner) (platformrollback.Snapshot, error) {
	kubernetesContext, err := runner.Run(ctx, "config", "current-context")
	if err != nil || strings.TrimSpace(kubernetesContext) == "" {
		return platformrollback.Snapshot{}, errors.New("resolve Kubernetes context")
	}
	snapshot := platformrollback.Snapshot{
		KubernetesContext: strings.TrimSpace(kubernetesContext),
		CollectedAt:       time.Now().UTC(),
		Deployments:       make(map[platformrollback.DeploymentName]platformrollback.LiveDeployment, 3),
	}
	for _, name := range []platformrollback.DeploymentName{
		platformrollback.DeploymentAPI,
		platformrollback.DeploymentWorker,
		platformrollback.DeploymentReconciler,
	} {
		resource := "deployment/" + string(name)
		container := strings.TrimPrefix(string(name), "agent-memory-")
		imageExpression := `{.spec.template.spec.containers[?(@.name=="` + container + `")].image}`
		image, _ := query(ctx, runner, resource, imageExpression)
		revision, _ := query(ctx, runner, resource, `{.metadata.annotations.deployment\.kubernetes\.io/revision}`)
		desired := queryInt(ctx, runner, resource, `{.spec.replicas}`, -1)
		ready := queryInt(ctx, runner, resource, `{.status.readyReplicas}`, 0)
		snapshot.Deployments[name] = platformrollback.LiveDeployment{
			Image: image, Revision: revision, DesiredReplicas: desired, ReadyReplicas: ready,
		}
	}
	return snapshot, nil
}

func query(ctx context.Context, runner kubectlRunner, resource, expression string) (string, bool) {
	value, err := runner.Run(ctx, "-n", "agent-memory-staging", "get", resource, "-o", "jsonpath="+expression)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func queryInt(ctx context.Context, runner kubectlRunner, resource, expression string, emptyValue int) int {
	value, err := runner.Run(ctx, "-n", "agent-memory-staging", "get", resource, "-o", "jsonpath="+expression)
	if err != nil {
		return -1
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return emptyValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 9999 {
		return -1
	}
	return parsed
}

func (runner commandRunner) Run(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, runner.binary, args...)
	command.Stderr = io.Discard
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", errors.New("prepare kubectl output")
	}
	if err := command.Start(); err != nil {
		return "", errors.New("start kubectl")
	}
	contents, readErr := io.ReadAll(io.LimitReader(stdout, maximumKubectlOutputBytes+1))
	tooLarge := len(contents) > maximumKubectlOutputBytes
	if tooLarge {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := command.Wait()
	if readErr != nil || tooLarge || waitErr != nil {
		return "", errors.New("kubectl query failed")
	}
	return string(contents), nil
}

func anyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
