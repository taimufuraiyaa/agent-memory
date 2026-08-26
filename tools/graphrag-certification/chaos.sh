#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

go test ./internal/saas/graphworker -run 'GraphWorker(AtLeastOnceReplayIsIdempotent|LossLeavesLeaseReclaimable|RejectsWorkspaceLimitBeforeAdapterModelCall)$' -count=1
go test ./internal/saas/graphindex -run 'GraphImportActivation(RejectsForgedEventBeforeLoad|CoalescesFinishedEvent)$|HostedGraphProjectionRebuildsBeforeAdapterStateCanExpire$' -count=1
go test ./internal/saas/isolationreview -run 'GraphTenantIsolationAcrossFullUpdateFailureReviewAndCredentialRevocation$' -count=1
