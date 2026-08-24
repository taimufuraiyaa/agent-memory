import {
  createNote as createStandaloneNote,
  deleteNotePermanently,
  deleteMemories as deleteStandaloneMemories,
  getStats,
  getSolutionEpisode as getStandaloneSolutionEpisode,
  getNote as getStandaloneNote,
  importLibraryBook,
  listFeedback,
  listNotes,
  listProjects,
  listRecentMemories,
  listSessions,
  listSolutionEpisodes,
  recallPreview,
  restoreNote as restoreStandaloneNote,
  reviewSolutionEpisode as reviewStandaloneSolutionEpisode,
  retryNoteIndex as retryStandaloneNoteIndex,
  searchMemories,
  setMemoryPinned,
  studyProject,
  submitRequestFeedback,
  trashNote as trashStandaloneNote,
  updateNote as updateStandaloneNote,
  type MemoryEntry,
  type RightsBasis,
} from '../api'
import {
  type ActivityItem,
  type AskResponse,
  type CursorPage,
  type KnowledgeGateway,
  type KnowledgeResult,
  type NoteSummary,
  type SourceSummary,
  type StudyResult,
  type WorkspaceSummary,
  type WorkspaceNote,
} from '../knowledgeGateway'
import { solutionActivityItem, solutionDetail } from './solutionEpisodeAdapter'

function memoryResult(memory: MemoryEntry, score?: number, explanation?: string): KnowledgeResult {
  return {
    id: memory.id,
    kind: 'memory',
    workspaceId: memory.workspace,
    memoryType: memory.type,
    content: memory.content,
    provenance: memory.source?.file_path || memory.source?.note_path || memory.source?.type,
    confidence: memory.confidence,
    relevance: score ?? memory.score,
    explanation: explanation || memory.match_reason,
    updatedAt: memory.updated_at,
    pinned: memory.pinned,
    actions: ['open', memory.pinned ? 'unpin' : 'pin', 'export', 'print', 'delete'],
  }
}

function numericCursor(cursor?: string): number {
  const value = Number.parseInt(cursor || '0', 10)
  return Number.isFinite(value) && value >= 0 ? value : 0
}

function sourceFormat(file: File): 'pdf' | 'epub' | 'markdown' | 'text' {
  const extension = file.name.toLowerCase().split('.').pop()
  if (extension === 'pdf' || extension === 'epub') return extension
  if (extension === 'md' || extension === 'markdown') return 'markdown'
  if (extension === 'txt') return 'text'
  throw new Error('Choose a PDF, EPUB, Markdown, or plain-text file.')
}

function rightsBasis(value: 'author-owned' | 'licensed' | 'public-domain-or-open' | 'lawfully-acquired-private-use'): RightsBasis {
  if (value === 'author-owned') return 'author_owned'
  if (value === 'public-domain-or-open') return 'public_domain'
  if (value === 'lawfully-acquired-private-use') return 'lawfully_acquired_private_use'
  return 'licensed'
}

function workspaceNote(note: import('../api').NoteDocument): WorkspaceNote {
  return { id: note.id, workspaceId: note.workspace, title: note.title, path: note.path, updatedAt: note.updated_at, deleted: Boolean(note.deleted_at), body: note.body, properties: note.properties, revision: note.revision, indexState: note.index_state, indexError: note.index_error }
}

