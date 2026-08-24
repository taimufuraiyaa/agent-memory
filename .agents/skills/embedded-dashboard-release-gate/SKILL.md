---
name: embedded-dashboard-release-gate
description: Verify that the agent-memory production binary embeds and serves the dashboard without npm, broken asset paths, or remote font dependencies.
---

# Embedded Dashboard Release Gate

Use this after dashboard changes or before a release.

1. Build the exact distribution path with `make build-with-dashboard`.
2. Confirm the CLI embedded-assets check delegates to
   `dashboard.HasEmbeddedAssets()` rather than a stub or hard-coded value.
3. Confirm Vite emits dashboard-relative asset URLs. The embedded index mounted
   at `/dashboard/` must reference `./assets/app.js` and `./assets/app.css`.
4. Confirm the production index does not reference Google Fonts or other
   unapproved remote assets.
5. Start the built binary with a temporary loopback address:
   `./bin/agent-memory dashboard --addr 127.0.0.1:<port> --no-open`.
6. Require the process output to say it is using embedded dashboard assets.
   Any Vite/npm startup is a failed production smoke test.
7. Fetch `/dashboard/` and `/dashboard/assets/app.js`. Require HTTP 200 for the
   JavaScript and verify notebook markers exist in the embedded bundle.
8. Exercise the detached lifecycle with the built binary on another fixed
   loopback port:
   - run `dashboard --start --no-open`
   - require the command to exit successfully with a dashboard URL
   - require `dashboard --status` to remain healthy after the starter exits
   - require the PID record to contain the child PID and non-empty URL
   - fetch the dashboard after the parent has exited
   - run `--start` again and require an idempotent already-running result
   - stop it with `dashboard --stop`

   A URL printed before the child immediately exits is a failed release gate.
   The child must publish the PID+URL handshake and run in a detached process
   session.
9. Stop the temporary process and run:
   - `go test ./...`
   - `npm --prefix tools/agent-memory/dashboard test`
   - `npm --prefix tools/agent-memory/dashboard run typecheck`
   - `git diff --check`

Keep the asset-path and self-contained-index assertions in
`internal/api/dashboard/assets_test.go` so this gate remains automated.
