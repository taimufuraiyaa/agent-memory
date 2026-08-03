// Package ingestion converts source assets into normalized, source-addressable documents.
package ingestion

import "github.com/taimufuraiyaa/agent-memory/internal/library"

type SourceSpan struct {
	SourceStart     int `json:"source_start"`
	SourceEnd       int `json:"source_end"`
	NormalizedStart int `json:"normalized_start"`
	NormalizedEnd   int `json:"normalized_end"`
}

type ExtractedSection struct {
	NodeID         string     `json:"node_id"`
	SourceText     string     `json:"source_text"`
	NormalizedText string     `json:"normalized_text"`
	Span           SourceSpan `json:"span"`
}

type ExtractedDocument struct {
	EditionID            string                   `json:"edition_id"`
	ParserVersion        string                   `json:"parser_version"`
	NormalizationVersion string                   `json:"normalization_version"`
	NormalizedText       string                   `json:"normalized_text"`
	Nodes                []library.StructuralNode `json:"nodes"`
	Sections             []ExtractedSection       `json:"sections"`
}
