package core

type GraphReportAdmissionState string

const (
	GraphReportAdmitted    GraphReportAdmissionState = "admitted"
	GraphReportQuarantined GraphReportAdmissionState = "quarantined"
	GraphReportRejected    GraphReportAdmissionState = "rejected"
)

type GraphReportCandidate struct {
	ExternalID     string
	Title          string
	Summary        string
	Findings       []string
	Rank           float64
	AdmissionState GraphReportAdmissionState
}

type GraphCommunityCandidate struct {
	ExternalID           string
	PriorCommunityID     string
	ParentExternalID     string
	EntityIDs            []string
	EdgeIDs              []string
	EvidenceFingerprints []string
	SourceCount          int
	UnresolvedCount      int
	Report               GraphReportCandidate
}

type GraphReportFreshness struct {
	MembershipFingerprint string
	EvidenceFingerprint   string
	ModelFingerprint      string
	PromptFingerprint     string
	ReviewVersion         int64
}

func (f GraphReportFreshness) StaleAgainst(current GraphReportFreshness) bool {
	return f.MembershipFingerprint != current.MembershipFingerprint ||
		f.EvidenceFingerprint != current.EvidenceFingerprint ||
		f.ModelFingerprint != current.ModelFingerprint ||
		f.PromptFingerprint != current.PromptFingerprint ||
		f.ReviewVersion != current.ReviewVersion
}

func (r GraphReport) Freshness() GraphReportFreshness {
	return GraphReportFreshness{
		MembershipFingerprint: r.MembershipFingerprint, EvidenceFingerprint: r.EvidenceFingerprint,
		ModelFingerprint: r.ModelFingerprint, PromptFingerprint: r.PromptFingerprint, ReviewVersion: r.ReviewVersion,
	}
}

func (GraphReport) CanGroundClaim() bool      { return false }
func (GraphReport) CanBeQuotedAsSource() bool { return false }
