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
}

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
  ask(scope: WorkspaceScope, question: string, signal?: AbortSignal): Promise<AskResponse>
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
  listClientProfiles(signal?: AbortSignal): Promise<{ profiles: ClientProfile[] }>
  createClientProfile(input: { id: string; display_name: string; client_kind: ClientKind; tool_profile: ClientToolProfile }): Promise<{ profile: ClientProfile }>
  updateClientProfile(input: { id: string; display_name: string; client_kind: ClientKind; tool_profile: ClientToolProfile; expected_revision: number }): Promise<{ profile: ClientProfile }>
  deleteClientProfile(input: { id: string; expected_revision: number }): Promise<{ deleted: boolean; id: string }>
  getSettings(scope: WorkspaceScope, signal?: AbortSignal): Promise<Record<string, unknown>>
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
