from __future__ import annotations

import pandas as pd
from graphrag.config.models.graph_rag_config import GraphRagConfig
from graphrag.data_model.data_reader import DataReader
from graphrag.index.run.utils import get_update_table_providers
from graphrag.index.typing.context import PipelineRunContext
from graphrag.index.typing.workflow import WorkflowFunctionOutput
from graphrag.index.workflows.factory import PipelineFactory
from graphrag.index.workflows.update_communities import run_workflow as update_communities


RECONCILED_COMMUNITIES_WORKFLOW = "agent_memory_update_communities"


async def reconcile_update_communities(config: GraphRagConfig, context: PipelineRunContext) -> WorkflowFunctionOutput:
    result = await update_communities(config, context)
    # GraphRAG 3.1.2 maps reused entity titles onto the previous UUIDs during
    # entity merging and applies that mapping to text units, but not community
    # memberships. Use its explicit mapping before update_clean_state removes it.
    mapping = context.state.get("incremental_update_entity_id_mapping")
    entities = context.state.get("incremental_update_merged_entities")
    if not isinstance(mapping, dict) or not isinstance(entities, pd.DataFrame) or "id" not in entities.columns:
        raise ValueError("incremental entity reconciliation state is missing")
    current_ids = set(entities["id"])
    if any(entity_id not in current_ids for entity_id in mapping.values()):
        raise ValueError("incremental entity mapping is unresolved")
    output, _, _ = get_update_table_providers(config, context.state["update_timestamp"])
    communities = await DataReader(output).communities()

    def resolve_memberships(values: object) -> list[str]:
        if hasattr(values, "tolist"):
            values = values.tolist()
        if not isinstance(values, (list, tuple)):
            raise ValueError("incremental community membership is invalid")
        resolved = list(dict.fromkeys(mapping.get(entity_id, entity_id) for entity_id in values))
        if not resolved or any(entity_id not in current_ids for entity_id in resolved):
            raise ValueError("incremental community entity is unresolved")
        return resolved

    communities["entity_ids"] = communities["entity_ids"].map(resolve_memberships)
    await output.write_dataframe("communities", communities)
    return result


def configure_incremental_reconciliation(config: GraphRagConfig, method: str) -> None:
    # Register an adapter-owned workflow; never replace a global upstream one.
    PipelineFactory.register(RECONCILED_COMMUNITIES_WORKFLOW, reconcile_update_communities)
    workflows = config.workflows or PipelineFactory.pipelines[f"{method}-update"]
    config.workflows = [RECONCILED_COMMUNITIES_WORKFLOW if name == "update_communities" else name for name in workflows]
