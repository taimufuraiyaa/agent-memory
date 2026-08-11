package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformprobe"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	reportSchemaV1       = "agent-memory-staging-edge-telemetry-report-v1"
	maximumResponseBytes = 4 << 10
	observationAttempts  = 5
)

type dependencies struct {
	client *http.Client
	now    func() time.Time
	ids    func() (string, string)
	sleep  func(time.Duration)
}

type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	CheckCount     int    `json:"check_count"`
	PassedCount    int    `json:"passed_count"`
	FailedCount    int    `json:"failed_count"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-platform-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	releasePath := flags.String("release", "", "Path to the passed staging release receipt")
	edgeValue := flags.String("edge-url", "", "HTTPS staging edge base URL")
	internalValue := flags.String("internal-url", "", "Internal API base URL")
	receiptPath := flags.String("receipt", "", "New path for the staging telemetry receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*releasePath, *edgeValue, *internalValue, *receiptPath) {
		fmt.Fprintln(stderr, "release, edge-url, internal-url, and receipt are required")
		return 2
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(*releasePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	edgeBase, err := validateBaseURL(*edgeValue, true)
	if err != nil {
		fmt.Fprintln(stderr, "edge and internal URLs are invalid")
		return 2
	}
	internalBase, err := validateBaseURL(*internalValue, false)
	if err != nil {
		fmt.Fprintln(stderr, "edge and internal URLs are invalid")
		return 2
	}
	deps = completeDependencies(deps)
	client := *deps.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	requestID, traceID := deps.ids()
	startedAt := deps.now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	edgeStatus, echoRequestID, echoTraceID, err := sendEdgeChallenge(ctx, &client, edgeBase, requestID, traceID)
	if err != nil {
		fmt.Fprintln(stderr, "send staging edge challenge")
		return 1
	}
	observation, err := pollObservation(ctx, &client, internalBase, requestID, deps.sleep)
	if err != nil {
		fmt.Fprintln(stderr, "query staging telemetry observation")
		return 1
	}
	completedAt := deps.now().UTC()
	receipt, err := platformprobe.Evaluate(platformprobe.Challenge{
		ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		RequestID: requestID, TraceID: traceID, StartedAt: startedAt, CompletedAt: completedAt,
		EdgeStatus: edgeStatus, EchoRequestID: echoRequestID, EchoTraceID: echoTraceID,
	}, observation)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := platformprobe.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := platformprobe.Assess(receipt)
	result := report{
		Schema: reportSchemaV1, Ready: assessment.Ready, ReceiptWritten: true,
		CheckCount: assessment.CheckCount, PassedCount: assessment.PassedCount, FailedCount: assessment.FailedCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode staging telemetry report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}

func completeDependencies(deps dependencies) dependencies {
	if deps.client == nil {
		deps.client = &http.Client{Timeout: 15 * time.Second}
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.ids == nil {
		deps.ids = func() (string, string) {
			requestID := uuid.NewString()
			traceID := strings.ReplaceAll(uuid.NewString(), "-", "")
			return requestID, traceID
		}
	}
	if deps.sleep == nil {
		deps.sleep = time.Sleep
	}
	return deps
}

func sendEdgeChallenge(ctx context.Context, client *http.Client, base *url.URL, requestID, traceID string) (int, string, string, error) {
	endpoint := base.ResolveReference(&url.URL{Path: "/_edge/health/ready"})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, "", "", errors.New("create edge request")
	}
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("X-Trace-ID", traceID)
	response, err := client.Do(request)
	if err != nil {
		return 0, "", "", errors.New("edge request failed")
	}
	defer response.Body.Close()
	if err := discardBounded(response.Body); err != nil {
		return 0, "", "", err
	}
	return response.StatusCode, strings.TrimSpace(response.Header.Get("X-Request-ID")), strings.TrimSpace(response.Header.Get("X-Trace-ID")), nil
}

func pollObservation(ctx context.Context, client *http.Client, base *url.URL, requestID string, sleep func(time.Duration)) (platformprobe.Observation, error) {
	endpoint := base.ResolveReference(&url.URL{Path: "/internal/evidence/requests/" + requestID})
	for attempt := 0; attempt < observationAttempts; attempt++ {
		observation, found, err := fetchObservation(ctx, client, endpoint)
		if err != nil {
			return platformprobe.Observation{}, err
		}
		if found {
			return observation, nil
		}
		if attempt+1 < observationAttempts {
			sleep(100 * time.Millisecond)
		}
	}
	return platformprobe.Observation{}, nil
}

func fetchObservation(ctx context.Context, client *http.Client, endpoint *url.URL) (platformprobe.Observation, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return platformprobe.Observation{}, false, errors.New("create observation request")
	}
	response, err := client.Do(request)
	if err != nil {
		return platformprobe.Observation{}, false, errors.New("observation request failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		if err := discardBounded(response.Body); err != nil {
			return platformprobe.Observation{}, false, err
		}
		return platformprobe.Observation{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		_ = discardBounded(response.Body)
		return platformprobe.Observation{}, false, errors.New("observation response is invalid")
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil || len(contents) > maximumResponseBytes {
		return platformprobe.Observation{}, false, errors.New("observation response is too large")
	}
	var observation platformprobe.Observation
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observation); err != nil {
		return platformprobe.Observation{}, false, errors.New("observation response JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return platformprobe.Observation{}, false, errors.New("observation response contains trailing JSON")
	}
	return observation, true, nil
}

func discardBounded(reader io.Reader) error {
	contents, err := io.ReadAll(io.LimitReader(reader, maximumResponseBytes+1))
	if err != nil || len(contents) > maximumResponseBytes {
		return errors.New("HTTP response body is too large")
	}
	return nil
}

func validateBaseURL(raw string, edge bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("base URL is invalid")
	}
	if edge {
		if parsed.Scheme != "https" {
			return nil, errors.New("edge URL must use HTTPS")
		}
	} else if parsed.Scheme != "https" && !(parsed.Scheme == "http" && internalHTTPHost(parsed.Hostname())) {
		return nil, errors.New("internal URL is unsafe")
	}
	parsed.Path = ""
	return parsed, nil
}

func internalHTTPHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "agent-memory-api" || strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func anyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
