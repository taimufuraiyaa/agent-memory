import type { ClientKind, ClientProfile, ClientToolProfile, LocalLLMConfig, LocalLLMStatus, SchedulerRunHistory, SchedulerSummary, SkillInfo } from './api'

export type KnowledgeCapability =
  | 'workspace'
  | 'ask'
  | 'search'
  | 'browse'
  | 'source'
  | 'study'
  | 'note'
  | 'activity'
  | 'settings'
  | 'lifecycle'
  | 'clients'
  | 'skills'
  | 'translation'
  | 'graph'

export type WorkspaceScope = { workspaceId: string; sourceId?: string }

export type WorkspaceSummary = {
  id: string
  name: string
  kind: 'registered-project' | 'hosted'
  memoryCount: number
  sourceCount: number
  noteCount: number
  latestActivity?: string
  connectionState: 'connected' | 'offline' | 'unavailable'
  capabilities: KnowledgeCapability[]
}

export type KnowledgeResult = {
  id: string
  kind: 'memory' | 'source-evidence'
  workspaceId: string
  sourceId?: string
  memoryType?: string
  title?: string
  content: string
  provenance?: string
  confidence?: number
  relevance?: number
  explanation?: string
  updatedAt?: string
  pinned?: boolean
  actions: Array<'open' | 'pin' | 'unpin' | 'export' | 'print' | 'delete'>
}

export type CursorPage<T> = { items: T[]; nextCursor?: string }

export type AskResponse = {
  requestId?: string
  answerable: boolean
  answer?: string
  sourceEvidence: KnowledgeResult[]
  durableMemory: KnowledgeResult[]
  weakContext: KnowledgeResult[]
  unavailableReason?: string
  graphRoute?: GraphRouteDecision
  graphContext?: GraphRecallContext
}

export type GraphQueryMode = 'basic' | 'auto' | 'local_graph' | 'global'
export type GraphAskOptions = { mode: GraphQueryMode; required?: boolean; allowStale?: boolean }
export type GraphRouteDecision = {
  requested_mode: GraphQueryMode
  selected_mode: GraphQueryMode
  intent: 'direct' | 'relational' | 'global'
  reason_code: string
  fallback: boolean
  degraded: boolean
  fresh: boolean
  active_revision_id?: string
}
export type GraphEvidence = { id: string; canonical_kind: string; canonical_id: string; canonical_fingerprint: string; locator?: string; occurrence_count: number }
export type GraphHop = { edge_id: string; from_entity_id: string; to_entity_id: string; kind: string; trust: string; direction: string; reason_code: string; influence: number; evidence: GraphEvidence[] }
export type GraphPath = { seed: { canonical_kind: string; canonical_id: string; score: number }; entity_ids: string[]; hops: GraphHop[]; evidence: GraphEvidence[]; path_score: number; can_support: boolean }
export type GraphCommunityContext = { id: string; level: number; rank: number; trust: string; fresh: boolean; source_count: number; unresolved_count: number; title: string; summary: string; findings?: string[]; evidence: GraphEvidence[] }
export type GraphRecallContext = {
  revision_id?: string
  fresh?: boolean
  canonical_memory_ids?: string[]
  degraded_reason?: string
  local?: { paths: GraphPath[]; conflicts: Array<{ seed: GraphPath['seed']; hop: GraphHop }> }
  global?: { communities: GraphCommunityContext[]; evidence: GraphEvidence[]; covered_sources: number; unresolved_evidence: number }
}

