#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

go test ./internal/contracts ./internal/validation ./internal/saas/objectcustody ./internal/saas/isolationreview -run 'Graph' -count=1
go test ./internal/saas/deletion ./internal/saas/export -run 'Graph' -count=1
go vet ./internal/contracts ./internal/validation ./internal/saas/graphworker ./internal/saas/graphindex
