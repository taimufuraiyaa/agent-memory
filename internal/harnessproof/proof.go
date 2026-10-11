// Package harnessproof carries the proof that a person approved one action. The run
// manager mints it, in a context, only after consuming a recorded approval; a mutating
// tool refuses to run unless the context carries a proof for exactly the action it was
// asked to perform. The proof is a second, independent lock behind policy: a mistake in
// a policy table cannot change a file, because the call would still arrive without one.
//
// Mint is exported so tests can exercise a tool directly. A test in this package walks
// the repository and fails if any non-test source outside the run manager mentions it.
package harnessproof

import "context"

type key struct{}

// Mint returns a context carrying the digest of an approved action.
func Mint(ctx context.Context, digest string) context.Context {
	return context.WithValue(ctx, key{}, digest)
}

// Approved returns the digest of the action a person approved for this call, or the empty
// string when the call was not approved.
func Approved(ctx context.Context) string {
	digest, _ := ctx.Value(key{}).(string)
	return digest
}
