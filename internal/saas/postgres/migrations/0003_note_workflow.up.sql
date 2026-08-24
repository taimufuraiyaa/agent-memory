ALTER TABLE saas_notes ADD COLUMN path text NOT NULL DEFAULT '';
ALTER TABLE saas_notes ADD COLUMN properties jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE saas_notes ADD COLUMN content_hash text NOT NULL DEFAULT '';
ALTER TABLE saas_notes ADD COLUMN index_state text NOT NULL DEFAULT 'pending';
ALTER TABLE saas_notes ADD COLUMN indexed_version bigint NOT NULL DEFAULT 0;
ALTER TABLE saas_notes ADD COLUMN index_error text NOT NULL DEFAULT '';
ALTER TABLE saas_notes ADD COLUMN idempotency_key text NOT NULL DEFAULT '';
ALTER TABLE saas_notes ADD CONSTRAINT saas_notes_idempotency UNIQUE (tenant_id, workspace_id, idempotency_key);

ALTER TABLE saas_note_revisions ADD COLUMN path text NOT NULL DEFAULT '';
ALTER TABLE saas_note_revisions ADD COLUMN properties jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE saas_note_revisions ADD COLUMN content_hash text NOT NULL DEFAULT '';
ALTER TABLE saas_note_revisions ADD COLUMN author_kind text NOT NULL DEFAULT 'member';

ALTER TABLE saas_sessions_memory ADD COLUMN idempotency_key text NOT NULL DEFAULT '';
ALTER TABLE saas_sessions_memory ADD COLUMN transcript_hash text NOT NULL DEFAULT '';
ALTER TABLE saas_sessions_memory ADD CONSTRAINT saas_sessions_memory_idempotency UNIQUE (tenant_id, workspace_id, idempotency_key);