export type GraphReadiness = { configuration_id?: string; ready: boolean; enabled: boolean; compatible: boolean; state: string; adapter_name?: string; adapter_version?: string; artifact_schema_version?: string; reason_code?: string; reason?: string }
export type GraphStatus = { configuration_id: string; configuration_version: number; enabled: boolean; state: string; adapter_name?: string; adapter_version?: string; compatible: boolean; index_method?: string; artifact_schema_version?: string; active_revision_id?: string; previous_revision_id?: string; indexed_watermark: { sequence: number; event_time: string; digest: string }; pending_changes: number; pending_records: number; current_job?: { id: string; state: string; created_at: string; updated_at: string }; queue_age_seconds: number; last_job_state?: string; last_job_id?: string; last_successful_at?: string; estimated_cost_usd: number; cost_available: boolean; fresh: boolean; degraded: boolean; remediation_code?: string; authorized_operations: GraphOperationAction[] }
export type GraphOperationAction = 'update' | 'rebuild' | 'cancel' | 'retry' | 'disable' | 'rollback'
export type GraphEntityRecord = { entity: { id: string; trust: string; superseded_by?: string }; version: { name: string; entity_type: string; description: string; aliases?: string[]; occurrence_count: number; degree: number }; evidence: GraphEvidence[]; record_version: number }
export type GraphEdgeRecord = { edge: { id: string; source_entity_id: string; target_entity_id: string; normalized_kind: string; external_kind?: string; trust: string }; version: { description: string; weight: number; origin: string; provenance_approved: boolean }; evidence: GraphEvidence[]; record_version: number }
export type GraphCommunityRecord = { community: { id: string; level: number; entity_count: number; edge_count: number; source_count: number; unresolved_count: number }; members: Array<{ kind: string; target_id: string }>; report: { id: string; title: string; summary: string; findings?: string[]; trust: string; stale: boolean; admission_state?: string; evidence_count: number; unresolved_count: number; review_version: number }; evidence: GraphEvidence[] }
export type GraphSnapshot = { scope: { tenant_id?: string; workspace_id: string }; configuration_id: string; revision_id: string; cache_identity: string; fresh: boolean; nodes: GraphEntityRecord[]; edges: GraphEdgeRecord[]; communities: GraphCommunityRecord[] }
export type GraphReviewInput = { targetKind: 'entity' | 'edge' | 'report'; targetId: string; action: 'approve' | 'reject' | 'supersede' | 'annotate' | 'reconsider'; from: string; to: string; expectedVersion: number; reason?: string }

export type TranslationResult = { text: string; targetLanguage: string; provider: string; model: string }

export type SourceSummary = {
  id: string
  workspaceId: string
  kind: 'codebase' | 'document' | 'note'
  title: string
  format?: 'pdf' | 'epub' | 'markdown' | 'text'
  state: 'registered' | 'uploading' | 'parsing' | 'ocr-required' | 'ocr-processing' | 'indexing' | 'ready' | 'failed'
  statusLabel: string
  updatedAt?: string
  failure?: { message: string; retryAllowed: boolean }
}

export type StudyRequest = WorkspaceScope & {
  depth: 'shallow' | 'medium' | 'deep'
  preview: boolean
  maxFiles: number
  offset: number
}

export type StudyResult = {
  preview: boolean
  scannedFiles: number
  extracted: number
  skipped: number
  writtenIds: string[]
  errors: Array<{ path: string; reason: string }>
  offset: number
  pageFiles: number
  nextOffset: number
  hasMore: boolean
}

export type NoteSummary = { id: string; workspaceId: string; title: string; path: string; updatedAt: string; deleted: boolean }

export type WorkspaceNote = NoteSummary & {
  body: string
  properties: Record<string, unknown>
  revision: number
  indexState: 'pending' | 'indexing' | 'ready' | 'failed' | 'retired' | 'paused'
  indexError?: string
}

export type ActivityItem = {
  id: string
  workspaceId: string
  kind: 'study' | 'upload' | 'indexing' | 'session' | 'episode' | 'retrieval' | 'feedback' | 'deletion'
  title: string
  state: 'queued' | 'running' | 'completed' | 'failed'
  updatedAt: string
  progress?: number
  failure?: { message: string; retryAllowed: boolean }
  feedback?: {
    requestId: string
    requestType: string
    query: string
    score: number
    reason: string
    usefulCount?: number
    totalCount?: number
  }
  episode?: SolutionEpisodeSummary
}

export type ActivityFilter = 'all' | ActivityItem['kind']
export const ACTIVITY_PAGE_SIZE = 10

export type SolutionEpisodeSummary = {
  id: string
  workspaceId: string
  principalId: string
  sessionId: string
  goal: string
  status: 'active' | 'paused' | 'completed' | 'partial' | 'abandoned' | 'cancelled'
  retention: 'transient' | 'standard' | 'pinned'
  version: number
  supersededBy?: string
  outcome?: 'success' | 'failure' | 'partial'
  summary?: string
  validation?: 'proposed' | 'verified' | 'rejected'
  pinned: boolean
  stepCount: number
  createdAt: string
  updatedAt: string
  finalizedAt?: string
}

