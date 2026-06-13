#!/usr/bin/env bash
#
# Repo hygiene cleanup for agent-memory.
#
# Removes scratch/debug files and committed build artifacts identified during
# the system design review (see the "Repository hygiene" section of the
# review). This script is intentionally conservative:
#
#   - `git rm --cached --ignore-unmatch` removes a path from git's index if
#     it is tracked, and is a no-op if it is not.
#   - `rm -rf` then removes the path from disk regardless of whether it was
#     tracked.
#
# Nothing here touches bin/, .build/, benchmark/bin/, or benchmark/results/,
# which are already covered by .gitignore.
#
# Usage:
#   bash scripts/repo-hygiene-cleanup.sh
#
# Review the "git status" output and diff before committing.

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

remove() {
  local path="$1"
  if [ -e "$path" ]; then
    git rm --cached --ignore-unmatch -q -- "$path" >/dev/null 2>&1 || true
    rm -rf -- "$path"
    echo "removed: $path"
  else
    echo "skip (not found): $path"
  fi
}

# --- Build artifacts that shouldn't be committed ----------------------------
# 22MB binary committed at the repo root (not previously covered by
# .gitignore; `bin/`, `.build`, and `*.out` don't match a bare `agent-memory`
# file). Use `make build` -> bin/agent-memory instead.
remove "agent-memory"

# --- Leftover backup files from the install.go refactor ---------------------
remove "install.go.backup"
remove "install.go.backup-final"

# --- Manual debugging session artifacts --------------------------------------
remove "err.log"
remove "out.log"

# --- Ad-hoc debug/test scripts left at the repo root -------------------------
# If you still actively use these for manual menubar/dashboard debugging,
# move them instead of deleting, e.g.:
#   mkdir -p scripts/debug && git mv test_cli.swift test_race.sh scripts/debug/
remove "test_cli.swift"
remove "test_race.sh"

# --- Resolved debugging notes -------------------------------------------------
# Both root causes documented here (the dashboard's external-serve PID
# fallback path, and the scheduler binding to the dashboard placeholder DB
# for workspace-scoped runs) are fixed in the current code and covered by:
#   - internal/cli/serve_command_test.go: TestServeSchedulerWorkspaceDBPathUsesWorkspaceDBForPlaceholder
#   - internal/cli/dashboard_command_test.go: TestDashboardPIDPathsFallbackOrder
#   - internal/api/server_test.go: TestExternalServePIDCandidatesOrder,
#     TestExternalSchedulerSummaryFindsUnsuffixedServePID
remove "debug-dashboard-scheduler-sync.md"

echo
echo "Done. Review 'git status' before committing."
