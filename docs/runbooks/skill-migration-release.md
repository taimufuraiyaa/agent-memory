# Skill Revision Migration Release Gate

The rollout remains blocked until the migration report is `ready: true` for both a fresh database and an upgraded existing project.

## Required release procedure

1. Back up the project database and `.agents/skills` directory.
2. Import every existing root skill as immutable revision 1. Do not change the root file during import.
3. Run representative tasks through legacy selection and lifecycle selection in shadow mode. Record only task IDs and selected skill identities; do not record task content.
4. Run the migration gate. It must verify root materialization, active digest, immutable bundle, shadow parity, and last-known-good availability for every skill.
5. Keep lifecycle selection non-authoritative while any discrepancy exists.

## Rollback drill

1. Promote a non-production test revision through the normal generation-safe activation boundary.
2. Confirm the prior revision becomes last-known-good and its immutable digest verifies.
3. Roll back using the recorded generation and a new idempotency key.
4. Confirm the root skill digest, activation digest, and last-known-good transition agree.
5. A stale generation, missing immutable bundle, materialization mismatch, or failed restoration blocks release.
