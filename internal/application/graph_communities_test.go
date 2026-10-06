package application

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphCommunitiesImportHierarchyAndVersionedReports(t *testing.T) {
	t.Parallel()
	request := graphCommunityImportRequest()
	request.Candidates = []core.GraphCommunityCandidate{
		{ExternalID: "root", EntityIDs: []string{"entity-a", "entity-b"}, EdgeIDs: []string{"edge-a"}, SourceCount: 2, UnresolvedCount: 0, Report: graphReportCandidate("Root", 0.9)},
		{ExternalID: "child", ParentExternalID: "root", EntityIDs: []string{"entity-a"}, SourceCount: 1, UnresolvedCount: 1, Report: graphReportCandidate("Child", 0.6)},
	}

	result, err := NewGraphCommunityImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Communities) != 2 || result.Communities[0].Level != 0 || result.Communities[1].Level != 1 {
		t.Fatalf("hierarchy levels not preserved: %#v", result.Communities)
	}
	if result.Communities[1].ParentID != result.Communities[0].ID || result.Reports[1].UnresolvedCount != 1 {
		t.Fatalf("hierarchy or coverage lost: %#v %#v", result.Communities, result.Reports)
	}
	if result.Reports[0].ModelFingerprint != request.ModelFingerprint || result.Reports[0].PromptFingerprint != request.PromptFingerprint {
		t.Fatalf("report generation identity lost: %#v", result.Reports[0])
	}
}

func TestGraphCommunityImportRejectsInvalidHierarchy(t *testing.T) {
	t.Parallel()
	request := graphCommunityImportRequest()
	request.Candidates = []core.GraphCommunityCandidate{
		{ExternalID: "a", ParentExternalID: "b", EntityIDs: []string{"entity-a"}, Report: graphReportCandidate("A", 0.5)},
		{ExternalID: "b", ParentExternalID: "a", EntityIDs: []string{"entity-b"}, Report: graphReportCandidate("B", 0.5)},
	}
	if _, err := NewGraphCommunityImporter().Import(request); err == nil {
		t.Fatal("cyclic hierarchy accepted")
	}
}

func TestGraphReportMarksStaleWhenMembershipEvidenceModelPromptOrReviewChanges(t *testing.T) {
	t.Parallel()
	request := graphCommunityImportRequest()
	request.Candidates = []core.GraphCommunityCandidate{{ExternalID: "root", EntityIDs: []string{"entity-a"}, SourceCount: 1, Report: graphReportCandidate("Root", 0.9)}}
	first, err := NewGraphCommunityImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviousReports = map[string]core.GraphReport{first.Communities[0].ID: first.Reports[0]}
	request.Candidates[0].PriorCommunityID = first.Communities[0].ID
	request.Candidates[0].EntityIDs = []string{"entity-a", "entity-b"}
	second, err := NewGraphCommunityImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reports[0].Stale {
		t.Fatal("changed membership did not mark prior report lineage stale")
	}
}

func graphCommunityImportRequest() GraphCommunityImportRequest {
	return GraphCommunityImportRequest{
		Scope: core.GraphScope{WorkspaceID: "workspace-a"}, RevisionID: "revision-2",
		ConfigurationID: "configuration-1", EntityIDs: map[string]struct{}{"entity-a": {}, "entity-b": {}}, EdgeIDs: map[string]struct{}{"edge-a": {}},
		ModelRoute: "graph-index-primary", ModelFingerprint: "sha256:model", PromptFingerprint: "sha256:prompt", ReviewVersion: 2,
		Now: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC),
	}
}

func graphReportCandidate(title string, rank float64) core.GraphReportCandidate {
	return core.GraphReportCandidate{ExternalID: "report-" + title, Title: title, Summary: title + " summary", Findings: []string{title + " finding"}, Rank: rank, AdmissionState: core.GraphReportAdmitted}
}
