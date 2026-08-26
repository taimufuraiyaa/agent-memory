export type HostedConnection = { token: string; tenant: string; workspace: string }

export type LocalSession = { state: 'loading' | 'signup_required' | 'authenticated'; tenant_id?: string; workspace_id?: string }

export type HostedSource = {
  id: string
  workspace_id: string
  filename: string
  media_type: string
  state: string
  progress?: { state?: string; stage?: string; percent?: number }
  failure?: { code?: string; retry_allowed?: boolean }
  retention_state?: string
}

export type HostedSourceStatus = Pick<HostedSource, 'id' | 'state' | 'progress' | 'failure'> & { updated_at: string }

export type HostedProcessingTask = {
  id: string
  kind: 'source_ingestion' | 'source_deletion'
  subject_id: string
  title: string
  state: 'queued' | 'running' | 'completed' | 'failed'
  progress: { state: string; label: string; percent: number }
  failure?: { code?: string; message?: string; action?: string; retry_allowed: boolean }
  created_at: string
  updated_at: string
}

export type HostedProject = {
  name: string
  db_path: string
  workspace_root?: string
  size_bytes: number
  memory_count: number
  last_activity: string
}

export type HostedProjectStudyResult = {
  sources_scanned: number
  scanned_files: number
  skipped: number
  extracted: number
  written_ids?: string[]
  errors?: Array<{ path: string; reason: string }>
  dry_run: boolean
	offset: number
	page_files: number
	next_offset: number
	has_more: boolean
}

export type HostedRetrievalRequest = {
  id: string
  workspace: string
  request_type: 'search' | 'recall'
  query: string
  score: number
  reason: string
  useful_count: number
  total_count: number
  created_at: string
}

export type HostedMemory = {
  id: string
  type: string
  content: string
  source_kind?: string
  workspace_id?: string
  updated_at?: string
  workspace?: string
  confidence?: number
  pinned?: boolean
  score?: number
  match_reason?: string
  source?: { file_path?: string; note_path?: string; type?: string }
}

export type HostedGraphRecallResponse = {
  request_id: string
  graph_route: GraphRouteDecision
  graph_context?: GraphRecallContext
  basic_memories: HostedMemory[]
  canonical_memories?: HostedMemory[]
}

export type HostedProjectMemoryResult = { memory: HostedMemory; score: number; explanation?: string }

export type HostedEvidence = {
  source_id: string
  source_version: number
  passage_id: string
  citation_id: string
  text: string
	locator?: { display?: string }
	reconstruction_strategy?: string
	included_passage_ids?: string[]
	included_citation_ids?: string[]
	answer_support?: boolean
	window_clipped?: boolean
	relevance_score?: number
}

export type HostedSemanticContext = {
	planner_used?: boolean
	reranker_used?: boolean
	plan_version?: string
	language?: string
	intent?: string
	subject?: string
	fallbacks?: string[]
}

export type HostedImportReport = {
  imported: Array<Record<string, unknown>>
  merged: Array<Record<string, unknown>>
  skipped: Array<Record<string, unknown>>
  failed: Array<Record<string, unknown>>
}

export type HostedImportResult = {
  id: string
  state: string
  duplicate: boolean
  report: HostedImportReport
}

export type HostedRightsBasis = 'author_owned' | 'licensed' | 'public_domain_or_open' | 'lawfully_acquired_private_use'

export type HostedRightsAttestationStatus = {
  status: 'required' | 'active' | 'expired'
  reason: 'missing' | 'active' | 'expired' | 'policy_changed'
  policy: {
    version: string
    effective_at: string
    renewal_days: number
    primary_confirmation: string
    statement_digest: string
    statements: Array<{ id: string; text: string }>
  }
  receipt?: {
    id: string
    policy_version: string
    statement_digest: string
    accepted_statement_ids: string[]
    accepted_at: string
    expires_at: string
  }
}

type HostedUploadGrant = {
  source_id: string
  upload_path: string
}

type Envelope<T> = { data: T; error?: { message?: string } }

function hostedHeaders(connection: HostedConnection, initial?: HeadersInit): Headers {
  const headers = new Headers(initial)
  if (connection.token) headers.set('Authorization', `Bearer ${connection.token}`)
  headers.set('X-Agent-Memory-Tenant', connection.tenant)
  return headers
}

async function localSessionRequest(path: string, init: RequestInit = {}): Promise<LocalSession> {
  const headers = new Headers(init.headers)
  if (init.body) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' })
  if (response.status === 204) return { state: 'signup_required' }
  const value = await response.json().catch(() => ({})) as Partial<Envelope<LocalSession>>
  if (!response.ok) throw new Error(value.error?.message || 'The local session is unavailable.')
  return value.data as LocalSession
}

