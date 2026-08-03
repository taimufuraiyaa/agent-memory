# Integration Contract

`GET /api/v1/capabilities` describes the stable contract used by CLI, HTTP,
hooks, and MCP adapters. The initial contract is `v1` and covers health,
write, search, recall, feedback, session listing, and session finalization.
The default MCP discovery profile exposes write, search, recall, feedback, and
session finalization. Health and session listing remain part of the stable
contract but require `AGENT_MEMORY_MCP_PROFILE=expanded` in MCP clients.

Compatibility rules:

- Additive response fields and new optional inputs are backward compatible.
- Removing or renaming an operation, required field, or error code requires a
  new major contract version.
- Adapters must reject unsupported major versions instead of guessing.
- Server-side request, response, and recall clipping limits are authoritative.
- `compact` output is intended for agent context; `full` output includes
  diagnostics such as score explanations and clipping metadata.

The capability response contains no paths, credentials, or user configuration.
