# Signed Release Approvals

Release approval is deliberately outside the Agent Memory application trust
boundary. Metrics and game-day receipts are queried from PostgreSQL, but human
decisions are Ed25519-signed JSON artifacts verified with a separately managed
public-key bundle. Application or database access alone cannot approve a
release.

## Owner key setup

Each accountable owner generates and retains a separate private key. Do not put
private keys in this repository, the SaaS database, container images, CI logs,
or application secrets.

```sh
openssl genpkey -algorithm ED25519 -out security-owner.pem
chmod 600 security-owner.pem
go run ./cmd/agent-memory-release-approval \
  --private-key security-owner.pem \
  --print-public-key
```

The last command prints the raw 32-byte public key as base64. A release trust
administrator places that value in an out-of-band trust bundle:

```json
{
  "schema": "agent-memory-approver-trust-v1",
  "keys": [
    {
      "key_id": "security-2026",
      "owner": "security-review",
      "public_key": "REPLACE_WITH_BASE64_PUBLIC_KEY",
      "gates": ["private_beta", "ga"],
      "controls": ["security_review"]
    }
  ]
}
```

The trust bundle is integrity-critical even though it contains only public
keys. Distribute it through the deployment control plane or another
independently access-controlled configuration system. A signer must match the
artifact owner and be explicitly scoped to its gate and control.

## Create a decision artifact

The evidence file is hashed locally; its contents are not copied into the
artifact or application database. Use a durable, content-free URI that the
release reviewer can resolve in the external evidence system.

For staging deployment and rollback evidence, use the JSON receipt emitted by
`scripts/saas-kubernetes-release.sh` with
`AGENT_MEMORY_RELEASE_RECEIPT_PATH`. Review it against
`api/evidence/v1/kubernetes-release-receipt.schema.json`, upload the unchanged
file to the external evidence system, and use that file as `--evidence` below.
The approval command independently hashes it; never sign a reconstructed
dashboard summary in place of the original receipt.

```sh
go run ./cmd/agent-memory-release-approval \
  --private-key security-owner.pem \
  --gate private_beta \
  --control security_review \
  --decision approved \
  --owner security-review \
  --key-id security-2026 \
  --evidence security-release-report.pdf \
  --evidence-ref report://security/private-beta-2026-08 \
  --valid-for 168h \
  > private-beta-approvals/security-review.json
```

The private-key file must be a regular, non-symlink PKCS#8 PEM file with no
group or other permissions. The evidence file must also be a regular,
non-symlink file. Approval lifetime defaults to seven days and cannot exceed 90
days. To withdraw an approval, issue a newer signed artifact with
`--decision rejected`; the latest signed decision for a control wins.

Store the complete decision history for one gate in one immutable exported
directory. Omitting a newer rejection from an export could expose an older
approval, so evidence-system access control and export completeness are part of
the release procedure. The verifier rejects symlinks, unknown JSON fields,
wrong-gate artifacts, ambiguous timestamps, invalid signatures, unauthorized
scope, future issue times, and expired decisions.

## Required controls

| Gate | Signed controls |
|---|---|
| `private_beta` | `legal_review`, `operations_review`, `privacy_review`, `product_review`, `security_review` |
| `public_beta` | `beta_readiness`, `external_signup`, `legal_pages`, `security_contact`, `status_page`, `support_policy` |
| `ga` | `legal_review`, `operations_review`, `privacy_review`, `product_review`, `security_review` |
| `external_evidence` | Every normalized control in `api/evidence/v1/external-control-catalog.json` (for example, `P1.2-A` uses `p1_2_a`) |

The external-evidence gate verifies the complete P0-P12 collection independently
of the three launch gates. Follow the
[External Evidence Index Runbook](external-evidence-index.md); do not add all 57
controls to a launch-gate trust scope unless that signer is genuinely authorized
for every one of them.

## Evaluate a release

```sh
export AGENT_MEMORY_POSTGRES_URL='postgres://...'
go run ./cmd/agent-memory-release-gate \
  --gate private_beta \
  --window-days 28 \
  --approver-keys /release-control/approver-trust.json \
  --approvals-dir /release-evidence/private-beta-approvals
```

The command requires all of the following at the same time:

- every configured metric passes over one shared evidence window;
- the metric window ends within the last 24 hours;
- every required game day passed during the requested window; and
- every required control has a current, authorized signed approval.

Exit code `0` is ready, `3` is a valid but unsatisfied gate, `2` is invalid or
missing configuration, and `1` is an operational or artifact-verification
error. The JSON report identifies failed/missing metrics, shared-window and
freshness state, missing drills, and missing/rejected/expired approvals.
