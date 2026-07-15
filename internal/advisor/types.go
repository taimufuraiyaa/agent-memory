package advisor

import (
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type DimensionKey string

const (
	DimensionQuality    DimensionKey = "quality"
	DimensionEfficiency DimensionKey = "efficiency"
	DimensionHygiene    DimensionKey = "hygiene"
	DimensionCoverage   DimensionKey = "coverage"
	DimensionTrust      DimensionKey = "trust"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarn     Severity = "warn"
	SeverityInfo     Severity = "info"
)

type Dimension struct {
	Key       DimensionKey `json:"key"`
	Label     string       `json:"label"`
	Score     int          `json:"score"`
	Weight    float64      `json:"weight"`
	Available bool         `json:"available"`
	Detail    string       `json:"detail"`
}

type Recommendation struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Metric   string   `json:"metric,omitempty"`
}

type Evidence struct {
	MemoryCount            int `json:"memory_count"`
	ActiveMemoryCount      int `json:"active_memory_count"`
	ScoredRequestCount     int `json:"scored_request_count"`
	UsefulRatioSampleCount int `json:"useful_ratio_sample_count"`
	RecallMetricRecords    int `json:"recall_metric_records"`
}

type Report struct {
	Workspace       string           `json:"workspace"`
	Score           int              `json:"score"`
	Grade           string           `json:"grade"`
	Neutral         bool             `json:"neutral"`
	Dimensions      []Dimension      `json:"dimensions"`
	Recommendations []Recommendation `json:"recommendations"`
	Evidence        Evidence         `json:"evidence"`
}

type Snapshot struct {
	Workspace               string
	Memories                []core.MemoryEntry
	Requests                []core.RetrievalRequestLog
	TokenMetricsByOperation []sqlite.TokenMetricOperationTotals
}