export function createStandaloneKnowledgeGateway(): KnowledgeGateway {
  const runtimeActivity: ActivityItem[] = []
  const recordActivity = (item: Omit<ActivityItem, 'updatedAt'>) => runtimeActivity.unshift({ ...item, updatedAt: new Date().toISOString() })
  return {
    runtime: 'standalone',
    capabilities: new Set(['workspace', 'ask', 'search', 'browse', 'source', 'study', 'note', 'activity', 'settings']),
    async listWorkspaces() {
      const response = await listProjects()
      return response.projects.map<WorkspaceSummary>((project) => ({
        id: project.name,
        name: project.name,
        kind: 'registered-project',
        memoryCount: project.memory_count,
        sourceCount: project.workspace_root ? 1 : 0,
        noteCount: 0,
        latestActivity: project.last_activity,
        connectionState: 'connected',
        capabilities: ['workspace', 'ask', 'search', 'browse', 'source', 'study', 'note', 'activity', 'settings'],
      }))
    },
    async ask(scope, question) {
      const response = await recallPreview({ workspace: scope.workspaceId, task_description: question, top_k: 50, token_budget: 4000, explain: true, include_memories: true })
      const inSource = (memory: MemoryEntry) => !scope.sourceId || (scope.sourceId.startsWith('codebase:') ? !memory.source?.note_id : memory.source?.note_id === scope.sourceId)
      const durableMemory = (response.memories_included_full || []).filter(inSource).map((memory) => memoryResult(memory))
      const weakContext = (response.weak_memories || []).filter(inSource).map((memory) => memoryResult(memory))
      return {
        answerable: durableMemory.length > 0,
        answer: durableMemory.length ? (scope.sourceId ? durableMemory.map((memory) => memory.content).join('\n\n') : response.context_block) : undefined,
        sourceEvidence: [],
        durableMemory,
        weakContext,
        unavailableReason: durableMemory.length ? undefined : 'No grounded durable memory was found in this workspace.',
      } satisfies AskResponse
    },
    async search(scope, query, cursor) {
      const offset = numericCursor(cursor)
      const limit = 20
      const response = await searchMemories({ workspace: scope.workspaceId, query, top_k: offset + limit + 1, explain: true, filters: { types: [], tiers: [] } })
      const page = (response.results || []).slice(offset, offset + limit)
      return { items: page.map((memory) => memoryResult(memory)), nextCursor: response.results.length > offset + limit ? String(offset + limit) : undefined }
    },
    async browse(scope, mode, cursor) {
      const offset = numericCursor(cursor)
      const limit = 20
      const response = await listRecentMemories({ workspace: scope.workspaceId, limit: Math.min(200, offset + limit + 1) })
      const filtered = mode === 'pinned' ? response.results.filter((memory) => memory.pinned) : response.results
      const page = filtered.slice(offset, offset + limit)
      return { items: page.map((memory) => memoryResult(memory)), nextCursor: filtered.length > offset + limit ? String(offset + limit) : undefined }
    },
    async getMemory(scope, memoryId) {
      const response = await listRecentMemories({ workspace: scope.workspaceId, limit: 200 })
      const memory = response.results.find((item) => item.id === memoryId)
      if (!memory) throw new Error('Memory was not found in this workspace.')
      return memoryResult(memory)
    },
    async setMemoryPinned(scope, memoryId, pinned) {
      await setMemoryPinned({ workspace: scope.workspaceId, memory_id: memoryId, pinned })
    },
    async deleteMemories(scope, memoryIds) {
      await deleteStandaloneMemories({ workspace: scope.workspaceId, memory_ids: memoryIds })
    },
    async listSources(scope) {
      const [projectsResponse, notesResponse] = await Promise.all([listProjects(), listNotes({ workspace: scope.workspaceId, include_deleted: false })])
      const project = projectsResponse.projects.find((item) => item.name === scope.workspaceId)
      const sources: SourceSummary[] = project ? [{ id: `codebase:${project.name}`, workspaceId: project.name, kind: 'codebase', title: project.name, state: 'registered', statusLabel: 'Registered project', updatedAt: project.last_activity }] : []
      for (const note of notesResponse.notes) {
        const libraryFormat = String(note.properties?.library_format || '')
        sources.push({
          id: note.id,
          workspaceId: scope.workspaceId,
          kind: libraryFormat ? 'document' : 'note',
          title: note.title,
          format: ['pdf', 'epub', 'markdown', 'text'].includes(libraryFormat) ? libraryFormat as SourceSummary['format'] : undefined,
          state: note.index_state === 'failed' ? 'failed' : note.index_state === 'ready' ? 'ready' : 'indexing',
          statusLabel: note.index_state,
          updatedAt: note.updated_at,
          failure: note.index_error ? { message: note.index_error, retryAllowed: true } : undefined,
        })
      }
      return sources
    },
    async uploadSource(input) {
      const format = sourceFormat(input.file)
      const title = input.file.name.replace(/\.[^.]+$/, '') || input.file.name
      const job = await importLibraryBook({ workspace: input.scope.workspaceId, title, edition_label: 'Imported document', language: 'en', format, source_file: input.file, rights_basis: rightsBasis(input.rightsBasis) })
      if (format === 'pdf' && job.state === 'failed' && /OCR is required/i.test(job.error || '')) {
        recordActivity({ id: `upload:${job.id}`, workspaceId: input.scope.workspaceId, kind: 'upload', title: title, state: 'failed', failure: { message: 'OCR is required before this PDF can be indexed.', retryAllowed: false } })
        return { id: job.id, workspaceId: input.scope.workspaceId, kind: 'document', title, format, state: 'ocr-required', statusLabel: 'OCR required', failure: { message: 'This PDF has no searchable text layer. Run OCR, then retry with the searchable copy.', retryAllowed: false } }
      }
      if (job.state === 'failed' || !job.result) throw new Error(job.error || 'The document could not be prepared.')
      if (job.result.existing) {
        const existing = (await listNotes({ workspace: input.scope.workspaceId, include_deleted: false })).notes.find((note) => note.properties?.library_asset_id === job.result?.asset_id)
        if (existing) {
          recordActivity({ id: `upload:${job.id}`, workspaceId: input.scope.workspaceId, kind: 'upload', title: `${existing.title} · already imported`, state: 'completed' })
          return { id: existing.id, workspaceId: input.scope.workspaceId, kind: 'document', title: existing.title, format, state: existing.index_state === 'ready' ? 'ready' : existing.index_state === 'failed' ? 'failed' : 'indexing', statusLabel: 'Already imported', updatedAt: existing.updated_at }
        }
      }
      const note = await createStandaloneNote({
        workspace: input.scope.workspaceId,
        path: `library/${title}.md`,
        title,
        body: `# ${title}\n\nImported ${format.toUpperCase()} source.`,
        properties: { source: 'library import', library_work_id: job.result.work_id, library_edition_id: job.result.edition_id, library_asset_id: job.result.asset_id, library_format: job.result.format },
      })
      recordActivity({ id: `upload:${job.id}`, workspaceId: input.scope.workspaceId, kind: 'upload', title, state: 'completed' })
      recordActivity({ id: `indexing:${note.note.id}`, workspaceId: input.scope.workspaceId, kind: 'indexing', title: `Index ${title}`, state: note.note.index_state === 'failed' ? 'failed' : note.note.index_state === 'ready' ? 'completed' : 'running', failure: note.note.index_error ? { message: note.note.index_error, retryAllowed: true } : undefined })
      return { id: note.note.id, workspaceId: input.scope.workspaceId, kind: 'document', title, format, state: note.note.index_state === 'ready' ? 'ready' : 'indexing', statusLabel: note.note.index_state, updatedAt: note.note.updated_at }
    },
    async retrySource() {
      throw new Error('Retry this source from its note indexing action.')
    },
    async deleteSource(scope, sourceId) {
      if (sourceId.startsWith('codebase:')) throw new Error('Registered codebases are removed from the project registry, not Sources.')
      await trashStandaloneNote({ workspace: scope.workspaceId, note_id: sourceId })
      recordActivity({ id: `deletion:source:${sourceId}`, workspaceId: scope.workspaceId, kind: 'deletion', title: 'Move source to Trash', state: 'completed' })
    },
    async study(input) {
      const response = await studyProject({ workspace: input.workspaceId, depth: input.depth, dry_run: input.preview, max_files: input.maxFiles, offset: input.offset })
      recordActivity({ id: `study:${input.workspaceId}:${input.offset}:${Date.now()}`, workspaceId: input.workspaceId, kind: 'study', title: `${input.preview ? 'Preview' : 'Study'} files ${input.offset + 1}–${response.next_offset || input.offset + input.maxFiles}`, state: response.errors?.length && !response.extracted ? 'failed' : 'completed', failure: response.errors?.length && !response.extracted ? { message: `${response.errors.length} files could not be studied.`, retryAllowed: response.has_more } : undefined })
      return { preview: response.dry_run, scannedFiles: response.scanned_files, extracted: response.extracted, skipped: response.skipped, writtenIds: response.written_ids || [], errors: response.errors || [], offset: response.offset, pageFiles: response.page_files, nextOffset: response.next_offset, hasMore: response.has_more } satisfies StudyResult
    },
    async listNotes(scope, includeDeleted) {
      const response = await listNotes({ workspace: scope.workspaceId, include_deleted: includeDeleted })
      return response.notes.map<NoteSummary>((note) => ({ id: note.id, workspaceId: note.workspace, title: note.title, path: note.path, updatedAt: note.updated_at, deleted: Boolean(note.deleted_at) }))
    },
    async getNote(scope, noteId) {
      return workspaceNote((await getStandaloneNote({ workspace: scope.workspaceId, note_id: noteId })).note)
    },
    async createNote(scope, input) {
      const note = workspaceNote((await createStandaloneNote({ workspace: scope.workspaceId, ...input })).note)
      recordActivity({ id: `indexing:${note.id}`, workspaceId: scope.workspaceId, kind: 'indexing', title: `Index ${note.title}`, state: note.indexState === 'failed' ? 'failed' : note.indexState === 'ready' ? 'completed' : 'running', failure: note.indexError ? { message: note.indexError, retryAllowed: true } : undefined })
      return note
    },
    async updateNote(scope, note) {
      return workspaceNote((await updateStandaloneNote({ workspace: scope.workspaceId, note_id: note.id, expected_revision: note.revision, path: note.path, title: note.title, body: note.body, properties: note.properties })).note)
    },
    async trashNote(scope, noteId) {
      await trashStandaloneNote({ workspace: scope.workspaceId, note_id: noteId })
      recordActivity({ id: `deletion:trash:${noteId}`, workspaceId: scope.workspaceId, kind: 'deletion', title: 'Move note to Trash', state: 'completed' })
    },
    async restoreNote(scope, noteId) {
      await restoreStandaloneNote({ workspace: scope.workspaceId, note_id: noteId })
    },
    async deleteNote(scope, noteId) {
      await deleteNotePermanently({ workspace: scope.workspaceId, note_id: noteId })
      recordActivity({ id: `deletion:permanent:${noteId}`, workspaceId: scope.workspaceId, kind: 'deletion', title: 'Delete note permanently', state: 'completed' })
    },
    async retryNoteIndex(scope, noteId) {
      await retryStandaloneNoteIndex({ workspace: scope.workspaceId, note_id: noteId })
    },
    async listActivity(scope, cursor) {
	  const [sessionsResponse, feedback, solutions] = await Promise.all([listSessions({ workspace: scope.workspaceId, limit: 100 }), listFeedback({ workspace: scope.workspaceId }), listSolutionEpisodes({ workspace: scope.workspaceId, limit: 100 })])
      const items: ActivityItem[] = [
        ...runtimeActivity.filter((item) => item.workspaceId === scope.workspaceId),
		...solutions.episodes.map(solutionActivityItem),
        ...sessionsResponse.sessions.map((session) => ({ id: `session:${session.session_id}`, workspaceId: scope.workspaceId, kind: 'session' as const, title: `Agent session ${session.session_id}`, state: session.ended_at ? 'completed' as const : 'running' as const, updatedAt: session.last_seen_at })),
        ...feedback.map((request) => ({
          id: `retrieval:${request.id}`,
          workspaceId: scope.workspaceId,
          kind: request.score >= 0 ? 'feedback' as const : 'retrieval' as const,
          title: request.query,
          state: 'completed' as const,
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
        })),
      ].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
      const offset = numericCursor(cursor)
      const page = items.slice(offset, offset + 20)
      return { items: page, nextCursor: items.length > offset + 20 ? String(offset + 20) : undefined }
    },
    async retryActivity() {
      throw new Error('This activity cannot be retried from the unified timeline.')
    },
    async getSolutionEpisode(scope, episodeId) {
      return solutionDetail((await getStandaloneSolutionEpisode({ workspace: scope.workspaceId, episode_id: episodeId })).detail)
    },
    async reviewSolutionEpisode(scope, input) {
      await reviewStandaloneSolutionEpisode({ workspace: scope.workspaceId, principal_id: input.principalId, episode_id: input.episodeId, action: input.action,
        step_id: input.stepId, reason: input.reason, reason_class: input.reasonClass, summary: input.summary,
        successor_episode_id: input.successorEpisodeId, idempotency_key: input.idempotencyKey, pinned: input.pinned })
    },
    async submitFeedback(scope, requestId, score, reason) {
      await submitRequestFeedback({ workspace: scope.workspaceId, request_id: requestId, score, reason })
    },
    async getSettings(scope) {
      return getStats(scope.workspaceId)
    },
    async importMigration() {
      throw new Error('Standalone workspaces export migration copies from System > Migration.')
    },
  }
}
