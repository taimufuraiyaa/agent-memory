#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

go test ./internal/evaluation -run 'GraphRAG(LargeCorpus|ProductionGoldCorpus|Day1Day10)' -count=3
go test ./internal/saas/graphworker -run 'GraphWorkerRejectsWorkspaceLimitBeforeAdapterModelCall$' -count=3
go test ./internal/application -run 'GraphBackpressureRejectsUnsafeWorkAndBoundsLease$' -count=3
