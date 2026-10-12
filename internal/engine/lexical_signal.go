package engine

import "math"

// ApplyLexicalSignal adds the relative BM25 contribution to one hit. relative
// is the hit's lexical score divided by the best lexical score of the same
// query, so it is in (0, 1] and independent of corpus size. A zero weight or
// non-positive relative value leaves the hit untouched.
func ApplyLexicalSignal(hit *RetrievalHit, relative, weight float64) {
	if hit == nil || weight <= 0 || relative <= 0 || math.IsNaN(relative) {
		return
	}
	relative = math.Min(1, relative)
	contribution := weight * relative
	hit.Breakdown.Lexical = relative
	hit.Breakdown.Activation += contribution
	hit.Breakdown.Total += contribution
	hit.Score += contribution
}
