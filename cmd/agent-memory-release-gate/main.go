package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-release-gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	gate := flags.String("gate", "ga", "Release gate: private_beta|public_beta|ga")
	windowDays := flags.Int("window-days", 28, "Minimum shared evidence-window duration in days")
	approverKeys := flags.String("approver-keys", strings.TrimSpace(getenv("AGENT_MEMORY_RELEASE_APPROVER_KEYS")), "Path to the out-of-band trusted approver key bundle")
	approvalsDir := flags.String("approvals-dir", strings.TrimSpace(getenv("AGENT_MEMORY_RELEASE_APPROVALS_DIR")), "Directory containing signed approval JSON artifacts for this gate")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if _, ok := readiness.GateMetrics[*gate]; !ok || *windowDays < 1 || strings.TrimSpace(*approverKeys) == "" || strings.TrimSpace(*approvalsDir) == "" {
		fmt.Fprintln(stderr, "gate, positive window, approver keys, and approvals directory are required")
		return 2
	}
	bundle, err := readiness.LoadTrustBundle(*approverKeys)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	approvals, err := readiness.LoadApprovals(*approvalsDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	url := strings.TrimSpace(getenv("AGENT_MEMORY_POSTGRES_URL"))
	if url == "" {
		fmt.Fprintln(stderr, "AGENT_MEMORY_POSTGRES_URL is required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer pool.Close()
	report, err := readiness.NewService(pool, nil).EvaluateRelease(ctx, *gate, time.Duration(*windowDays)*24*time.Hour, bundle, approvals, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		fmt.Fprintln(stderr, "encode release report")
		return 1
	}
	if !report.Ready {
		return 3
	}
	return 0
}
