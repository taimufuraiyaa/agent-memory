UPDATE saas_launch_policy
SET signup_enabled = false,
    invitation_required = true,
    updated_by = 'migration',
    reason_code = 'safe_platform_default_closed',
    updated_at = clock_timestamp()
WHERE singleton = true
  AND phase = 'internal_alpha'
  AND (signup_enabled = true OR invitation_required = false);
