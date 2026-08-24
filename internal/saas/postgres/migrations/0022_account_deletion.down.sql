DROP TABLE IF EXISTS saas_audit_pseudonymization;
DROP TABLE IF EXISTS saas_account_deletion_policies;
ALTER TABLE saas_deletion_operations DROP COLUMN IF EXISTS execute_after;
