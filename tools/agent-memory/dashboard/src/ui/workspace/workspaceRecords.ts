import type { AskResponse, GraphCommunityContext, GraphEvidence, GraphHop, GraphPath, GraphQueryMode, GraphRouteDecision, KnowledgeResult } from '../../lib/knowledgeGateway'

export const WORKSPACE_RECORDS_KEY = 'agent-memory:workspace-work-items:v1'
export const MAX_WORK_ITEMS_PER_WORKSPACE = 40
export const MAX_CHAT_TURNS = 20
export const MAX_WORKSPACE_RECORDS_CHARS = 1_500_000

type StoredAskResponse = Pick<AskResponse, 'requestId' | 'answerable' | 'answer' | 'sourceEvidence' | 'durableMemory' | 'weakContext' | 'unavailableReason' | 'graphRoute' | 'graphContext'>

export type ChatTurn = {
  id: string
  question: string
  createdAt: string
  response?: StoredAskResponse
  failed?: boolean
}

export type ChatWorkItem = {
  id: string
  workspaceId: string
  type: 'chat'
  title: string
  titleEdited: boolean
  createdAt: string
  updatedAt: string
  turns: ChatTurn[]
}

export type SearchWorkItem = {
  id: string
  workspaceId: string
  type: 'search'
  title: string
  titleEdited: boolean
  createdAt: string
  updatedAt: string
  query: string
}

export type WorkspaceWorkItem = ChatWorkItem | SearchWorkItem

export type WorkspaceRecordsState = {
  items: WorkspaceWorkItem[]
  error?: string
  canPersist: boolean
}

type WorkspaceRecordEnvelope = { version: 1; workspaces: Record<string, WorkspaceWorkItem[]> }

export class WorkspaceRecordsError extends Error {}

