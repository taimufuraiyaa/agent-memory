package platformprobe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvaluateAcceptsExactEdgeAPIAndTelemetryCorrelation(t *testing.T) {
	receipt, err := Evaluate(validChallenge(), validObservation())
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if !assessment.Ready || assessment.PassedCount != 3 || assessment.CheckCount != 3 {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
	for _, check := range receipt.Checks {
		if check.Outcome != OutcomePassed {
			t.Fatalf("check %s outcome=%s, want passed", check.ID, check.Outcome)
		}
	}
}

func TestEvaluateEmitsFixedUnreadyChecks(t *testing.T) {
	tests := map[string]struct {
		mutateChallenge   func(*Challenge)
		mutateObservation func(*Observation)
		check             CheckID
	}{
		"edge status":       {mutateChallenge: func(value *Challenge) { value.EdgeStatus = 503 }, check: CheckEdgeResponse},
		"request echo":      {mutateChallenge: func(value *Challenge) { value.EchoRequestID = "other-request" }, check: CheckAPICorrelation},
		"trace echo":        {mutateChallenge: func(value *Challenge) { value.EchoTraceID = strings.Repeat("b", 32) }, check: CheckAPICorrelation},
		"missing telemetry": {mutateObservation: func(value *Observation) { *value = Observation{} }, check: CheckTelemetryObservation},
		"wrong operation":   {mutateObservation: func(value *Observation) { value.Operation = "POST:/v1/signup" }, check: CheckTelemetryObservation},
		"late observation":  {mutateObservation: func(value *Observation) { value.ObservedAt = time.Date(2026, 8, 10, 1, 3, 0, 0, time.UTC) }, check: CheckTelemetryObservation},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			challenge := validChallenge()
			observation := validObservation()
			if test.mutateChallenge != nil {
				test.mutateChallenge(&challenge)
			}
			if test.mutateObservation != nil {
				test.mutateObservation(&observation)
			}
			receipt, err := Evaluate(challenge, observation)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Ready || outcomeFor(receipt, test.check) != OutcomeFailed {
				t.Fatalf("unexpected receipt: %+v", receipt)
			}
		})
	}
}

func TestEvaluateRejectsUnsafeIdentityAndWindow(t *testing.T) {
	for name, mutate := range map[string]func(*Challenge){
		"release id":      func(value *Challenge) { value.ReleaseID = "release with spaces" },
		"release digest":  func(value *Challenge) { value.ReleaseReceiptSHA256 = "not-a-digest" },
		"request id":      func(value *Challenge) { value.RequestID = "customer@example.com" },
		"trace id":        func(value *Challenge) { value.TraceID = "trace-id" },
		"reversed window": func(value *Challenge) { value.CompletedAt = value.StartedAt.Add(-time.Second) },
		"long window":     func(value *Challenge) { value.CompletedAt = value.StartedAt.Add(3 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			challenge := validChallenge()
			mutate(&challenge)
			if _, err := Evaluate(challenge, validObservation()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishIsPrivateCreateOnlyAndRejectsSymlink(t *testing.T) {
	receipt, err := Evaluate(validChallenge(), validObservation())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "probe.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o, want 600", info.Mode().Perm())
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, receipt); err == nil {
		t.Fatal("symlink destination was accepted")
	}
}

func validChallenge() Challenge {
	return Challenge{
		ReleaseID:            "release-20260810",
		ReleaseReceiptSHA256: strings.Repeat("a", 64),
		RequestID:            "8c73a4f1-027e-4ea5-95c8-75eb9a847ac4",
		TraceID:              "0123456789abcdef0123456789abcdef",
		StartedAt:            time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
		CompletedAt:          time.Date(2026, 8, 10, 1, 1, 0, 0, time.UTC),
		EdgeStatus:           200,
		EchoRequestID:        "8c73a4f1-027e-4ea5-95c8-75eb9a847ac4",
		EchoTraceID:          "0123456789abcdef0123456789abcdef",
	}
}

func validObservation() Observation {
	return Observation{
		RequestID: "8c73a4f1-027e-4ea5-95c8-75eb9a847ac4", TraceID: "0123456789abcdef0123456789abcdef",
		Service: "api", Operation: "GET:/health/ready", Status: 200, Outcome: "success",
		ObservedAt: time.Date(2026, 8, 10, 1, 0, 30, 0, time.UTC),
	}
}

func outcomeFor(receipt Receipt, id CheckID) Outcome {
	for _, check := range receipt.Checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}
