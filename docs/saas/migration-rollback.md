# Local-to-Hosted Migration Rollback

Use this procedure when a portable import report, retrieval parity gate, or
hosted operational signal is not accepted.

1. Stop using the hosted profile. Keep the original local database and AMPB2
   bundle unchanged until reconciliation is accepted.
2. Run `agent-memory hosted logout --profile <name>` to revoke the hosted agent
   credential and remove it from the OS keyring. Use `--local-only` only when
   the service is unreachable and record the server-side revocation as follow-up.
3. Compare the import report with the local selection manifest. Investigate
   every `failed` or unexplained `skipped` item; do not retry with modified
   bytes under the same idempotency key.
4. If hosted data must be removed, request each source deletion and then account
   deletion. Retain operation IDs and deletion receipts. Access is revoked
   immediately; physical purge and backup tombstones complete asynchronously.
5. Continue in explicit local mode. Do not copy hosted IDs into the local
   database and do not overwrite the local database with an export.
6. Re-run the versioned SQLite/PostgreSQL retrieval parity gate after a fix.
   Resume hosted use only when ordering, exact terms, feedback, decay,
   suppression, citations, and reconciliation all pass their approved threshold.

Rollback never restores a deleted source from object storage. If a user chooses
to migrate again, create a new encrypted bundle from their retained lawful local
copy, review the source-byte selection, and use a new passphrase.
