ALTER TABLE saas_memories ADD COLUMN source jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE saas_memories ADD COLUMN entities text[] NOT NULL DEFAULT '{}';
ALTER TABLE saas_memories ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
ALTER TABLE saas_memories ADD COLUMN keywords jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE saas_memories ADD COLUMN outcome jsonb;
ALTER TABLE saas_memories ADD COLUMN confidence double precision NOT NULL DEFAULT 0.8 CHECK (confidence >= 0 AND confidence <= 1);
ALTER TABLE saas_memories ADD COLUMN storage_tier text NOT NULL DEFAULT 'vector';
ALTER TABLE saas_memories ADD COLUMN session_id uuid;
ALTER TABLE saas_memories ADD COLUMN idempotency_key text NOT NULL;
ALTER TABLE saas_memories ADD COLUMN request_hash text NOT NULL;
ALTER TABLE saas_memories ADD CONSTRAINT saas_memories_idempotency UNIQUE (tenant_id, workspace_id, idempotency_key);

ALTER TABLE saas_feedback ADD COLUMN reconsolidation_action text NOT NULL DEFAULT '';
ALTER TABLE saas_feedback ADD COLUMN successor_memory_id uuid;

CREATE TABLE saas_note_revisions (
    tenant_id uuid NOT NULL,
    note_id uuid NOT NULL,
    version bigint NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    actor_id text NOT NULL,
    request_id text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, note_id, version),
    FOREIGN KEY (tenant_id, note_id) REFERENCES saas_notes(tenant_id, id)
);

ALTER TABLE saas_note_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_note_revisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_note_revisions
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

