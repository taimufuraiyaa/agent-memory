# Staging human and agent journey evidence

This workflow combines two content-free journey receipts produced by the real
staging web and scoped-agent clients. It binds both to one exact passed staging
Kubernetes release receipt. The collector validates evidence; it does not log
in, mint credentials, execute the journeys, deploy software, or certify CP3-A
from local fixtures.

## Private input contract

The human runner and agent runner each create one private JSON file conforming
to `api/evidence/v1/staging-client-journey.schema.json`. Both inputs use:

- schema `agent-memory-staging-client-journey-v1`;
- classification `staging_external` and environment `staging`;
- the exact release ID and SHA-256 of the same passed release receipt;
- client kind `human_web` or `scoped_agent`;
- a unique lowercase 32-character trace ID;
- a post-release UTC window no longer than 30 minutes; and
- exactly five fixed checks with unique UUID-v4 request IDs.

The fixed checks are `identity_authenticated`, `memory_write_audited`,
`memory_search_audited`, `export_ready_audited`, and `client_cleanup`. For the
human runner, cleanup means logout. For the agent runner, cleanup means
revocation of the temporary scoped credential. A ready input has all five
outcomes set to `passed`; a failed operation is retained as `failed` with
`ready: false`.

Successful write, search, and export operations must be joined to the matching
content-free audit and telemetry records by request/trace ID before their
checks are marked passed. Retain those raw private records outside the
application database. Do not copy them into the journey JSON.

Never put tokens, cookies, authorization headers, URLs, tenant/account/
workspace/memory/export/credential identifiers, memory content, queries,
results, export bodies, browser storage, screenshots, logs, or raw audit and
telemetry bodies in either input.

## Combine

Run the command within 24 hours of both journeys. Choose a receipt destination
that does not exist:

```sh
make saas-staging-journey-collect \
  STAGING_RELEASE=/absolute/private/staging-release.json \
  STAGING_HUMAN_JOURNEY=/absolute/private/human-journey.json \
  STAGING_AGENT_JOURNEY=/absolute/private/agent-journey.json \
  STAGING_JOURNEY_RECEIPT=/absolute/private/staging-journey-bundle.json
```

The command writes a create-only mode-`0600` receipt conforming to
`api/evidence/v1/staging-client-journey-bundle.schema.json`. The bundle keeps
only release and input digests, client kinds, request/trace IDs, UTC windows,
fixed outcomes, and aggregate readiness. CLI output contains counts only.

Exit code `0` means both client journeys passed. Exit code `3` means the inputs
were valid and the bundle was written, but at least one fixed check failed.
Exit code `2` means command usage was invalid. Exit code `1` means an input was
malformed or unsafe, release binding failed, or receipt publication failed.

## Retain and approve

Store the passed release receipt, both journey inputs, the combined receipt,
matching private audit/trace exports, and the Product/QA review in the external
immutable CP3-A dossier. Bind its digest using the signed release-approval and
external-evidence index workflows.

Mock inputs, local Compose, automated unit tests, and a locally signed fixture
prove only that the collector fails closed. CP3-A and Checkpoint 3 remain open
until both inputs come from the real self-managed staging clients and an
authorized Product/QA decision references the immutable dossier.
