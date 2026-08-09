# Alpha, Beta, and GA Scorecards

## Content-free analytics

Only the allowlisted lifecycle events in `internal/saas/readiness` are accepted.
Dimensions may contain categorical format, phase, policy version, duration band,
and safe outcome codes. Content, prompts, responses, source bytes, credentials,
and free-form book metadata are rejected by the common safe-metadata validator.

## Internal alpha

Exercise signup, rights confirmation, upload, validation, indexing, cited query,
reviewed memory, export, source deletion, and account deletion with capped,
non-sensitive fixtures. Review funnel completion, failure class, queue age,
latency, deletion completion, audit integrity, and cost weekly. Every discovered
failure is assigned through `saas_failure_ownership`.

## Private beta

Admission requires a one-time hashed invitation, verified email match, approved
country, age confirmation, account cap, source cap, trial window, signup-rate
limit, and abuse threshold. Tenant feature flags and workload modes can pause
uploads or reduce expensive work without deleting state. Production billing,
support staffing, customer feedback, and notice staffing require human evidence.

## Public beta and GA

Public signup removes only the invitation requirement. Geography, age, rate,
account, trial, source, and abuse controls remain enforced. Country is accepted
only from a five-minute HMAC-signed trusted-edge assertion; direct client
headers fail closed. Launch dashboards
must cover funnel, availability, latency, jobs, deletion, security, billing,
support, and cost.

The release gate combines immutable-window samples from
`saas_release_evidence`, recent passed game days, and external signed approvals.
Use one complete approval-artifact directory per gate. For example:

```sh
go run ./cmd/agent-memory-release-gate --gate public_beta --window-days 28 \
  --approver-keys /release-control/approver-trust.json \
  --approvals-dir /release-evidence/public-beta
go run ./cmd/agent-memory-release-gate --gate ga --window-days 28 \
  --approver-keys /release-control/approver-trust.json \
  --approvals-dir /release-evidence/ga
```

Exit code `3` means a metric, freshness, drill, or approval condition is not
satisfied. See [Signed Release Approvals](release-approvals.md) for key setup,
artifact creation, required controls, failure behavior, and all exit codes. The
tool does not manufacture samples or approvals. Independent review, real
observation time, and accountable-owner decisions remain external gates.

## Future organization isolation

Organization accounts are post-MVP and cannot reuse personal-tenant assumptions
by simply adding members. They require a separate requirements/design review for
membership lifecycle, invitations, RBAC, sharing, export, deletion, billing,
support access, and adversarial isolation. Individual tenant composite keys,
forced RLS, object namespaces, and authorization-before-retrieval remain minimum
controls and may not be weakened for organization convenience.