export type SolutionEpisodeDetail = SolutionEpisodeSummary & {
  steps: Array<{ id: string; ordinal: number; kind: string; status: string; summary: string; rationale?: string; confidence: number; references: Array<{ kind: string; targetId: string; locator?: string; resolution?: string }>; createdAt: string; misleading: boolean; redacted: boolean; reviewReason?: string; reasonClass?: string }>
  evidence: Array<{ kind: string; targetId: string; locator?: string; resolution?: string }>
  risks: string[]
  nextGuidance?: string
  promotions: Array<{ id: string; kind: string; memoryType?: string; targetId?: string; state: string; createdAt: string }>
  promotionTargets: Array<{ promotionId: string; kind: string; memoryType?: string; targetId?: string; state: string; availability: string; memory?: KnowledgeResult; createdAt: string }>
  pathFeedback: Array<{ id: string; targetId: string; outcome: 'helpful' | 'ignored' | 'rejected' | 'harmful'; createdAt: string }>
}

export type SolutionEpisodeReviewInput = {
  principalId: string
  episodeId: string
  action: 'pin' | 'misleading' | 'redact' | 'correct' | 'supersede' | 'delete'
  stepId?: string
  reason?: string
  reasonClass?: string
  summary?: string
  successorEpisodeId?: string
  idempotencyKey?: string
  pinned?: boolean
}

export type SourceUploadInput = {
  scope: WorkspaceScope
  file: File
  format: 'pdf' | 'epub' | 'markdown' | 'text'
  rightsBasis: 'author-owned' | 'licensed' | 'public-domain-or-open' | 'lawfully-acquired-private-use'
}

