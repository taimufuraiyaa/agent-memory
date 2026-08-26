package contracts

import (
	"context"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphQueryNode struct {
	Entity        core.GraphEntity        `json:"entity"`
	Version       core.GraphEntityVersion `json:"version"`
	Evidence      []core.GraphEvidence    `json:"evidence"`
	RecordVersion int64                   `json:"record_version"`
}

type GraphQueryEdge struct {
	Edge          core.GraphEdge        `json:"edge"`
	Version       core.GraphEdgeVersion `json:"version"`
	Evidence      []core.GraphEvidence  `json:"evidence"`
	RecordVersion int64                 `json:"record_version"`
}

type GraphQueryCommunity struct {
	Community core.GraphCommunity    `json:"community"`
	Members   []GraphCommunityMember `json:"members"`
	Report    core.GraphReport       `json:"report"`
	Evidence  []core.GraphEvidence   `json:"evidence"`
}

type GraphQuerySnapshot struct {
	Scope           core.GraphScope       `json:"scope"`
	ConfigurationID string                `json:"configuration_id"`
	RevisionID      string                `json:"revision_id"`
	CacheIdentity   string                `json:"cache_identity"`
	Fresh           bool                  `json:"fresh"`
	Nodes           []GraphQueryNode      `json:"nodes"`
	Edges           []GraphQueryEdge      `json:"edges"`
	Communities     []GraphQueryCommunity `json:"communities"`
}

type GraphQueryStore interface {
	LoadActiveGraphSnapshot(context.Context, core.GraphScope, int, int, int) (GraphQuerySnapshot, error)
	ResolveGraphCanonicalMemories(context.Context, core.GraphScope, []core.GraphEvidence) (map[string]core.MemoryEntry, map[string]struct{}, error)
}
