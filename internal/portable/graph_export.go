package portable

import (
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphExportSelection struct {
	IncludeGraphMetadata bool
}

type GraphMetadata struct {
	Entities                []core.GraphEntity
	EntityVersions          []core.GraphEntityVersion
	Edges                   []core.GraphEdge
	EdgeVersions            []core.GraphEdgeVersion
	Communities             []core.GraphCommunity
	CommunityMembers        map[string][]contracts.GraphCommunityMember
	Reports                 []core.GraphReport
	Reviews                 []core.GraphReview
	NativeArtifactLocations []string
}

type GraphExport struct {
	SchemaVersion    string                                      `json:"schema_version"`
	Entities         []core.GraphEntity                          `json:"entities,omitempty"`
	EntityVersions   []core.GraphEntityVersion                   `json:"entity_versions,omitempty"`
	Edges            []core.GraphEdge                            `json:"edges,omitempty"`
	EdgeVersions     []core.GraphEdgeVersion                     `json:"edge_versions,omitempty"`
	Communities      []core.GraphCommunity                       `json:"communities,omitempty"`
	CommunityMembers map[string][]contracts.GraphCommunityMember `json:"community_members,omitempty"`
	Reports          []core.GraphReport                          `json:"reports,omitempty"`
	Reviews          []core.GraphReview                          `json:"reviews,omitempty"`
	NativeArtifacts  []string                                    `json:"native_artifacts,omitempty"`
}

func BuildGraphExport(selection GraphExportSelection, metadata GraphMetadata) *GraphExport {
	if !selection.IncludeGraphMetadata {
		return nil
	}
	return &GraphExport{
		SchemaVersion: "agent-memory-graph-metadata/v1",
		Entities:      append([]core.GraphEntity(nil), metadata.Entities...), EntityVersions: append([]core.GraphEntityVersion(nil), metadata.EntityVersions...),
		Edges: append([]core.GraphEdge(nil), metadata.Edges...), EdgeVersions: append([]core.GraphEdgeVersion(nil), metadata.EdgeVersions...),
		Communities: append([]core.GraphCommunity(nil), metadata.Communities...), CommunityMembers: metadata.CommunityMembers,
		Reports: append([]core.GraphReport(nil), metadata.Reports...), Reviews: append([]core.GraphReview(nil), metadata.Reviews...),
		NativeArtifacts: nil,
	}
}
