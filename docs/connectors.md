# Connector SDK and Filesystem Reference

Connectors are isolated producers of normalized observations. They cannot call
the memory write pipeline. An emitter acknowledges an event only after durable
observation acceptance; the connector saves its checkpoint after that
acknowledgement. Failed instances degrade independently.

The lifecycle contract is `Validate`, `Start`, `Stop`, and `Health`, with an
emitter and checkpoint store supplied by the host. Checkpoints contain opaque
source state, update time, last error, and emitted/coalesced/rescanned counters.

## Filesystem configuration

```yaml
connectors:
  - id: project-notes
    type: filesystem
    enabled: true
    workspace: my-project
    roots: [/absolute/path/to/notes]
    ignore: [".git/*", "node_modules/*", "*.key"]
    preview_bytes: 1024
    poll_interval_ms: 1000
```

Roots must be explicit existing directories and cannot themselves be symlinks.
Symlink entries are skipped, previews are bounded and scrubbed, and polling acts
as the recovery rescan after downtime. Unchanged files are not emitted again.
Network connectors remain deferred until these checkpoint semantics are stable.
