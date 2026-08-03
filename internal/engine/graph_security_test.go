package engine_test

import "testing"

func TestAuthorizationLeakGraphTraversalExpansion(t *testing.T) {
	TestKnowledgeGraphPreservesConflictAndAuthorization(t)
}
