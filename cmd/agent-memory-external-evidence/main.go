package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidenceindex"
)

func main() {
	os.Exit(run(os.Args[1:], time.Now, os.Stdout, os.Stderr))
}

func run(args []string, now func() time.Time, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-external-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	catalogPath := flags.String("catalog", "", "Path to the canonical external control catalog")
	indexPath := flags.String("index", "", "Path to the external evidence index")
	artifactRoot := flags.String("artifacts-root", "", "Root containing indexed dossier artifacts")
	trustPath := flags.String("trust", "", "Path to the external evidence approver trust bundle")
	approvalsPath := flags.String("approvals-dir", "", "Directory containing signed external evidence approvals")
	at := flags.String("at", "", "Evaluation time in RFC3339 (defaults to now)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*catalogPath, *indexPath, *artifactRoot, *trustPath, *approvalsPath) {
		fmt.Fprintln(stderr, "catalog, index, artifacts root, trust bundle, and approvals directory are required")
		return 2
	}
	evaluationTime := now().UTC()
	if strings.TrimSpace(*at) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*at))
		if err != nil {
			fmt.Fprintln(stderr, "evaluation time must use RFC3339")
			return 2
		}
		evaluationTime = parsed.UTC()
	}
	verification, err := evidenceindex.VerifyCanonicalFiles(*catalogPath, *indexPath, *artifactRoot, *trustPath, *approvalsPath, evaluationTime)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(verification.Report); err != nil {
		fmt.Fprintln(stderr, "encode external evidence report")
		return 1
	}
	if !verification.Report.Ready {
		return 3
	}
	return 0
}

func anyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