export function getLocalSession(): Promise<LocalSession> {
  return localSessionRequest('/v1/local-session')
}

export function signupLocalOwner(input: { display_name: string; email: string; private_installation_confirmed: boolean }): Promise<LocalSession> {
  return localSessionRequest('/v1/local-session/signup', { method: 'POST', body: JSON.stringify(input) })
}

export async function logoutLocalSession(): Promise<void> {
  await localSessionRequest('/v1/local-session', { method: 'DELETE' })
}

async function hostedRequest<T>(connection: HostedConnection, path: string, init: RequestInit = {}): Promise<T> {
  const headers = hostedHeaders(connection, init.headers)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' })
  const value = await response.json().catch(() => ({})) as Partial<Envelope<T>>
  if (!response.ok) throw new Error(value.error?.message || 'The request was not accepted.')
  return value.data as T
}

export function listHostedSources(connection: HostedConnection): Promise<HostedSource[]> {
  return hostedRequest(connection, `/v1/sources?workspace_id=${encodeURIComponent(connection.workspace)}`)
}

export function listHostedSourceStatuses(connection: HostedConnection): Promise<HostedSourceStatus[]> {
  return hostedRequest(connection, `/v1/source-statuses?workspace_id=${encodeURIComponent(connection.workspace)}`)
}

export function listHostedProcessingTasks(connection: HostedConnection): Promise<HostedProcessingTask[]> {
  return hostedRequest(connection, `/v1/processing-tasks?workspace_id=${encodeURIComponent(connection.workspace)}`)
}

export function listHostedProjects(connection: HostedConnection): Promise<{ projects: HostedProject[] }> {
  return hostedRequest(connection, '/v1/local-projects')
}

export function getHostedProjectLifecycle(connection: HostedConnection, workspace: string): Promise<{ scheduler?: import('./api').SchedulerSummary; history: import('./api').SchedulerRunHistory[] }> {
  return hostedRequest(connection, `/v1/local-projects/lifecycle?workspace=${encodeURIComponent(workspace)}&limit=100`)
}

export function listHostedProjectSkills(connection: HostedConnection, workspace: string): Promise<{ skills: import('./api').SkillInfo[] }> {
  return hostedRequest(connection, `/v1/local-projects/skills?workspace=${encodeURIComponent(workspace)}`)
}

export function listHostedClientProfiles(connection: HostedConnection): Promise<{ profiles: import('./api').ClientProfile[] }> {
  return hostedRequest(connection, '/v1/local-client-profiles')
}

export function createHostedClientProfile(connection: HostedConnection, input: { id: string; display_name: string; client_kind: import('./api').ClientKind; tool_profile: import('./api').ClientToolProfile }): Promise<{ profile: import('./api').ClientProfile }> {
  return hostedRequest(connection, '/v1/local-client-profiles', { method: 'POST', body: JSON.stringify(input) })
}

