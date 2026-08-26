package core

import "testing"

func TestGraphReportCannotResolveAsDirectEvidenceOrQuotation(t *testing.T) {
	t.Parallel()
	report := GraphReport{ID: "report-1", Summary: "Derived synthesis"}
	if report.CanGroundClaim() || report.CanBeQuotedAsSource() {
		t.Fatal("community report was treated as canonical evidence")
	}
}

func TestGraphReportStalenessTracksMeaningfulInputs(t *testing.T) {
	t.Parallel()
	baseline := GraphReportFreshness{
		MembershipFingerprint: "sha256:members", EvidenceFingerprint: "sha256:evidence",
		ModelFingerprint: "sha256:model", PromptFingerprint: "sha256:prompt", ReviewVersion: 3,
	}
	if baseline.StaleAgainst(baseline) {
		t.Fatal("identical report inputs marked stale")
	}
	changed := baseline
	changed.PromptFingerprint = "sha256:new-prompt"
	if !baseline.StaleAgainst(changed) {
		t.Fatal("changed prompt did not stale report")
	}
}
