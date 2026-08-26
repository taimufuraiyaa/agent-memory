package modelgateway

import (
	"strings"
	"testing"
)

func TestGraphGlobalSynthesisUsesReportsOnlyAsGuideAndCanonicalEvidenceAsGrounding(t *testing.T) {
	request, err := BuildGraphSynthesisRequest(GraphSynthesisInput{
		TenantID: "tenant-a", Provider: "private-model", Model: "model-v1", Query: "What patterns recur?",
		Communities:    []GraphSynthesisCommunity{{ID: "community-1", Title: "Payments", Summary: "Retry failures recur", CoveredSources: 3, UnresolvedEvidence: 1}},
		Evidence:       []Evidence{{SourceID: "memory-1", PassageID: "memory-1", Text: "Retry handler timed out."}},
		MaxPromptBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Evidence) != 1 || request.Evidence[0].SourceID != "memory-1" {
		t.Fatalf("canonical evidence changed: %#v", request.Evidence)
	}
	if strings.Contains(request.Evidence[0].Text, "Retry failures recur") || !strings.Contains(request.Prompt, "navigation context") || !strings.Contains(request.Prompt, "canonical evidence") {
		t.Fatalf("report/evidence roles are not explicit: %#v", request)
	}
}

func TestGraphGlobalSynthesisRejectsReportOnlyAndOversizedInput(t *testing.T) {
	base := GraphSynthesisInput{TenantID: "tenant-a", Provider: "private-model", Model: "model-v1", Query: "patterns", Communities: []GraphSynthesisCommunity{{ID: "c1", Summary: "generated report"}}, MaxPromptBytes: 256}
	if _, err := BuildGraphSynthesisRequest(base); err == nil {
		t.Fatal("report-only synthesis was accepted")
	}
	base.Evidence = []Evidence{{SourceID: "m1", PassageID: "m1", Text: strings.Repeat("x", 1024)}}
	if _, err := BuildGraphSynthesisRequest(base); err == nil {
		t.Fatal("oversized graph synthesis was accepted")
	}
}
