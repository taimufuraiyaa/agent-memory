import {
  browseHostedProjectMemories,
  deleteHostedSource,
  getHostedBilling,
  getHostedPrivacy,
  getHostedProjectMemory,
  importHostedBundle,
  listHostedProcessingTasks,
  listHostedProjects,
  listHostedRetrievalFeedback,
  listHostedSources,
  queryHostedSources,
  retryHostedSource,
  searchHostedMemories,
  searchHostedProjectMemories,
  studyHostedProject,
  submitHostedRetrievalFeedback,
  uploadHostedSource,
  type HostedConnection,
  type HostedMemory,
  type HostedProject,
  type HostedRightsBasis,
  type HostedSource,
} from '../hostedApi'
import type {
  ActivityItem,
  AskResponse,
  KnowledgeCapability,
  KnowledgeGateway,
  KnowledgeResult,
  SourceSummary,
  WorkspaceSummary,
} from '../knowledgeGateway'

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

export function createHostedKnowledgeGateway(connection: HostedConnection): KnowledgeGateway {
  let registeredProjects = new Map<string, HostedProject>()
  const isRegisteredProject = (workspaceId: string) => registeredProjects.has(workspaceId)

  return {
    runtime: 'hosted',
    capabilities: new Set<KnowledgeCapability>(['workspace', 'ask', 'search', 'browse', 'source', 'study', 'activity', 'settings']),
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
    async ask(scope, question) {
      if (isRegisteredProject(scope.workspaceId)) {
        const response = await searchHostedProjectMemories(connection, { workspace: scope.workspaceId, query: question, limit: 12 })
        const durableMemory = response.items.map((item) => hostedMemoryResult(item.memory, scope.workspaceId, item.score, item.explanation))
        return { answerable: durableMemory.length > 0, answer: durableMemory.length ? durableMemory.map((item) => item.content).join('\n\n') : undefined, sourceEvidence: [], durableMemory, weakContext: [], unavailableReason: durableMemory.length ? undefined : 'No grounded durable memory was found in this project.' } satisfies AskResponse
      }
      const scopedConnection = { ...connection, workspace: scope.workspaceId }
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
    async listActivity(scope) {
      if (isRegisteredProject(scope.workspaceId)) {
        const response = await listHostedRetrievalFeedback(connection, scope.workspaceId)
        return { items: response.feedback.map<ActivityItem>((request) => ({
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
        })) }
      }
      const tasks = await listHostedProcessingTasks({ ...connection, workspace: scope.workspaceId })
      return { items: tasks.map<ActivityItem>((task) => ({ id: task.id, workspaceId: scope.workspaceId, kind: task.kind === 'source_deletion' ? 'deletion' : 'upload', title: task.title, state: task.state, updatedAt: task.updated_at, progress: task.progress.percent, failure: task.failure ? { message: task.failure.message || task.failure.code || 'Task failed.', retryAllowed: task.failure.retry_allowed } : undefined })) }
    },
    async retryActivity(scope, activityId) {
      if (isRegisteredProject(scope.workspaceId)) throw new Error('This retrieval activity cannot be retried.')
      await retryHostedSource({ ...connection, workspace: scope.workspaceId }, activityId)
    },
    async submitFeedback(scope, requestId, score, reason) {
      if (!isRegisteredProject(scope.workspaceId)) throw new Error('Hosted feedback uses the memory feedback control.')
      await submitHostedRetrievalFeedback(connection, { workspace: scope.workspaceId, request_id: requestId, score, reason })
    },
    async getSettings(scope) {
      if (isRegisteredProject(scope.workspaceId)) return { workspace: scope.workspaceId, kind: 'registered-project' }
      const scoped = { ...connection, workspace: scope.workspaceId }
      const [privacy, billing] = await Promise.all([getHostedPrivacy(scoped), getHostedBilling(scoped)])
      return { privacy, billing }
    },
    async importMigration(scope, file, passphrase, idempotencyKey) {
      if (isRegisteredProject(scope.workspaceId)) throw new Error('Migration import is available only for hosted workspaces.')
      const result = await importHostedBundle({ ...connection, workspace: scope.workspaceId }, file, passphrase, idempotencyKey)
      return { imported: result.report.imported.length, merged: result.report.merged.length, skipped: result.report.skipped.length, failed: result.report.failed.length }
    },
  }
}
