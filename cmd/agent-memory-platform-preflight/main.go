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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformpreflight"
)

const maximumKubectlOutputBytes = 4 << 10

type kubectlRunner interface {
	Run(context.Context, ...string) (string, error)
}

type commandRunner struct {
	binary string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithRunner(args, stdout, stderr, nil)
}

func runWithRunner(args []string, stdout, stderr io.Writer, injected kubectlRunner) int {
	flags := flag.NewFlagSet("agent-memory-platform-preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the validated self-managed platform inventory")
	environmentValue := flags.String("environment", "", "Target environment: staging or production")
	receiptPath := flags.String("receipt", "", "New path for the content-free preflight receipt")
	kubectlBinary := flags.String("kubectl", "kubectl", "kubectl executable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *environmentValue, *receiptPath, *kubectlBinary) {
		fmt.Fprintln(stderr, "inventory, environment, receipt, and kubectl are required")
		return 2
	}
	environment := platforminventory.Environment(strings.TrimSpace(*environmentValue))
	if environment != platforminventory.Staging && environment != platforminventory.Production {
		fmt.Fprintln(stderr, "environment must be staging or production")
		return 2
	}
	inventory, err := platforminventory.Load(*inventoryPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if inventory.Environment != environment {
		fmt.Fprintln(stderr, "inventory environment does not match target")
		return 2
	}
	runner := injected
	if runner == nil {
		runner = commandRunner{binary: strings.TrimSpace(*kubectlBinary)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snapshot, err := collectSnapshot(ctx, runner, inventory)
	if err != nil {
		fmt.Fprintln(stderr, "collect Kubernetes platform preflight")
		return 1
	}
	receipt, err := platformpreflight.Evaluate(snapshot)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := platformpreflight.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	receipt, err = platformpreflight.Load(*receiptPath, inventory)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := struct {
		Ready          bool `json:"ready"`
		ReceiptWritten bool `json:"receipt_written"`
	}{Ready: receipt.Ready, ReceiptWritten: true}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode platform preflight result")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}

func collectSnapshot(ctx context.Context, runner kubectlRunner, inventory platforminventory.Inventory) (platformpreflight.Snapshot, error) {
	kubernetesContext, err := runner.Run(ctx, "config", "current-context")
	if err != nil || strings.TrimSpace(kubernetesContext) == "" {
		return platformpreflight.Snapshot{}, fmt.Errorf("resolve Kubernetes context")
	}
	namespace := "agent-memory-" + string(inventory.Environment)
	snapshot := platformpreflight.Snapshot{
		Environment:            inventory.Environment,
		KubernetesContext:      strings.TrimSpace(kubernetesContext),
		Namespace:              namespace,
		InventoryID:            inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256,
		CollectedAt:            time.Now().UTC(),
		ServiceAccounts:        map[string]bool{},
		Secrets:                map[string]bool{},
		NetworkPolicies:        map[string]bool{},
		ServiceTypes:           map[string]string{},
		Workloads:              map[string]platformpreflight.Workload{},
	}
	snapshot.NamespaceExists = resourceExists(ctx, runner, "", "namespace/"+namespace)
	for _, name := range []string{"agent-memory-api", "agent-memory-worker", "agent-memory-reconciler", "agent-memory-migration"} {
		snapshot.ServiceAccounts[name] = resourceExists(ctx, runner, namespace, "serviceaccount/"+name)
	}
	for _, name := range []string{"agent-memory-api-secrets", "agent-memory-worker-secrets", "agent-memory-reconciler-secrets", "agent-memory-migration-secrets"} {
		snapshot.Secrets[name] = resourceExists(ctx, runner, namespace, "secret/"+name)
	}
	for _, name := range []string{"default-deny", "allow-api-edge-ingress", "allow-dns-and-managed-services", "allow-observability-scrape"} {
		snapshot.NetworkPolicies[name] = resourceExists(ctx, runner, namespace, "networkpolicy/"+name)
	}
	if value, ok := query(ctx, runner, namespace, "service/agent-memory-api", `{.spec.type}`); ok {
		snapshot.ServiceTypes["agent-memory-api"] = value
	}
	for _, name := range []string{"agent-memory-api", "agent-memory-worker", "agent-memory-reconciler"} {
		workload := platformpreflight.Workload{}
		workload.ServiceAccount, _ = query(ctx, runner, namespace, "deployment/"+name, `{.spec.template.spec.serviceAccountName}`)
		workload.Image, _ = query(ctx, runner, namespace, "deployment/"+name, `{.spec.template.spec.containers[0].image}`)
		workload.DesiredReplicas = queryInt(ctx, runner, namespace, "deployment/"+name, `{.spec.replicas}`)
		workload.ReadyReplicas = queryInt(ctx, runner, namespace, "deployment/"+name, `{.status.readyReplicas}`)
		snapshot.Workloads[name] = workload
	}
	return snapshot, nil
}

func resourceExists(ctx context.Context, runner kubectlRunner, namespace, resource string) bool {
	args := []string{}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "get", resource, "-o", "name")
	value, err := runner.Run(ctx, args...)
	return err == nil && strings.TrimSpace(value) != ""
}

func query(ctx context.Context, runner kubectlRunner, namespace, resource, expression string) (string, bool) {
	value, err := runner.Run(ctx, "-n", namespace, "get", resource, "-o", "jsonpath="+expression)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func queryInt(ctx context.Context, runner kubectlRunner, namespace, resource, expression string) int {
	value, ok := query(ctx, runner, namespace, resource, expression)
	if !ok {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 9999 {
		return 0
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
