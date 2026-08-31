import {
  browseHostedProjectMemories,
  createHostedClientProfile,
  deleteHostedSource,
  deleteHostedClientProfile,
  getHostedProjectLifecycle,
  getHostedBilling,
  getHostedPrivacy,
  getHostedGraphReadiness,
  getHostedGraphSnapshot,
  getHostedGraphStatus,
  getHostedProjectMemory,
  getHostedProjectSolution,
  importHostedBundle,
  listHostedProcessingTasks,
  listHostedClientProfiles,
  listHostedProjects,
  listHostedRetrievalFeedback,
  listHostedProjectSolutions,
  listHostedProjectSkills,
  listHostedSkillLifecycle,
  inspectHostedSkillLifecycle,
  operateHostedSkillLifecycle,
  getHostedSkillOrchestration,
  controlHostedSkillOrchestration,
  listHostedSources,
  queryHostedSources,
  recallHostedGraph,
  operateHostedGraph,
  retryHostedSource,
  reviewHostedProjectSolution,
  reviewHostedGraph,
  searchHostedMemories,
  searchHostedProjectMemories,
  studyHostedProject,
  submitHostedRetrievalFeedback,
  submitHostedGraphFeedback,
  uploadHostedSource,
  updateHostedClientProfile,
  type HostedConnection,
  type HostedMemory,
  type HostedProject,
  type HostedRightsBasis,
  type HostedSource,
} from '../hostedApi'
import type {
  ActivityFilter,
  ActivityItem,
  AskResponse,
  KnowledgeCapability,
  KnowledgeGateway,
  KnowledgeResult,
  SourceSummary,
  WorkspaceSummary,
} from '../knowledgeGateway'
import { ACTIVITY_PAGE_SIZE } from '../knowledgeGateway'
import { solutionActivityItem, solutionDetail } from './solutionEpisodeAdapter'

function numericCursor(cursor?: string): number {
  const value = Number.parseInt(cursor || '0', 10)
  return Number.isFinite(value) && value >= 0 ? value : 0
}

function activityPage(items: ActivityItem[], cursor?: string, filter: ActivityFilter = 'all') {
  const filteredItems = filter === 'all' ? items : items.filter((item) => item.kind === filter)
  const offset = numericCursor(cursor)
  return {
    items: filteredItems.slice(offset, offset + ACTIVITY_PAGE_SIZE),
    nextCursor: filteredItems.length > offset + ACTIVITY_PAGE_SIZE ? String(offset + ACTIVITY_PAGE_SIZE) : undefined,
  }
}

function hostedMemoryResult(memory: HostedMemory, workspaceId: string, score?: number, explanation?: string): KnowledgeResult {
  return {
    id: memory.id,
    kind: 'memory',
    workspaceId,
    memoryType: memory.type,
    content: memory.content,
    provenance: memory.source?.file_path || memory.source?.note_path || memory.source?.type || memory.source_kind,
    confidence: memory.confidence,
    relevance: score ?? memory.score,
    explanation: explanation || memory.match_reason,
    updatedAt: memory.updated_at,
    pinned: memory.pinned,
    actions: ['open'],
  }
}

function sourceState(source: HostedSource): SourceSummary['state'] {
  if (source.state === 'ready') return 'ready'
  if (source.state === 'failed' || source.state === 'rejected' || source.state === 'disabled') return 'failed'
  if (source.state.includes('ocr')) return source.state.includes('required') ? 'ocr-required' : 'ocr-processing'
  if (source.state === 'uploading' || source.state === 'uploaded') return 'uploading'
  if (source.state === 'parsing') return 'parsing'
  return 'indexing'
}

function sourceSummary(source: HostedSource): SourceSummary {
  const format = source.media_type === 'application/pdf' ? 'pdf' : source.media_type === 'application/epub+zip' ? 'epub' : source.media_type === 'text/markdown' ? 'markdown' : source.media_type === 'text/plain' ? 'text' : undefined
  return {
    id: source.id,
    workspaceId: source.workspace_id,
    kind: 'document',
    title: source.filename,
    format,
    state: sourceState(source),
    statusLabel: source.progress?.stage || source.state,
    failure: source.failure ? { message: source.failure.code || 'Source processing failed.', retryAllowed: Boolean(source.failure.retry_allowed) } : undefined,
  }
}

