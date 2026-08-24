ALTER TABLE saas_retention_policies ADD COLUMN purpose text;

UPDATE saas_retention_policies SET purpose = CASE data_class
    WHEN 'account_identity' THEN 'Operate the private account and satisfy account lifecycle obligations.'
    WHEN 'sessions_credentials' THEN 'Authenticate access and revoke compromised or expired credentials.'
    WHEN 'memory_content' THEN 'Provide user-requested memory storage, retrieval, and deletion.'
    WHEN 'source_originals' THEN 'Preserve the user private copy for authorized citation-grounded interactions.'
    WHEN 'source_derived' THEN 'Provide searchable passages and rebuildable retrieval projections.'
    WHEN 'exports' THEN 'Provide a time-limited user-requested portability download.'
    WHEN 'model_usage' THEN 'Reconcile quotas, provider usage, and content-free service cost.'
    WHEN 'audit_events' THEN 'Detect abuse, investigate incidents, and prove accountable operations.'
    WHEN 'security_cases' THEN 'Investigate and resolve security, trust, and abuse cases.'
    WHEN 'billing_records' THEN 'Reconcile subscriptions, payments, disputes, and statutory finance records.'
    WHEN 'backups' THEN 'Recover authoritative service state while enforcing deletion tombstones.'
    WHEN 'analytics' THEN 'Measure content-free reliability, capacity, and product operation.'
END;

ALTER TABLE saas_retention_policies
    ALTER COLUMN purpose SET NOT NULL,
    ADD CONSTRAINT saas_retention_policy_purpose CHECK (char_length(purpose) BETWEEN 1 AND 512 AND purpose = btrim(purpose));
