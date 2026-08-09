ALTER TABLE saas_tenant_entitlements DROP COLUMN IF EXISTS billing_state,DROP COLUMN IF EXISTS max_storage_bytes,DROP COLUMN IF EXISTS max_tokens_per_month,DROP COLUMN IF EXISTS max_requests_per_minute,DROP COLUMN IF EXISTS max_concurrent_jobs,DROP COLUMN IF EXISTS entitlement_version,DROP COLUMN IF EXISTS plan_id;
DROP TABLE IF EXISTS saas_invoices;
DROP TABLE IF EXISTS saas_request_rate_windows;
DROP TABLE IF EXISTS saas_usage_aggregates;
DROP TABLE IF EXISTS saas_usage_events;
DROP TABLE IF EXISTS saas_billing_webhook_events;
DROP TABLE IF EXISTS saas_subscriptions;
DROP TABLE IF EXISTS saas_plans;