function hostedRightsBasis(value: 'author-owned' | 'licensed' | 'public-domain-or-open' | 'lawfully-acquired-private-use'): HostedRightsBasis {
  if (value === 'author-owned') return 'author_owned'
  if (value === 'public-domain-or-open') return 'public_domain_or_open'
  if (value === 'lawfully-acquired-private-use') return 'lawfully_acquired_private_use'
  return 'licensed'
}

export function createHostedKnowledgeGateway(connection: HostedConnection, options: { localOwner?: boolean; localSystemTools?: boolean } = {}): KnowledgeGateway {
  let registeredProjects = new Map<string, HostedProject>()
  const isRegisteredProject = (workspaceId: string) => registeredProjects.has(workspaceId)
  const localOwner = Boolean(options.localOwner)
  const localSystemTools = Boolean(localOwner && options.localSystemTools)
  const capabilities = new Set<KnowledgeCapability>(['workspace', 'ask', 'search', 'browse', 'source', 'study', 'activity', 'settings', 'graph', ...(localSystemTools ? ['lifecycle', 'clients', 'skills'] as const : [])])

  return {
    runtime: 'hosted',
    capabilities,
    supports(capability, scope) {
      if (!capabilities.has(capability)) return false
      if (capability === 'clients') return localSystemTools
      if (capability === 'lifecycle' || capability === 'skills') return localSystemTools && isRegisteredProject(scope.workspaceId)
      if (capability === 'graph') return !isRegisteredProject(scope.workspaceId)
      return true
    },
    async listWorkspaces() {
      const response = await listHostedProjects(connection).catch(() => ({ projects: [] }))
      registeredProjects = new Map(response.projects.map((project) => [project.name, project]))
      const projectWorkspaces = response.projects.map<WorkspaceSummary>((project) => ({
        id: project.name,
        name: project.name,
        kind: 'registered-project',
        memoryCount: project.memory_count,
        sourceCount: project.workspace_root ? 1 : 0,
        noteCount: 0,
        latestActivity: project.last_activity,
        connectionState: 'connected',
        capabilities: ['workspace', 'ask', 'search', 'browse', 'source', 'study', 'activity', 'settings'],
      }))
      if (registeredProjects.has(connection.workspace)) return projectWorkspaces
      return [{ id: connection.workspace, name: 'Hosted workspace', kind: 'hosted', memoryCount: 0, sourceCount: 0, noteCount: 0, connectionState: 'connected', capabilities: ['workspace', 'ask', 'search', 'source', 'activity', 'settings'] }, ...projectWorkspaces]
    },
    async ask(scope, question, options = { mode: 'basic' }, signal) {
      if (isRegisteredProject(scope.workspaceId)) {
        if (options.mode !== 'basic') throw new Error('Graph Ask is available for hosted workspaces; this registered project uses its Basic memory index.')
        const response = await searchHostedProjectMemories(connection, { workspace: scope.workspaceId, query: question, limit: 12 })
        const durableMemory = response.items.map((item) => hostedMemoryResult(item.memory, scope.workspaceId, item.score, item.explanation))
        return { answerable: durableMemory.length > 0, answer: durableMemory.length ? durableMemory.map((item) => item.content).join('\n\n') : undefined, sourceEvidence: [], durableMemory, weakContext: [], unavailableReason: durableMemory.length ? undefined : 'No grounded durable memory was found in this project.' } satisfies AskResponse
      }
      const scopedConnection = { ...connection, workspace: scope.workspaceId }
      if (options.mode !== 'basic') {
        const response = await recallHostedGraph(scopedConnection, question, options, signal)
        const durableMemory = response.basic_memories.map((memory) => hostedMemoryResult(memory, scope.workspaceId))
        const directIDs = new Set(durableMemory.map((memory) => memory.id))
        const weakContext = (response.canonical_memories || [])
          .filter((memory) => !directIDs.has(memory.id))
          .map((memory) => hostedMemoryResult(memory, scope.workspaceId, undefined, 'Canonical memory connected through the active graph index.'))
        const answerContext = [...durableMemory, ...weakContext]
        return {
          requestId: response.request_id,
          answerable: answerContext.length > 0,
          answer: answerContext.length ? answerContext.map((item) => item.content).join('\n\n') : undefined,
          sourceEvidence: [],
          durableMemory,
          weakContext,
          unavailableReason: answerContext.length ? undefined : 'No grounded durable memory or graph-connected canonical memory was found.',
          graphRoute: response.graph_route,
          graphContext: response.graph_context,
        } satisfies AskResponse
      }
      const sources = await listHostedSources(scopedConnection)
      const sourceIDs = scope.sourceId ? sources.filter((source) => source.id === scope.sourceId).map((source) => source.id) : sources.filter((source) => source.state === 'ready').map((source) => source.id)
      const [memoryResult, evidenceResult] = await Promise.allSettled([
        searchHostedMemories(scopedConnection, question),
        sourceIDs.length ? queryHostedSources(scopedConnection, sourceIDs, question, 8, 0) : Promise.resolve({ answerable: false, evidence_available: false, synthesis: undefined, evidence: [], pagination: { offset: 0, limit: 8, has_more: false } }),
      ])
      const durableMemory = memoryResult.status === 'fulfilled' ? memoryResult.value.items.map((memory) => hostedMemoryResult(memory, scope.workspaceId)) : []
      const sourceEvidence: KnowledgeResult[] = evidenceResult.status === 'fulfilled' ? evidenceResult.value.evidence.map((item) => ({ id: item.citation_id || item.passage_id, kind: 'source-evidence', workspaceId: scope.workspaceId, sourceId: item.source_id, content: item.text, provenance: item.locator?.display, relevance: item.relevance_score, actions: ['open'] })) : []
      const synthesis = evidenceResult.status === 'fulfilled' ? evidenceResult.value.synthesis : undefined
      return { answerable: Boolean(sourceEvidence.length || durableMemory.length), answer: synthesis || (durableMemory.length ? durableMemory.map((item) => item.content).join('\n\n') : undefined), sourceEvidence, durableMemory, weakContext: [], unavailableReason: sourceEvidence.length || durableMemory.length ? undefined : 'No grounded source evidence or durable memory was found.' }
    },
    async translateAnswer() { throw new Error('Local translation is unavailable in this runtime.') },
    async getTranslationStatus() { throw new Error('Local translation is unavailable in this runtime.') },
    async testTranslationSettings() { throw new Error('Local translation is unavailable in this runtime.') },
    async saveTranslationSettings() { throw new Error('Local translation is unavailable in this runtime.') },
    async search(scope, query, cursor) {
      if (isRegisteredProject(scope.workspaceId)) {
        const response = await searchHostedProjectMemories(connection, { workspace: scope.workspaceId, query, limit: 20, cursor })
        return { items: response.items.map((item) => hostedMemoryResult(item.memory, scope.workspaceId, item.score, item.explanation)), nextCursor: response.next_cursor }
      }
      const response = await searchHostedMemories({ ...connection, workspace: scope.workspaceId }, query)
      return { items: response.items.map((memory) => hostedMemoryResult(memory, scope.workspaceId)), nextCursor: response.next_cursor }
    },
    async browse(scope, mode, cursor) {
      if (!isRegisteredProject(scope.workspaceId)) return { items: [] }
      const response = await browseHostedProjectMemories(connection, { workspace: scope.workspaceId, mode, limit: 20, cursor })
      return { items: response.items.map((memory) => hostedMemoryResult(memory, scope.workspaceId)), nextCursor: response.next_cursor }
    },
    async getMemory(scope, memoryId) {
      if (!isRegisteredProject(scope.workspaceId)) throw new Error('Hosted memory detail is unavailable in this runtime.')
      return hostedMemoryResult(await getHostedProjectMemory(connection, scope.workspaceId, memoryId), scope.workspaceId)
    },
    async setMemoryPinned() { throw new Error('Pinning is unavailable in this runtime.') },
    async deleteMemories() { throw new Error('Memory deletion is unavailable in this runtime.') },
    async listSources(scope) {
      const project = registeredProjects.get(scope.workspaceId)
      if (project) return [{ id: `codebase:${project.name}`, workspaceId: project.name, kind: 'codebase', title: project.name, state: 'registered', statusLabel: 'Registered project', updatedAt: project.last_activity }]
      return (await listHostedSources({ ...connection, workspace: scope.workspaceId })).map(sourceSummary)
    },
    async uploadSource(input) {
      if (isRegisteredProject(input.scope.workspaceId)) throw new Error('Documents belong to a hosted workspace; this registered project accepts codebase study only.')
      const scopedConnection = { ...connection, workspace: input.scope.workspaceId }
      const result = await uploadHostedSource(scopedConnection, input.file, hostedRightsBasis(input.rightsBasis))
      const source = (await listHostedSources(scopedConnection)).find((item) => item.id === result.source_id)
      return source ? sourceSummary(source) : { id: result.source_id, workspaceId: input.scope.workspaceId, kind: 'document', title: input.file.name, format: input.format, state: 'uploading', statusLabel: 'Upload accepted' }
    },
    async retrySource(scope, sourceId) {
      if (isRegisteredProject(scope.workspaceId)) throw new Error('Codebase study retries use Continue study.')
      return sourceSummary(await retryHostedSource({ ...connection, workspace: scope.workspaceId }, sourceId))
    },
    async deleteSource(scope, sourceId) {
      if (isRegisteredProject(scope.workspaceId)) throw new Error('Registered codebases are removed from the project registry, not Sources.')
      await deleteHostedSource({ ...connection, workspace: scope.workspaceId }, sourceId)
    },
    async study(input) {
      if (!isRegisteredProject(input.workspaceId)) throw new Error('Study is available only for registered project workspaces.')
      const response = await studyHostedProject(connection, { workspace: input.workspaceId, depth: input.depth, dry_run: input.preview, max_files: input.maxFiles, offset: input.offset })
      return { preview: response.dry_run, scannedFiles: response.scanned_files, extracted: response.extracted, skipped: response.skipped, writtenIds: response.written_ids || [], errors: response.errors || [], offset: response.offset, pageFiles: response.page_files, nextOffset: response.next_offset, hasMore: response.has_more }
    },
    async listNotes() { return [] },
    async getNote() { throw new Error('Notes are unavailable in this runtime.') },
    async createNote() { throw new Error('Notes are unavailable in this runtime.') },
    async updateNote() { throw new Error('Notes are unavailable in this runtime.') },
    async trashNote() { throw new Error('Notes are unavailable in this runtime.') },
    async restoreNote() { throw new Error('Notes are unavailable in this runtime.') },
    async deleteNote() { throw new Error('Notes are unavailable in this runtime.') },
    async retryNoteIndex() { throw new Error('Notes are unavailable in this runtime.') },
    async listActivity(scope, cursor, filter: ActivityFilter = 'all') {
      if (isRegisteredProject(scope.workspaceId)) {
        const [response, solutions] = await Promise.all([listHostedRetrievalFeedback(connection, scope.workspaceId), listHostedProjectSolutions(connection, scope.workspaceId)])
        const items = [...solutions.episodes.map(solutionActivityItem), ...(response.feedback ?? []).map<ActivityItem>((request) => ({
          id: request.id,
          workspaceId: scope.workspaceId,
          kind: request.score >= 0 ? 'feedback' : 'retrieval',
          title: request.query,
          state: 'completed',
          updatedAt: request.created_at,
          feedback: {
            requestId: request.id,
            requestType: request.request_type,
            query: request.query,
            score: request.score,
            reason: request.reason,
            usefulCount: request.useful_count,
            totalCount: request.total_count,
          },
        }))].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
        return activityPage(items, cursor, filter)
      }
      const tasks = await listHostedProcessingTasks({ ...connection, workspace: scope.workspaceId })
      const items = tasks.map<ActivityItem>((task) => ({ id: task.id, workspaceId: scope.workspaceId, kind: task.kind === 'source_deletion' ? 'deletion' : 'upload', title: task.title, state: task.state, updatedAt: task.updated_at, progress: task.progress.percent, failure: task.failure ? { message: task.failure.message || task.failure.code || 'Task failed.', retryAllowed: task.failure.retry_allowed } : undefined }))
      return activityPage(items, cursor, filter)
    },
    async retryActivity(scope, activityId) {
      if (isRegisteredProject(scope.workspaceId)) throw new Error('This retrieval activity cannot be retried.')
      await retryHostedSource({ ...connection, workspace: scope.workspaceId }, activityId)
    },
    async listHowHistory(scope) {
      if (!isRegisteredProject(scope.workspaceId)) return []
      const response = await listHostedProjectSolutions(connection, scope.workspaceId)
      return response.episodes.map((record) => solutionActivityItem(record).episode!)
    },
    async getSolutionEpisode(scope, episodeId) {
	  if (!isRegisteredProject(scope.workspaceId)) throw new Error('Solution episodes are available only for registered projects.')
	  return solutionDetail((await getHostedProjectSolution(connection, scope.workspaceId, episodeId)).detail)
    },
    async reviewSolutionEpisode(scope, input) {
	  if (!isRegisteredProject(scope.workspaceId)) throw new Error('Solution episode review is available only for registered projects.')
	  await reviewHostedProjectSolution(connection, { workspace: scope.workspaceId, episode_id: input.episodeId, action: input.action,
		step_id: input.stepId, reason: input.reason, reason_class: input.reasonClass, summary: input.summary,
		successor_episode_id: input.successorEpisodeId, idempotency_key: input.idempotencyKey, pinned: input.pinned })
    },
    async submitFeedback(scope, requestId, score, reason) {
      if (!isRegisteredProject(scope.workspaceId)) throw new Error('Hosted feedback uses the memory feedback control.')
      await submitHostedRetrievalFeedback(connection, { workspace: scope.workspaceId, request_id: requestId, score, reason })
    },
    async listLifecycle(scope) {
      if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Lifecycle is available only for registered projects in a private installation.')
      return getHostedProjectLifecycle(connection, scope.workspaceId)
    },
    async listSkills(scope) {
      if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Skills are available only for registered projects in a private installation.')
      return (await listHostedProjectSkills(connection, scope.workspaceId)).skills || []
    },
    async listSkillLifecycle(scope) { if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Skill lifecycle is available only for registered projects.'); return (await listHostedSkillLifecycle(connection, scope.workspaceId)).skills || [] },
    async inspectSkillLifecycle(scope, skillId, environment) { if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Skill lifecycle is available only for registered projects.'); return inspectHostedSkillLifecycle(connection, scope.workspaceId, skillId, environment) },
    async operateSkillLifecycle(scope, _actor, operation, payload) { if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Skill lifecycle is available only for registered projects.'); return operateHostedSkillLifecycle(connection, scope.workspaceId, operation, payload) },
    async getSkillOrchestration(scope, skillId, _actor, signal) { if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Skill orchestration is available only for registered projects.'); return getHostedSkillOrchestration(connection, scope.workspaceId, skillId, signal) },
    async controlSkillOrchestration(scope, _actor, input) { if (!localSystemTools || !isRegisteredProject(scope.workspaceId)) throw new Error('Skill orchestration is available only for registered projects.'); return controlHostedSkillOrchestration(connection, scope.workspaceId, input) },
    async listClientProfiles() {
      if (!localSystemTools) throw new Error('Client profiles are available only in a private installation.')
      return listHostedClientProfiles(connection)
    },
    async createClientProfile(input) {
      if (!localSystemTools) throw new Error('Client profiles are available only in a private installation.')
      return createHostedClientProfile(connection, input)
    },
    async updateClientProfile(input) {
      if (!localSystemTools) throw new Error('Client profiles are available only in a private installation.')
      return updateHostedClientProfile(connection, input)
    },
    async deleteClientProfile(input) {
      if (!localSystemTools) throw new Error('Client profiles are available only in a private installation.')
      return deleteHostedClientProfile(connection, input)
    },
    async getSettings(scope) {
      if (isRegisteredProject(scope.workspaceId)) return { workspace: scope.workspaceId, kind: 'registered-project' }
      const scoped = { ...connection, workspace: scope.workspaceId }
      const [privacy, billing] = await Promise.all([getHostedPrivacy(scoped), getHostedBilling(scoped)])
      return { privacy, billing }
    },
    async getGraphReadiness(scope, signal) { return getHostedGraphReadiness({ ...connection, workspace: scope.workspaceId }, signal) },
    async getGraphStatus(scope, signal) { return getHostedGraphStatus({ ...connection, workspace: scope.workspaceId }, signal) },
    async getGraphSnapshot(scope, signal) { return getHostedGraphSnapshot({ ...connection, workspace: scope.workspaceId }, signal) },
    async operateGraph(scope, configurationId, action, expectedRevision, jobId) { return operateHostedGraph({ ...connection, workspace: scope.workspaceId }, configurationId, action, expectedRevision, jobId) },
    async reviewGraph(scope, input) { return reviewHostedGraph({ ...connection, workspace: scope.workspaceId }, input) },
    async submitGraphFeedback(scope, requestId, targetKind, targetId, outcome, reason) { return submitHostedGraphFeedback({ ...connection, workspace: scope.workspaceId }, requestId, targetKind, targetId, outcome, reason) },
    async importMigration(scope, file, passphrase, idempotencyKey) {
      if (isRegisteredProject(scope.workspaceId)) throw new Error('Migration import is available only for hosted workspaces.')
      const result = await importHostedBundle({ ...connection, workspace: scope.workspaceId }, file, passphrase, idempotencyKey)
      return { imported: result.report.imported.length, merged: result.report.merged.length, skipped: result.report.skipped.length, failed: result.report.failed.length }
    },
  }
}
