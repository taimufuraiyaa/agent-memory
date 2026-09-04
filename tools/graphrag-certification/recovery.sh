#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

go test ./internal/saas/backup -run 'GraphBackupRestoreReplaysDeletionAndRebuildsWithoutNativeArtifacts$' -count=1
go test ./internal/saas/deletion -run 'GraphDeletion' -count=1
go test ./internal/application ./internal/integration -run 'Graph.*(Lifecycle|Activation|Restore|Standalone|Deletion)' -count=1
