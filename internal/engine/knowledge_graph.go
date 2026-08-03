package engine

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

type KnowledgeGraphRepository interface {
	PutKnowledgeEdge(context.Context, core.KnowledgeEdge) error
	ListKnowledgeEdgesFrom(context.Context, string) ([]core.KnowledgeEdge, error)
	GetLibraryResourcePolicy(context.Context, library.ResourceType, string) (library.LibraryResourcePolicy, error)
}
type KnowledgeGraph struct{ repository KnowledgeGraphRepository }

func NewKnowledgeGraph(repository KnowledgeGraphRepository) *KnowledgeGraph {
	return &KnowledgeGraph{repository: repository}
}
func (g *KnowledgeGraph) Put(ctx context.Context, e core.KnowledgeEdge) error {
	if g == nil || g.repository == nil {
		return errors.New("knowledge graph repository is required")
	}
	return g.repository.PutKnowledgeEdge(ctx, e)
}
func (g *KnowledgeGraph) Expand(ctx context.Context, scope core.AuthorizationScope, nodeID string) ([]core.KnowledgeEdge, error) {
	if g == nil || g.repository == nil {
		return nil, errors.New("knowledge graph repository is required")
	}
	start, err := g.repository.GetLibraryResourcePolicy(ctx, library.ResourceGraphNode, nodeID)
	if err != nil || !core.Authorize(scope, start.Policy, core.CapabilityReadSource).Allowed {
		return []core.KnowledgeEdge{}, nil
	}
	edges, err := g.repository.ListKnowledgeEdgesFrom(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	out := []core.KnowledgeEdge{}
	for _, edge := range edges {
		target, err := g.repository.GetLibraryResourcePolicy(ctx, library.ResourceGraphNode, edge.ToID)
		if err == nil && core.Authorize(scope, target.Policy, core.CapabilityReadSource).Allowed {
			out = append(out, edge)
		}
	}
	return out, nil
}
