# Integration Contract

`GET /api/v1/capabilities` describes the stable contract used by CLI, HTTP,
hooks, and MCP adapters. The initial contract is `v1` and covers health,
write, search, recall, feedback, session listing, and session finalization.
The default MCP discovery profile exposes write, search, recall, feedback, and
session finalization. Health and session listing remain part of the stable
contract but require `AGENT_MEMORY_MCP_PROFILE=expanded` in MCP clients.
Local installations can instead persist this choice per client through
`/api/v1/client-profiles` and select it with `AGENT_MEMORY_CLIENT_ID`. When a
client ID is present, its persisted profile is authoritative and is resolved
before the MCP input loop starts; changes apply on reconnect.

Compatibility rules:

- Additive response fields and new optional inputs are backward compatible.
- Removing or renaming an operation, required field, or error code requires a
  new major contract version.
- Adapters must reject unsupported major versions instead of guessing.
- Server-side request, response, and recall clipping limits are authoritative.
- `compact` output is intended for agent context; `full` output includes
  diagnostics such as score explanations and clipping metadata.

The capability response contains no paths, credentials, or user configuration.

## Internal infrastructure settings

The internal operator UI persists the self-managed platform's monthly operations
budget and decision status through `GET` and `PUT
/api/v1/deployment-profile`. This profile belongs to the entire installation;
it is not a client profile, tenant preference, or customer billing control.
Updates use an expected revision to prevent accidental overwrites.

The safe initial profile is an assumed USD `1,000` monthly infrastructure
operations budget. The endpoint stores planning metadata only and contains no
cloud-provider choice, region, credentials, or spending authorization. It does
not deploy resources, authorize a purchase, or satisfy release approval controls.
