import type { SolutionEpisodeDetailRecord, SolutionEpisodeRecord } from '../api'
import type { ActivityItem, SolutionEpisodeDetail } from '../knowledgeGateway'

export function solutionActivityItem(record: SolutionEpisodeRecord): ActivityItem {
  const episode = record.episode
  const state = episode.status === 'active' || episode.status === 'paused' ? 'running' : episode.status === 'abandoned' || episode.status === 'cancelled' ? 'failed' : 'completed'
  return {
    id: `episode:${episode.id}`,
    workspaceId: episode.workspace,
    kind: 'episode',
    title: episode.goal_summary,
    state,
    updatedAt: episode.updated_at,
    episode: {
      id: episode.id, workspaceId: episode.workspace, principalId: episode.principal_id, sessionId: episode.session_id,
      goal: episode.goal_summary, status: episode.status, retention: episode.retention_class, version: episode.version,
      supersededBy: episode.superseded_by, outcome: record.summary?.outcome, summary: record.summary?.summary,
      validation: record.summary?.validation, pinned: record.pinned, stepCount: record.step_count,
      createdAt: episode.created_at, updatedAt: episode.updated_at, finalizedAt: record.summary?.created_at,
    },
  }
}

export function solutionDetail(record: SolutionEpisodeDetailRecord): SolutionEpisodeDetail {
  const summary = solutionActivityItem(record).episode!
  const reviews = new Map(record.step_reviews.map((review) => [review.step_id, review]))
  const references = [...(record.summary?.evidence || []), ...record.steps.flatMap((step) => step.references || [])]
  const evidence = references.filter((reference, index) => references.findIndex((candidate) =>
    candidate.kind === reference.kind && candidate.target_id === reference.target_id && candidate.locator === reference.locator && candidate.resolution === reference.resolution) === index)
  return {
    ...summary,
    steps: record.steps.map((step) => {
      const review = reviews.get(step.id)
      return { id: step.id, ordinal: step.ordinal, kind: step.kind, status: step.status, summary: step.summary,
        rationale: step.rationale_summary, confidence: step.confidence, createdAt: step.created_at,
        references: (step.references || []).map((reference) => ({ kind: reference.kind, targetId: reference.target_id, locator: reference.locator, resolution: reference.resolution })),
        misleading: Boolean(review?.misleading), redacted: Boolean(review?.redacted), reviewReason: review?.reason, reasonClass: review?.reason_class }
    }),
    evidence: evidence.map((reference) => ({ kind: reference.kind, targetId: reference.target_id, locator: reference.locator, resolution: reference.resolution })),
    risks: record.summary?.risks || [],
    nextGuidance: record.summary?.next_guidance,
    promotions: record.promotions.map((promotion) => ({ id: promotion.id, kind: promotion.kind, memoryType: promotion.memory_type, targetId: promotion.target_id, state: promotion.state, createdAt: promotion.created_at })),
    promotionTargets: (record.promotion_targets || []).map((target) => ({
      promotionId: target.promotion.id, kind: target.promotion.kind, memoryType: target.promotion.memory_type,
      targetId: target.promotion.target_id, state: target.promotion.state, availability: target.availability, createdAt: target.promotion.created_at,
      memory: target.memory ? { id: target.memory.id, kind: 'memory', workspaceId: target.memory.workspace, memoryType: target.memory.type,
        content: target.memory.content, provenance: target.memory.source?.file_path || target.memory.source?.note_path || target.memory.source?.type,
        confidence: target.memory.confidence, updatedAt: target.memory.updated_at, pinned: target.memory.pinned, actions: ['open'] } : undefined,
    })),
    pathFeedback: (record.path_feedback || []).map((feedback) => ({ id: feedback.id, targetId: feedback.target_id, outcome: feedback.outcome, createdAt: feedback.created_at })),
  }
}
