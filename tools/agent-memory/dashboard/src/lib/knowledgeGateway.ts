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
  kind: 'study' | 'upload' | 'indexing' | 'session' | 'retrieval' | 'feedback' | 'deletion'
  title: string
  state: 'queued' | 'running' | 'completed' | 'failed'
  updatedAt: string
  progress?: number
  failure?: { message: string; retryAllowed: boolean }
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
  listWorkspaces(signal?: AbortSignal): Promise<WorkspaceSummary[]>
  ask(scope: WorkspaceScope, question: string, signal?: AbortSignal): Promise<AskResponse>
  search(scope: WorkspaceScope, query: string, cursor?: string, signal?: AbortSignal): Promise<CursorPage<KnowledgeResult>>
  browse(scope: WorkspaceScope, mode: 'recent' | 'pinned' | 'type', cursor?: string, signal?: AbortSignal): Promise<CursorPage<KnowledgeResult>>
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
  listActivity(scope: WorkspaceScope, cursor?: string, signal?: AbortSignal): Promise<CursorPage<ActivityItem>>
  retryActivity(scope: WorkspaceScope, activityId: string): Promise<void>
  submitFeedback(scope: WorkspaceScope, requestId: string, score: number, reason: string): Promise<void>
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