export interface KnowledgeGateway {
  readonly runtime: 'standalone' | 'hosted'
  readonly capabilities: ReadonlySet<KnowledgeCapability>
  supports(capability: KnowledgeCapability, scope: WorkspaceScope): boolean
  listWorkspaces(signal?: AbortSignal): Promise<WorkspaceSummary[]>
  ask(scope: WorkspaceScope, question: string, options?: GraphAskOptions, signal?: AbortSignal): Promise<AskResponse>
  translateAnswer(scope: WorkspaceScope, text: string, targetLanguage: string, signal?: AbortSignal): Promise<TranslationResult>
  getTranslationStatus(): Promise<LocalLLMStatus>
  testTranslationSettings(input: LocalLLMConfig): Promise<LocalLLMStatus>
  saveTranslationSettings(input: LocalLLMConfig): Promise<LocalLLMStatus>
  search(scope: WorkspaceScope, query: string, cursor?: string, signal?: AbortSignal): Promise<CursorPage<KnowledgeResult>>
  browse(scope: WorkspaceScope, mode: 'recent' | 'pinned' | 'type' | 'ungrouped', cursor?: string, signal?: AbortSignal): Promise<CursorPage<KnowledgeResult>>
  getMemory(scope: WorkspaceScope, memoryId: string, signal?: AbortSignal): Promise<KnowledgeResult>
  setMemoryPinned(scope: WorkspaceScope, memoryId: string, pinned: boolean): Promise<void>
  deleteMemories(scope: WorkspaceScope, memoryIds: string[]): Promise<void>
  listSources(scope: WorkspaceScope, signal?: AbortSignal): Promise<SourceSummary[]>
  uploadSource(input: SourceUploadInput): Promise<SourceSummary>
  retrySource(scope: WorkspaceScope, sourceId: string): Promise<SourceSummary>
  deleteSource(scope: WorkspaceScope, sourceId: string): Promise<void>
  study(input: StudyRequest): Promise<StudyResult>
  listNotes(scope: WorkspaceScope, includeDeleted: boolean, signal?: AbortSignal): Promise<NoteSummary[]>
  getNote(scope: WorkspaceScope, noteId: string, signal?: AbortSignal): Promise<WorkspaceNote>
  createNote(scope: WorkspaceScope, input: { title: string; path: string; body: string; properties: Record<string, unknown> }): Promise<WorkspaceNote>
  updateNote(scope: WorkspaceScope, note: WorkspaceNote): Promise<WorkspaceNote>
  trashNote(scope: WorkspaceScope, noteId: string): Promise<void>
  restoreNote(scope: WorkspaceScope, noteId: string): Promise<void>
  deleteNote(scope: WorkspaceScope, noteId: string): Promise<void>
  retryNoteIndex(scope: WorkspaceScope, noteId: string): Promise<void>
  listActivity(scope: WorkspaceScope, cursor?: string, filter?: ActivityFilter, signal?: AbortSignal): Promise<CursorPage<ActivityItem>>
  retryActivity(scope: WorkspaceScope, activityId: string): Promise<void>
  listHowHistory(scope: WorkspaceScope, signal?: AbortSignal): Promise<SolutionEpisodeSummary[]>
  getSolutionEpisode(scope: WorkspaceScope, episodeId: string, signal?: AbortSignal): Promise<SolutionEpisodeDetail>
  reviewSolutionEpisode(scope: WorkspaceScope, input: SolutionEpisodeReviewInput): Promise<void>
  submitFeedback(scope: WorkspaceScope, requestId: string, score: number, reason: string): Promise<void>
  listLifecycle(scope: WorkspaceScope, signal?: AbortSignal): Promise<{ scheduler?: SchedulerSummary; history: SchedulerRunHistory[] }>
  listSkills(scope: WorkspaceScope, signal?: AbortSignal): Promise<SkillInfo[]>
  listSkillLifecycle(scope: WorkspaceScope, signal?: AbortSignal): Promise<import('./api').SkillLifecycleSummary[]>
  inspectSkillLifecycle(scope: WorkspaceScope, skillId: string, environment?: string): Promise<import('./api').SkillLifecycleDetail>
  operateSkillLifecycle(scope: WorkspaceScope, actor: string, operation: string, payload: Record<string, unknown>): Promise<unknown>
  listClientProfiles(signal?: AbortSignal): Promise<{ profiles: ClientProfile[] }>
  createClientProfile(input: { id: string; display_name: string; client_kind: ClientKind; tool_profile: ClientToolProfile }): Promise<{ profile: ClientProfile }>
  updateClientProfile(input: { id: string; display_name: string; client_kind: ClientKind; tool_profile: ClientToolProfile; expected_revision: number }): Promise<{ profile: ClientProfile }>
  deleteClientProfile(input: { id: string; expected_revision: number }): Promise<{ deleted: boolean; id: string }>
  getSettings(scope: WorkspaceScope, signal?: AbortSignal): Promise<Record<string, unknown>>
  getGraphReadiness(scope: WorkspaceScope, signal?: AbortSignal): Promise<GraphReadiness>
  getGraphStatus(scope: WorkspaceScope, signal?: AbortSignal): Promise<GraphStatus>
  getGraphSnapshot(scope: WorkspaceScope, signal?: AbortSignal): Promise<GraphSnapshot>
  operateGraph(scope: WorkspaceScope, configurationId: string, action: GraphOperationAction, expectedRevision?: string, jobId?: string): Promise<GraphStatus>
  reviewGraph(scope: WorkspaceScope, input: GraphReviewInput): Promise<void>
  submitGraphFeedback(scope: WorkspaceScope, requestId: string, targetKind: string, targetId: string, outcome: 'helpful' | 'ignored' | 'rejected' | 'harmful', reason?: string): Promise<void>
  importMigration(scope: WorkspaceScope, file: File, passphrase: string, idempotencyKey: string): Promise<{ imported: number; merged: number; skipped: number; failed: number }>
}

export class UnsupportedCapabilityError extends Error {
  constructor(readonly capability: KnowledgeCapability) {
    super(`${capability} is not available in this runtime.`)
    this.name = 'UnsupportedCapabilityError'
  }
}

export function requireCapability(gateway: Pick<KnowledgeGateway, 'capabilities'>, capability: KnowledgeCapability): void {
  if (!gateway.capabilities.has(capability)) throw new UnsupportedCapabilityError(capability)
}
