# Billing, Trial-Abuse, and Payment Incident Runbook

## Prevention

Signup admission enforces network/email hashes, geography, age confirmation,
account caps, per-hour attempt limits, rejection thresholds, trial duration,
source caps, and one-use invitation reservations. Entitlements additionally
bound bytes, source count, concurrent uploads/jobs, request rate, storage, and
monthly model tokens. `EconomicsService.WorstCase` must fit the approved plan
ceiling before a rollout cap is raised.

## Provider webhook incident

1. Pause plan mutations but keep customer data and reads intact.
2. Verify provider signature and event identity; never replay an unverified
   payload.
3. Compare provider creation time with the last applied event. Duplicate and
   older events remain recorded but unapplied.
4. Reconcile subscriptions, entitlements, invoices, and the authoritative usage
   ledger. Record only provider references and payload hashes.
5. Keep past-due tenants in their approved grace state. Do not delete sources as
   a payment-failure side effect.
6. Resume webhooks oldest-to-newest, rerun reconciliation, and verify quota
   behavior before reopening plan changes.

## Trial-abuse response

Use content-free attempt/fingerprint evidence. Narrow controls in order: signup
rate, invitation requirement, geography, account cap, source cap, then signup
freeze. Any tenant suspension or credential revocation goes through the audited
containment policy. Appeals never require support to inspect source content.

