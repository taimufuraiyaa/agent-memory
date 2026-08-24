# Launch notice legal and staffing evidence

This boundary creates one content-free receipt supporting P6.5-A and CP6-A.
It proves structural legal-copy, jurisdiction-routing, deadline, escalation,
staffing, and tabletop coverage. It does not provide legal advice or approve
either external control.

## Prepare the evidence

First produce a ready P0.1 receipt using the launch-scope runbook. The notice
collector reloads that receipt, revalidates every normalized field, and hashes
its exact bytes. Edited, unready, symlinked, or substituted prerequisites fail
closed.

Copy [`launch-notice-readiness.example.json`](launch-notice-readiness.example.json)
to private storage. Replace every identifier, timestamp, count, and digest with
the real reviewed artifacts. The number of routes must exactly match the P0.1
notice-jurisdiction count. Use a stable SHA-256 reference for each jurisdiction;
do not put country codes, jurisdiction names, notice copy, claimants, contacts,
staff names, schedules, cases, or source data in the input.

Each route needs complete language coverage, positive normal and urgent
deadlines with the urgent deadline no longer than the normal deadline, and at
least one primary plus backup escalation path. Notice intake, legal review, and
user response each need primary and backup staffing coverage. Run and reconcile
at least one valid, invalid, conflicting, and urgent tabletop scenario.

## Normalize

```sh
make saas-notice-readiness-check \
  LAUNCH_SCOPE_RECEIPT=/secure/launch-scope-receipt.json \
  NOTICE_READINESS_INPUT=/secure/notice-readiness-input.json \
  NOTICE_READINESS_RECEIPT=/secure/notice-readiness-receipt.json
```

The receipt destination must not exist. Publication is create-only mode `0600`.
Exit `0` means ready normalized evidence, `3` means complete valid-unready
evidence, `2` means CLI misuse, and `1` means malformed, unsafe,
contradictory, prerequisite, or write failure.

Retain the private source artifacts and receipt according to the approved
custody policy. Counsel must separately sign the exact receipt for P6.5-A;
Legal Operations and Support must separately sign it for CP6-A. Add both real
dossiers and current decisions through the unchanged signed external-evidence
index. A locally ready fixture closes neither row.
