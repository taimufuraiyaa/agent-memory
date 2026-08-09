DROP TRIGGER IF EXISTS saas_launch_policy_history_immutable ON saas_launch_policy_history;
DROP FUNCTION IF EXISTS saas_deny_launch_history_mutation();
DROP TRIGGER IF EXISTS saas_launch_policy_audit ON saas_launch_policy;
DROP FUNCTION IF EXISTS saas_capture_launch_policy_change();
DROP TABLE IF EXISTS saas_launch_policy_history;
ALTER TABLE saas_launch_policy DROP COLUMN IF EXISTS reason_code,DROP COLUMN IF EXISTS updated_by;

