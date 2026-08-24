# External Integration Data Policy

External integrations are disabled by default. Enabling one requires a named
owner, purpose, jurisdiction, contract/data-processing review, retention and
training settings, credential custody, traffic allowlist, outage behavior, exit
procedure, and signed approval that identifies the exact configuration version.

| Integration | Allowed purpose and minimum data | Prohibited data/use | Required evidence |
|---|---|---|---|
| Payment processor | Account-scoped billing reference, plan/SKU, amount, currency, tax fields required by law, invoice and settlement references | Source bytes/text, memories, prompts, citations, search history, API credentials, internal tenant topology | Contract/subprocessor review, webhook-signature configuration, test charge/refund, settlement reconciliation, retention/deletion settings |
| Transactional email | Verified destination, approved template ID/version, locale, minimal account or operation reference, expiry where applicable | Source content, extracted passages, memories, prompts, unrestricted audit data, secrets | Template/copy approval, suppression/bounce behavior, delivery reference, retention setting, jurisdiction and exit review |
| Model API | Only the minimum authorized passages and instruction required for the user-requested embedding or synthesis route | General logging, shared training, unrelated tenant data, hidden source expansion, credentials, internal object keys, raw provider error bodies | Route/model/version, endpoint allowlist, contract and training/retention settings, redaction/token limits, traffic sample, outage/circuit and cost tests |

## Enforcement contract

- All calls pass through backend-owned adapters; clients cannot select arbitrary
  endpoints or credentials.
- Destination hosts are operator-configured and allowlisted. Redirects, DNS
  rebinding, loopback/link-local/private-network access, and unbounded response
  bodies fail closed where an external HTTP adapter is used.
- Secrets are referenced by the internal secret system and never returned by
  APIs, UI, logs, metrics, traces, receipts, or evidence reports.
- Telemetry contains bounded operation, route, outcome, duration, and integer
  usage/cost fields only.
- Provider error bodies are suppressed. Model failure returns cited evidence
  without fabricated generated text.
- A change in purpose, destination, jurisdiction, retention, training use, data
  fields, or subprocessor requires a new approval before traffic resumes.

## Review and exit

Operations must be able to disable each integration without losing authoritative
Agent Memory data. Finance reconciles payment exports against the internal usage
ledger. Support and privacy reconcile email delivery/suppression state according
to the approved retention policy. Model routes require no content export because
model responses are non-authoritative; only content-free request and cost
references are retained.

P0.2-C remains open until privacy and security approve the exact enabled
integration contracts/settings and compare real egress traffic with this policy.
