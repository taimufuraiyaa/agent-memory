# agent-memory menubar

Thin macOS menu bar controller for the local `agent-memory` installation.

## Phase 1
- Open / start / stop dashboard
- Show installed `agent-memory` version
- Trigger `agent-memory upgrade --yes`
- Detect whether future `serve` support exists

## Run From Source

```bash
cd tools/agent-memory/menubar
swift run
```

## Package As `.app`

```bash
cd tools/agent-memory/menubar
./scripts/package_app.sh
open dist/AgentMemoryMenuBar.app
```

Output:
- `tools/agent-memory/menubar/dist/AgentMemoryMenuBar.app`

Optional signing / notarization:

```bash
SIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" ./scripts/package_app.sh
NOTARY_PROFILE="agent-memory-notary" ./scripts/package_app.sh
```

## Notes
- Packaged `.app` builds prefer the bundled `agent-memory` backend in `Contents/Resources/bin/agent-memory`.
- Source runs still fall back to the local `agent-memory` binary on your `PATH`.
- The app now uses real `agent-memory serve` controls when that command is available.
