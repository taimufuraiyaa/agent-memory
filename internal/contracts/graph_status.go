package contracts

import "github.com/taimufuraiyaa/agent-memory/internal/core"

const SupportedGraphAdapterVersion = "0.1.0"

func GraphAdapterCompatible(name, version, artifactSchema string) bool {
	return (name == "agent-memory-graphrag-adapter" || name == "agent-memory-graphrag") &&
		version == SupportedGraphAdapterVersion && artifactSchema == GraphArtifactSchemaV1
}

func PopulateGraphStatusPolicy(status *GraphIndexStatus) {
	if status == nil {
		return
	}
	status.Degraded = status.Enabled && !status.Fresh
	switch status.State {
	case "incompatible":
		status.RemediationCode = "upgrade_or_rollback_adapter"
		status.AuthorizedOperations = []GraphOperationAction{GraphOperationDisable}
	case "disabled":
		status.RemediationCode = "enable_configuration"
		status.AuthorizedOperations = nil
	case "queued", "running":
		status.RemediationCode = "wait_or_cancel"
		status.AuthorizedOperations = []GraphOperationAction{GraphOperationCancel}
	case string(core.GraphJobFailed), string(core.GraphJobDeadLetter), string(core.GraphJobCancelled):
		status.RemediationCode = "inspect_safe_failure_and_retry"
		status.AuthorizedOperations = []GraphOperationAction{GraphOperationRetry, GraphOperationRebuild, GraphOperationDisable}
	case "not_indexed":
		status.RemediationCode = "run_rebuild"
		status.AuthorizedOperations = []GraphOperationAction{GraphOperationRebuild, GraphOperationDisable}
	case "stale":
		status.RemediationCode = "run_update"
		status.AuthorizedOperations = []GraphOperationAction{GraphOperationUpdate, GraphOperationRebuild, GraphOperationDisable}
	default:
		status.AuthorizedOperations = []GraphOperationAction{GraphOperationUpdate, GraphOperationRebuild, GraphOperationDisable}
		if status.PreviousRevisionID != "" {
			status.AuthorizedOperations = append(status.AuthorizedOperations, GraphOperationRollback)
		}
	}
}