export function newWorkspaceWorkItemId(): string {
  return globalThis.crypto?.randomUUID?.() || `local-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

export function createChatRecord(workspaceId: string): ChatWorkItem {
  const now = new Date().toISOString()
  return { id: newWorkspaceWorkItemId(), workspaceId, type: 'chat', title: 'New chat', titleEdited: false, createdAt: now, updatedAt: now, turns: [] }
}

export function createSearchRecord(workspaceId: string): SearchWorkItem {
  const now = new Date().toISOString()
  return { id: newWorkspaceWorkItemId(), workspaceId, type: 'search', title: 'New search', titleEdited: false, createdAt: now, updatedAt: now, query: '' }
}

export function readWorkspaceWorkItems(workspaceId: string): WorkspaceRecordsState {
  try {
    const storage = getStorage()
    if (!storage) return { items: [], canPersist: false, error: 'Browser storage is unavailable. New chats and searches will stay open only in this tab.' }
    const envelope = readEnvelope(storage)
    return { items: envelope.workspaces[workspaceId] || [], canPersist: true }
  } catch (reason) {
    return { items: [], canPersist: false, error: reason instanceof Error ? reason.message : 'Local chat and search history could not be read.' }
  }
}

export function saveWorkspaceWorkItem(item: WorkspaceWorkItem): void {
  const storage = getStorage()
  if (!storage) throw new WorkspaceRecordsError('Browser storage is unavailable. This record can be used until the page is closed.')
  const envelope = readEnvelope(storage)
  const current = envelope.workspaces[item.workspaceId] || []
  const index = current.findIndex((candidate) => candidate.id === item.id)
  const next = index < 0 ? [...current, item] : current.map((candidate, offset) => offset === index ? item : candidate)
  if (next.length > MAX_WORK_ITEMS_PER_WORKSPACE) throw new WorkspaceRecordsError(`This workspace can store up to ${MAX_WORK_ITEMS_PER_WORKSPACE} chats and searches. Delete a local record before creating another.`)
  envelope.workspaces[item.workspaceId] = next
  const serialized = JSON.stringify(envelope)
  if (serialized.length > MAX_WORKSPACE_RECORDS_CHARS) throw new WorkspaceRecordsError('Local chat history is full. Delete an older local chat or search before saving more content.')
  try { storage.setItem(WORKSPACE_RECORDS_KEY, serialized) } catch { throw new WorkspaceRecordsError('The browser could not save this chat. Free browser storage space and try again.') }
}

export function deleteWorkspaceWorkItem(workspaceId: string, workItemId: string): void {
  const storage = getStorage()
  if (!storage) throw new WorkspaceRecordsError('Browser storage is unavailable; this local record could not be deleted.')
  const envelope = readEnvelope(storage)
  envelope.workspaces[workspaceId] = (envelope.workspaces[workspaceId] || []).filter((item) => item.id !== workItemId)
  if (!envelope.workspaces[workspaceId].length) delete envelope.workspaces[workspaceId]
  try { storage.setItem(WORKSPACE_RECORDS_KEY, JSON.stringify(envelope)) } catch { throw new WorkspaceRecordsError('The browser could not update local history. Try again after freeing storage space.') }
}

export function clearAllWorkspaceWorkItems(): void {
  const storage = getStorage()
  if (!storage) throw new WorkspaceRecordsError('Browser storage is unavailable; local history could not be cleared.')
  try { storage.removeItem(WORKSPACE_RECORDS_KEY) } catch { throw new WorkspaceRecordsError('The browser could not clear local history. Check browser storage permissions and try again.') }
}

export function renameWorkspaceWorkItem(item: WorkspaceWorkItem, title: string): WorkspaceWorkItem {
  const nextTitle = title.trim().slice(0, 80)
  if (!nextTitle) throw new WorkspaceRecordsError('Enter a name before saving.')
  return { ...item, title: nextTitle, titleEdited: true, updatedAt: new Date().toISOString() }
}

export function titleFromPrompt(prompt: string, fallback: string): string {
  const normalized = prompt.trim().replace(/\s+/g, ' ')
  return normalized ? normalized.slice(0, 80) : fallback
}

export function responseForStorage(response: AskResponse, workspaceId: string): StoredAskResponse {
  return {
    requestId: response.requestId,
    answerable: response.answerable,
    answer: response.answer,
    sourceEvidence: response.sourceEvidence.filter((item) => item.workspaceId === workspaceId).slice(0, 20),
    durableMemory: response.durableMemory.filter((item) => item.workspaceId === workspaceId).slice(0, 20),
    weakContext: response.weakContext.filter((item) => item.workspaceId === workspaceId).slice(0, 20),
    unavailableReason: response.unavailableReason,
    graphRoute: sanitizeGraphRoute(response.graphRoute),
    graphContext: sanitizeGraphContext(response.graphContext),
  }
}

function getStorage(): Storage | null {
  try { return typeof window === 'undefined' ? null : window.localStorage } catch { return null }
}

function readEnvelope(storage: Storage): WorkspaceRecordEnvelope {
  const raw = storage.getItem(WORKSPACE_RECORDS_KEY)
  if (!raw) return { version: 1, workspaces: {} }
  if (raw.length > MAX_WORKSPACE_RECORDS_CHARS) throw new WorkspaceRecordsError('Local chat history exceeds its size limit. Existing data was left untouched; clear local history to continue.')
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new WorkspaceRecordsError('Local chat history is unreadable. Existing data was left untouched; clear this browser’s Agent Memory history to start again.') }
  if (!isRecord(parsed) || parsed.version !== 1 || !isRecord(parsed.workspaces)) throw new WorkspaceRecordsError('Local chat history uses an unsupported format. Existing data was left untouched.')
  const workspaces: Record<string, WorkspaceWorkItem[]> = {}
  for (const [workspaceId, value] of Object.entries(parsed.workspaces)) {
    if (typeof workspaceId !== 'string' || !Array.isArray(value)) throw new WorkspaceRecordsError('Local chat history has an invalid workspace record. Existing data was left untouched; clear local history to continue.')
    if (value.length > MAX_WORK_ITEMS_PER_WORKSPACE) throw new WorkspaceRecordsError('Local chat history exceeds its item limit. Existing data was left untouched; clear local history to continue.')
    const items = value.map((candidate) => sanitizeWorkItem(candidate, workspaceId))
    if (items.some((item) => !item)) throw new WorkspaceRecordsError('Local chat history has an invalid record. Existing data was left untouched; clear local history to continue.')
    if (items.length) workspaces[workspaceId] = items as WorkspaceWorkItem[]
  }
  return { version: 1, workspaces }
}

function sanitizeWorkItem(value: unknown, workspaceId: string): WorkspaceWorkItem | null {
  if (!isRecord(value) || typeof value.id !== 'string' || value.id.length > 120 || typeof value.title !== 'string' || value.title.length > 80 || typeof value.createdAt !== 'string' || typeof value.updatedAt !== 'string' || value.workspaceId !== workspaceId || typeof value.titleEdited !== 'boolean') return null
  const base = { id: value.id, workspaceId, title: value.title, titleEdited: value.titleEdited, createdAt: value.createdAt, updatedAt: value.updatedAt }
  if (value.type === 'chat' && Array.isArray(value.turns)) {
    if (value.turns.length > MAX_CHAT_TURNS) return null
    const turns = value.turns.map((turn) => sanitizeTurn(turn, workspaceId))
    if (turns.some((turn) => !turn)) return null
    return { ...base, type: 'chat', turns: turns as ChatTurn[] }
  }
  if (value.type === 'search' && typeof value.query === 'string' && value.query.length <= 2000) return { ...base, type: 'search', query: value.query }
  return null
}

function sanitizeTurn(value: unknown, workspaceId: string): ChatTurn | null {
  if (!isRecord(value) || typeof value.id !== 'string' || value.id.length > 120 || typeof value.question !== 'string' || value.question.length > 2000 || typeof value.createdAt !== 'string') return null
  const turn: ChatTurn = { id: value.id, question: value.question, createdAt: value.createdAt }
  if (value.failed === true) turn.failed = true
  if ('response' in value) {
    const response = sanitizeResponse(value.response, workspaceId)
    if (!response) return null
    turn.response = response
  }
  return turn
}

function sanitizeResponse(value: unknown, workspaceId: string): StoredAskResponse | undefined {
  if (!isRecord(value) || typeof value.answerable !== 'boolean' || !Array.isArray(value.sourceEvidence) || !Array.isArray(value.durableMemory) || !Array.isArray(value.weakContext)) return undefined
  if (value.sourceEvidence.length > 20 || value.durableMemory.length > 20 || value.weakContext.length > 20 || (typeof value.answer === 'string' && value.answer.length > 80_000)) return undefined
  const inScope = (item: KnowledgeResult | null): item is KnowledgeResult => Boolean(item && item.workspaceId === workspaceId)
  const sourceEvidence = value.sourceEvidence.map(sanitizeResult)
  const durableMemory = value.durableMemory.map(sanitizeResult)
  const weakContext = value.weakContext.map(sanitizeResult)
  if (sourceEvidence.some((item) => !inScope(item)) || durableMemory.some((item) => !inScope(item)) || weakContext.some((item) => !inScope(item))) return undefined
  return {
    requestId: typeof value.requestId === 'string' && value.requestId.length <= 120 ? value.requestId : undefined,
    answerable: value.answerable,
    answer: typeof value.answer === 'string' ? value.answer : undefined,
    sourceEvidence: sourceEvidence as KnowledgeResult[],
    durableMemory: durableMemory as KnowledgeResult[],
    weakContext: weakContext as KnowledgeResult[],
    unavailableReason: typeof value.unavailableReason === 'string' && value.unavailableReason.length <= 1000 ? value.unavailableReason : undefined,
    graphRoute: sanitizeGraphRoute(value.graphRoute),
    graphContext: sanitizeGraphContext(value.graphContext),
  }
}

function sanitizeGraphRoute(value: unknown): GraphRouteDecision | undefined {
  if (!isRecord(value)) return undefined
  const modes: GraphQueryMode[] = ['basic', 'auto', 'local_graph', 'global']
  const intents = ['direct', 'relational', 'global'] as const
  if (!modes.includes(value.requested_mode as GraphQueryMode) || !modes.includes(value.selected_mode as GraphQueryMode) || !intents.includes(value.intent as typeof intents[number]) || typeof value.reason_code !== 'string' || value.reason_code.length > 120 || typeof value.fallback !== 'boolean' || typeof value.degraded !== 'boolean' || typeof value.fresh !== 'boolean') return undefined
  return {
    requested_mode: value.requested_mode as GraphQueryMode,
    selected_mode: value.selected_mode as GraphQueryMode,
    intent: value.intent as GraphRouteDecision['intent'],
    reason_code: value.reason_code,
    fallback: value.fallback,
    degraded: value.degraded,
    fresh: value.fresh,
    active_revision_id: typeof value.active_revision_id === 'string' && value.active_revision_id.length <= 240 ? value.active_revision_id : undefined,
  }
}

function sanitizeGraphContext(value: unknown): AskResponse['graphContext'] {
  if (!isRecord(value)) return undefined
  const context: NonNullable<AskResponse['graphContext']> = {}
  if (typeof value.revision_id === 'string' && value.revision_id.length <= 240) context.revision_id = value.revision_id
  if (typeof value.fresh === 'boolean') context.fresh = value.fresh
  if (typeof value.degraded_reason === 'string' && value.degraded_reason.length <= 120) context.degraded_reason = value.degraded_reason
  if (Array.isArray(value.canonical_memory_ids)) context.canonical_memory_ids = value.canonical_memory_ids.filter((id): id is string => typeof id === 'string' && id.length <= 240).slice(0, 200)
  if (isRecord(value.local)) {
    const paths = Array.isArray(value.local.paths) ? value.local.paths.map(sanitizeGraphPath).filter((path): path is GraphPath => path !== null).slice(0, 20) : []
    const conflicts = Array.isArray(value.local.conflicts) ? value.local.conflicts.map(sanitizeGraphConflict).filter((conflict): conflict is { seed: GraphPath['seed']; hop: GraphHop } => conflict !== null).slice(0, 20) : []
    context.local = { paths, conflicts }
  }
  if (isRecord(value.global)) {
    const communities = Array.isArray(value.global.communities) ? value.global.communities.map(sanitizeGraphCommunity).filter((item): item is GraphCommunityContext => item !== null).slice(0, 20) : []
    const evidence = Array.isArray(value.global.evidence) ? value.global.evidence.map(sanitizeGraphEvidence).filter((item): item is GraphEvidence => item !== null).slice(0, 200) : []
    const coveredSources = finiteNumber(value.global.covered_sources)
    const unresolvedEvidence = finiteNumber(value.global.unresolved_evidence)
    if (coveredSources !== undefined && unresolvedEvidence !== undefined) context.global = { communities, evidence, covered_sources: coveredSources, unresolved_evidence: unresolvedEvidence }
  }
  return context
}

function sanitizeGraphPath(value: unknown): GraphPath | null {
  if (!isRecord(value) || !isRecord(value.seed) || !Array.isArray(value.entity_ids) || !Array.isArray(value.hops) || !Array.isArray(value.evidence) || typeof value.can_support !== 'boolean') return null
  const seed = sanitizeGraphSeed(value.seed)
  const pathScore = finiteNumber(value.path_score)
  if (!seed || pathScore === undefined) return null
  return {
    seed,
    entity_ids: value.entity_ids.filter((id): id is string => typeof id === 'string' && id.length <= 240).slice(0, 24),
    hops: value.hops.map(sanitizeGraphHop).filter((hop): hop is GraphHop => hop !== null).slice(0, 12),
    evidence: value.evidence.map(sanitizeGraphEvidence).filter((item): item is GraphEvidence => item !== null).slice(0, 40),
    path_score: pathScore,
    can_support: value.can_support,
  }
}

function sanitizeGraphSeed(value: unknown): GraphPath['seed'] | null {
  if (!isRecord(value) || typeof value.canonical_kind !== 'string' || value.canonical_kind.length > 80 || typeof value.canonical_id !== 'string' || value.canonical_id.length > 240) return null
  const score = finiteNumber(value.score)
  return score === undefined ? null : { canonical_kind: value.canonical_kind, canonical_id: value.canonical_id, score }
}

function sanitizeGraphHop(value: unknown): GraphHop | null {
  if (!isRecord(value) || typeof value.edge_id !== 'string' || value.edge_id.length > 240 || typeof value.from_entity_id !== 'string' || value.from_entity_id.length > 240 || typeof value.to_entity_id !== 'string' || value.to_entity_id.length > 240 || typeof value.kind !== 'string' || value.kind.length > 120 || typeof value.trust !== 'string' || value.trust.length > 80 || typeof value.direction !== 'string' || value.direction.length > 40 || typeof value.reason_code !== 'string' || value.reason_code.length > 120 || !Array.isArray(value.evidence)) return null
  const influence = finiteNumber(value.influence)
  if (influence === undefined) return null
  return { edge_id: value.edge_id, from_entity_id: value.from_entity_id, to_entity_id: value.to_entity_id, kind: value.kind, trust: value.trust, direction: value.direction, reason_code: value.reason_code, influence, evidence: value.evidence.map(sanitizeGraphEvidence).filter((item): item is GraphEvidence => item !== null).slice(0, 20) }
}

function sanitizeGraphEvidence(value: unknown): GraphEvidence | null {
  if (!isRecord(value) || typeof value.id !== 'string' || value.id.length > 240 || typeof value.canonical_kind !== 'string' || value.canonical_kind.length > 80 || typeof value.canonical_id !== 'string' || value.canonical_id.length > 240 || typeof value.canonical_fingerprint !== 'string' || value.canonical_fingerprint.length > 240) return null
  const occurrenceCount = finiteNumber(value.occurrence_count)
  if (occurrenceCount === undefined) return null
  return { id: value.id, canonical_kind: value.canonical_kind, canonical_id: value.canonical_id, canonical_fingerprint: value.canonical_fingerprint, locator: typeof value.locator === 'string' && value.locator.length <= 500 ? value.locator : undefined, occurrence_count: occurrenceCount }
}

function sanitizeGraphConflict(value: unknown): { seed: GraphPath['seed']; hop: GraphHop } | null {
  if (!isRecord(value)) return null
  const seed = sanitizeGraphSeed(value.seed)
  const hop = sanitizeGraphHop(value.hop)
  return seed && hop ? { seed, hop } : null
}

function sanitizeGraphCommunity(value: unknown): GraphCommunityContext | null {
  if (!isRecord(value) || typeof value.id !== 'string' || value.id.length > 240 || typeof value.trust !== 'string' || value.trust.length > 80 || typeof value.fresh !== 'boolean' || typeof value.title !== 'string' || value.title.length > 500 || typeof value.summary !== 'string' || value.summary.length > 3000 || !Array.isArray(value.evidence)) return null
  const level = finiteNumber(value.level); const rank = finiteNumber(value.rank); const sourceCount = finiteNumber(value.source_count); const unresolvedCount = finiteNumber(value.unresolved_count)
  if (level === undefined || rank === undefined || sourceCount === undefined || unresolvedCount === undefined) return null
  return {
    id: value.id, level, rank, trust: value.trust, fresh: value.fresh, source_count: sourceCount, unresolved_count: unresolvedCount,
    title: value.title, summary: value.summary,
    findings: Array.isArray(value.findings) ? value.findings.filter((finding): finding is string => typeof finding === 'string' && finding.length <= 800).slice(0, 30) : undefined,
    evidence: value.evidence.map(sanitizeGraphEvidence).filter((item): item is GraphEvidence => item !== null).slice(0, 40),
  }
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function sanitizeResult(value: unknown): KnowledgeResult | null {
  if (!isRecord(value) || typeof value.id !== 'string' || value.id.length > 240 || (value.kind !== 'memory' && value.kind !== 'source-evidence') || typeof value.workspaceId !== 'string' || typeof value.content !== 'string' || value.content.length > 30_000 || !Array.isArray(value.actions)) return null
  if ((typeof value.title === 'string' && value.title.length > 500) || (typeof value.provenance === 'string' && value.provenance.length > 2000) || (typeof value.explanation === 'string' && value.explanation.length > 2000)) return null
  const allowedActions = new Set(['open', 'pin', 'unpin', 'export', 'print', 'delete'])
  return {
    id: value.id, kind: value.kind, workspaceId: value.workspaceId,
    sourceId: typeof value.sourceId === 'string' && value.sourceId.length <= 240 ? value.sourceId : undefined,
    memoryType: typeof value.memoryType === 'string' && value.memoryType.length <= 80 ? value.memoryType : undefined,
    title: typeof value.title === 'string' ? value.title : undefined,
    content: value.content,
    provenance: typeof value.provenance === 'string' ? value.provenance : undefined,
    confidence: typeof value.confidence === 'number' && Number.isFinite(value.confidence) ? value.confidence : undefined,
    relevance: typeof value.relevance === 'number' && Number.isFinite(value.relevance) ? value.relevance : undefined,
    explanation: typeof value.explanation === 'string' ? value.explanation : undefined,
    updatedAt: typeof value.updatedAt === 'string' && value.updatedAt.length <= 80 ? value.updatedAt : undefined,
    pinned: typeof value.pinned === 'boolean' ? value.pinned : undefined,
    actions: value.actions.filter((action): action is KnowledgeResult['actions'][number] => typeof action === 'string' && allowedActions.has(action)),
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