export function updateHostedClientProfile(connection: HostedConnection, input: { id: string; display_name: string; client_kind: import('./api').ClientKind; tool_profile: import('./api').ClientToolProfile; expected_revision: number }): Promise<{ profile: import('./api').ClientProfile }> {
  const { id, ...body } = input
  return hostedRequest(connection, `/v1/local-client-profiles/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) })
}

export function deleteHostedClientProfile(connection: HostedConnection, input: { id: string; expected_revision: number }): Promise<{ deleted: boolean; id: string }> {
  const query = new URLSearchParams({ expected_revision: String(input.expected_revision) })
  return hostedRequest(connection, `/v1/local-client-profiles/${encodeURIComponent(input.id)}?${query.toString()}`, { method: 'DELETE' })
}

export function studyHostedProject(connection: HostedConnection, input: { workspace: string; depth: 'shallow' | 'medium' | 'deep'; dry_run: boolean; max_files: number; offset: number }): Promise<HostedProjectStudyResult> {
  return hostedRequest(connection, '/v1/local-projects/study', { method: 'POST', body: JSON.stringify(input) })
}

export function searchHostedProjectMemories(connection: HostedConnection, input: { workspace: string; query: string; limit: number; cursor?: string }): Promise<{ items: HostedProjectMemoryResult[]; next_cursor?: string }> {
  return hostedRequest(connection, '/v1/local-projects/search', { method: 'POST', body: JSON.stringify(input) })
}

export function browseHostedProjectMemories(connection: HostedConnection, input: { workspace: string; mode: 'recent' | 'pinned' | 'type' | 'ungrouped'; limit: number; cursor?: string }): Promise<{ items: HostedMemory[]; next_cursor?: string }> {
  const query = new URLSearchParams({ workspace: input.workspace, mode: input.mode, limit: String(input.limit) })
  if (input.cursor) query.set('cursor', input.cursor)
  return hostedRequest(connection, `/v1/local-projects/memories?${query.toString()}`)
}

export function getHostedProjectMemory(connection: HostedConnection, workspace: string, memoryID: string): Promise<HostedMemory> {
  return hostedRequest(connection, `/v1/local-projects/memories/${encodeURIComponent(memoryID)}?workspace=${encodeURIComponent(workspace)}`)
}

export function listHostedRetrievalFeedback(connection: HostedConnection, workspace: string): Promise<{ feedback: HostedRetrievalRequest[] | null }> {
  return hostedRequest(connection, `/v1/local-project-feedback?workspace=${encodeURIComponent(workspace)}`)
}

export function submitHostedRetrievalFeedback(connection: HostedConnection, input: { workspace: string; request_id: string; score: number; reason: string; useful_count?: number; total_count?: number }): Promise<unknown> {
  return hostedRequest(connection, '/v1/local-project-feedback', { method: 'POST', body: JSON.stringify(input) })
}

export function listHostedProjectSolutions(connection: HostedConnection, workspace: string): Promise<{ episodes: import('./api').SolutionEpisodeRecord[] }> {
  return hostedRequest(connection, `/v1/local-project-solutions?workspace=${encodeURIComponent(workspace)}&limit=100`)
}

export function getHostedProjectSolution(connection: HostedConnection, workspace: string, episodeID: string): Promise<{ detail: import('./api').SolutionEpisodeDetailRecord }> {
  return hostedRequest(connection, `/v1/local-project-solutions?workspace=${encodeURIComponent(workspace)}&episode_id=${encodeURIComponent(episodeID)}`)
}

export function reviewHostedProjectSolution(connection: HostedConnection, input: {
  workspace: string
  episode_id: string
  action: 'pin' | 'misleading' | 'redact' | 'correct' | 'supersede' | 'delete'
  step_id?: string
  reason?: string
  reason_class?: string
  summary?: string
  successor_episode_id?: string
  idempotency_key?: string
  pinned?: boolean
}): Promise<unknown> {
  return hostedRequest(connection, '/v1/local-project-solutions/review', { method: 'POST', body: JSON.stringify(input) })
}

export function getHostedRightsAttestationStatus(connection: HostedConnection): Promise<HostedRightsAttestationStatus> {
  return hostedRequest(connection, '/v1/attestations/rights')
}

export function acceptHostedRightsAttestation(connection: HostedConnection, input: { policy_version: string; accepted_statement_ids: string[] }): Promise<HostedRightsAttestationStatus> {
  return hostedRequest(connection, '/v1/attestations/rights', { method: 'POST', body: JSON.stringify(input) })
}

export function retryHostedSource(connection: HostedConnection, sourceID: string): Promise<HostedSource> {
  return hostedRequest(connection, `/v1/sources/${encodeURIComponent(sourceID)}/retry`, { method: 'POST', body: '{}' })
}

export function deleteHostedSource(connection: HostedConnection, sourceID: string): Promise<unknown> {
  return hostedRequest(connection, `/v1/sources/${encodeURIComponent(sourceID)}`, { method: 'DELETE', headers: { 'Idempotency-Key': crypto.randomUUID() } })
}

export function searchHostedMemories(connection: HostedConnection, query: string): Promise<{ items: HostedMemory[]; next_cursor?: string }> {
  return hostedRequest(connection, '/v1/search', { method: 'POST', body: JSON.stringify({ workspace_id: connection.workspace, query, limit: 20 }) })
}

export function queryHostedSources(connection: HostedConnection, sourceIDs: string[], query: string, limit: number, offset: number): Promise<{ answerable: boolean; evidence_available: boolean; synthesis?: string; evidence: HostedEvidence[]; pagination: { offset: number; limit: number; has_more: boolean; next_offset?: number }; context?: { strategy?: string; reconstructed_windows?: number; semantic?: HostedSemanticContext } }> {
  return hostedRequest(connection, '/v1/source-queries', { method: 'POST', body: JSON.stringify({ source_ids: sourceIDs, query, limit, offset, generate: false, provider: 'local-minilm-scaffold', model: 'local-hash-v1' }) })
}

export function createHostedProposal(connection: HostedConnection, input: { content: string; type: string; evidence: HostedEvidence[] }): Promise<{ id: string; content: string; status: string }> {
  return hostedRequest(connection, '/v1/memory-proposals', { method: 'POST', body: JSON.stringify({ workspace_id: connection.workspace, memory_type: input.type, content: input.content, transformation: 'interpretation', evidence: input.evidence.map(({ source_id, source_version, passage_id, citation_id }) => ({ source_id, source_version, passage_id, citation_id })) }) })
}

export function editHostedProposal(connection: HostedConnection, proposalID: string, content: string): Promise<{ id: string; content: string; status: string }> {
  return hostedRequest(connection, `/v1/memory-proposals/${encodeURIComponent(proposalID)}`, { method: 'PATCH', body: JSON.stringify({ content, transformation: 'user_edit' }) })
}

export function acceptHostedProposal(connection: HostedConnection, proposalID: string): Promise<{ id: string; content: string; status: string }> {
  return hostedRequest(connection, `/v1/memory-proposals/${encodeURIComponent(proposalID)}/accept`, { method: 'POST', body: '{}' })
}

export function rejectHostedProposal(connection: HostedConnection, proposalID: string): Promise<{ id: string; content: string; status: string }> {
  return hostedRequest(connection, `/v1/memory-proposals/${encodeURIComponent(proposalID)}/reject`, { method: 'POST', body: '{}' })
}

export function getHostedPrivacy(connection: HostedConnection): Promise<Record<string, unknown>> {
  return hostedRequest(connection, '/v1/privacy')
}

export function getHostedBilling(connection: HostedConnection): Promise<Record<string, unknown>> {
  return hostedRequest(connection, '/v1/billing')
}

export function requestHostedPlanChange(connection: HostedConnection, action: 'upgrade' | 'cancel', planID: string): Promise<unknown> {
  return hostedRequest(connection, '/v1/billing/plan-changes', { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ action, plan_id: planID }) })
}

export function listHostedCredentials(connection: HostedConnection): Promise<Array<Record<string, unknown>>> {
  return hostedRequest(connection, '/v1/credentials')
}

export function createHostedCredential(connection: HostedConnection, label: string, scopes: string[], expiresAt: string): Promise<Record<string, unknown>> {
  return hostedRequest(connection, '/v1/credentials', { method: 'POST', body: JSON.stringify({ label, scopes, expires_at: expiresAt }) })
}

export function rotateHostedCredential(connection: HostedConnection, credentialID: string, expiresAt: string): Promise<Record<string, unknown>> {
  return hostedRequest(connection, `/v1/credentials/${encodeURIComponent(credentialID)}/rotate`, { method: 'POST', body: JSON.stringify({ expires_at: expiresAt }) })
}

export function revokeHostedCredential(connection: HostedConnection, credentialID: string): Promise<unknown> {
  return hostedRequest(connection, `/v1/credentials/${encodeURIComponent(credentialID)}`, { method: 'DELETE' })
}

export function requestHostedExport(connection: HostedConnection): Promise<{ id: string; state: string }> {
  return hostedRequest(connection, '/v1/exports', { method: 'POST', body: JSON.stringify({ workspace_id: connection.workspace }) })
}

export function getHostedExport(connection: HostedConnection, exportID: string): Promise<{ id: string; state: string; expires_at?: string }> {
  return hostedRequest(connection, `/v1/exports/${encodeURIComponent(exportID)}`)
}

export async function downloadHostedExport(connection: HostedConnection, exportID: string): Promise<Blob> {
  const response = await fetch(`/v1/exports/${encodeURIComponent(exportID)}/download`, { headers: hostedHeaders(connection), credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) throw new Error('The export is not ready for download.')
  return response.blob()
}

export function deleteHostedAccount(connection: HostedConnection): Promise<{ id: string; state: string }> {
  return hostedRequest(connection, '/v1/account', { method: 'DELETE', headers: { 'Idempotency-Key': crypto.randomUUID() } })
}

function sourceMediaType(file: File): string {
  const extension = file.name.toLowerCase().split('.').pop()
  if (extension === 'pdf') return 'application/pdf'
  if (extension === 'epub') return 'application/epub+zip'
  if (extension === 'md' || extension === 'markdown') return 'text/markdown'
  if (extension === 'txt') return 'text/plain'
  throw new Error('Choose a PDF, EPUB, Markdown, or plain-text file.')
}

async function sha256(file: File): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer())
  return Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, '0')).join('')
}

export async function uploadHostedSource(
  connection: HostedConnection,
  file: File,
  rightsBasis: HostedRightsBasis,
): Promise<{ source_id: string }> {
  const mediaType = sourceMediaType(file)
  const checksum = await sha256(file)
  const grant = await hostedRequest<HostedUploadGrant>(connection, '/v1/sources/uploads', {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: JSON.stringify({
      workspace_id: connection.workspace,
      filename: file.name,
      media_type: mediaType,
      size_bytes: file.size,
      checksum_sha256: checksum,
      rights_basis: rightsBasis,
    }),
  })
  const response = await fetch(grant.upload_path, {
    method: 'PUT',
    cache: 'no-store',
    headers: { 'Content-Type': mediaType },
    body: file,
  })
  if (!response.ok) throw new Error('The file upload could not be completed.')
  return { source_id: grant.source_id }
}

export function importHostedBundle(connection: HostedConnection, file: File, passphrase: string, idempotencyKey: string): Promise<HostedImportResult> {
  return hostedRequest(connection, '/v1/imports', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/octet-stream',
      'X-Agent-Memory-Workspace': connection.workspace,
      'X-Agent-Memory-Bundle-Passphrase': passphrase,
      'Idempotency-Key': idempotencyKey,
    },
    body: file,
  })
}

export function getHostedImport(connection: HostedConnection, importID: string): Promise<HostedImportResult> {
  return hostedRequest(connection, `/v1/imports/${encodeURIComponent(importID)}`)
}

function hostedGraphQuery(connection: HostedConnection, configurationId?: string): string {
  const query = new URLSearchParams({ workspace_id: connection.workspace })
  if (configurationId) query.set('configuration_id', configurationId)
  return query.toString()
}

export function getHostedGraphReadiness(connection: HostedConnection, signal?: AbortSignal): Promise<GraphReadiness> {
  return hostedRequest(connection, `/v1/graph-index/readiness?${hostedGraphQuery(connection)}`, { signal })
}

export function getHostedGraphStatus(connection: HostedConnection, signal?: AbortSignal): Promise<GraphStatus> {
  return hostedRequest(connection, `/v1/graph-index/status?${hostedGraphQuery(connection)}`, { signal })
}

export function getHostedGraphSnapshot(connection: HostedConnection, signal?: AbortSignal): Promise<GraphSnapshot> {
  return hostedRequest(connection, `/v1/graph-index/explorer?${hostedGraphQuery(connection)}`, { signal })
}

export function recallHostedGraph(connection: HostedConnection, query: string, options: GraphAskOptions, signal?: AbortSignal): Promise<HostedGraphRecallResponse> {
  return hostedRequest(connection, '/v1/graph-index/recall', {
    method: 'POST',
    signal,
    body: JSON.stringify({
      workspace_id: connection.workspace,
      query,
      mode: options.mode,
      required: Boolean(options.required),
      allow_stale: Boolean(options.allowStale),
      limit: 50,
    }),
  })
}

export async function operateHostedGraph(connection: HostedConnection, configurationId: string, action: GraphOperationAction, expectedRevision?: string, jobId?: string): Promise<GraphStatus> {
  const result = await hostedRequest<{ status: GraphStatus }>(connection, '/v1/graph-index/operations', { method: 'POST', body: JSON.stringify({ workspace_id: connection.workspace, configuration_id: configurationId, action, expected_revision: expectedRevision || '', job_id: jobId || '', idempotency_key: crypto.randomUUID() }) })
  return result.status
}

export async function reviewHostedGraph(connection: HostedConnection, input: GraphReviewInput): Promise<void> {
  await hostedRequest(connection, '/v1/graph-index/review', { method: 'POST', body: JSON.stringify({ scope: { workspace_id: connection.workspace }, action: input.action, target_kind: input.targetKind, target_id: input.targetId, from: input.from, to: input.to, expected_version: input.expectedVersion, reason: input.reason || '' }) })
}

export async function submitHostedGraphFeedback(connection: HostedConnection, requestId: string, targetKind: string, targetId: string, outcome: string, reason?: string): Promise<void> {
  await hostedRequest(connection, '/v1/graph-index/feedback', { method: 'POST', body: JSON.stringify({ scope: { workspace_id: connection.workspace }, request_id: requestId, target_kind: targetKind, target_id: targetId, outcome, reason: reason || '', created_at: new Date().toISOString() }) })
}
import type { GraphAskOptions, GraphOperationAction, GraphReadiness, GraphRecallContext, GraphReviewInput, GraphRouteDecision, GraphSnapshot, GraphStatus } from './knowledgeGateway'
