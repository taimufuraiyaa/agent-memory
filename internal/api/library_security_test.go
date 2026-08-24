package api

import "testing"

func TestAuthorizationLeakWikiProjectionCache(t *testing.T) {
	TestWikiProjectionIsEvidenceExpandableRegenerableAndAuthorizationKeyed(t)
}
func TestAuthorizationLeakSeminarExistenceAndStreamState(t *testing.T) {
	TestSeminarProgressAndAuthorizedIdempotentCancellation(t)
}
