# Audit Policy

Audit events are append-only, sanitized operational records and are excluded
from memory search and recall. Structural mutation paths audit writes,
supersession, deletion, archive/restore, promotion/demotion, consolidation,
retention/eviction, and import. Bulk target ID lists are capped while the full
affected count is preserved.

Zero-mutation lifecycle sweeps intentionally produce no audit event. This keeps
the stream focused on state changes; scheduler run history remains the source
for proving that a no-op sweep ran. Destructive deletion and its required audit
event share one transaction and therefore roll back together.
