export type OutcomeResult = 'success' | 'failure' | 'partial'
export type MemoryType = 'episodic' | 'semantic' | 'procedural' | 'outcome'
export type StorageTier = 'markdown' | 'vector' | 'vector+graph' | 'document' | 'cold'
export type NoteIndexState = 'pending' | 'indexing' | 'ready' | 'failed' | 'retired' | 'paused'

export type ClientKind = 'codex' | 'claude' | 'cursor' | 'kiro' | 'other'
export type ClientToolProfile = 'default' | 'expanded'

export type ClientProfile = {
  id: string
  display_name: string
  client_kind: ClientKind
  tool_profile: ClientToolProfile
  revision: number
  created_at: string
  updated_at: string
}

export type DeploymentDecisionStatus = 'assumed' | 'operator_confirmed'

export type DeploymentProfile = {
  monthly_infrastructure_operations_budget_usd: number
  decision_status: DeploymentDecisionStatus
  revision: number
  created_at: string
  updated_at: string
}

export type RightsBasis = 'author_owned' | 'licensed' | 'public_domain' | 'lawfully_acquired_private_use'

export type RightsAttestationPolicy = {
  version: string
  effective_at: string
  renewal_days: number
  primary_confirmation: string
  statement_digest: string
  statements: Array<{ id: string; text: string }>
}

export type RightsAttestationStatus = {
  status: 'required' | 'active' | 'expired'
  reason: 'missing' | 'active' | 'expired' | 'policy_changed'
  policy: RightsAttestationPolicy
  receipt?: {
    id: string
    policy_version: string
    statement_digest: string
    accepted_statement_ids: string[]
    accepted_at: string
    expires_at: string
  }
}

export type NoteDocument = {
  id: string
  workspace: string
  path: string
  title: string
  body: string
  properties: Record<string, unknown>
  revision: number
  content_hash: string
  index_state: NoteIndexState
  indexed_revision: number
  index_error?: string
  created_at: string
  updated_at: string
  deleted_at?: string
}

export type NoteRevision = {
  note_id: string
  workspace: string
  revision: number
  path: string
  title: string
  body: string
  properties: Record<string, unknown>
  content_hash: string
  author_kind: string
  created_at: string
}

export type NoteLink = {
  source_note_id: string
  target_note_id?: string
  raw_target: string
  line: number
  snippet: string
}

export type LibraryImportRequest = {
  workspace: string
  library_id?: string
  library_kind?: 'personal' | 'organization'
  organization_id?: string
  principal_id?: string
  title: string
  edition_label: string
  language: string
  format?: 'markdown' | 'text'
  markdown: string
  rights_basis: RightsBasis
}

export type LibraryFileImportRequest = Omit<LibraryImportRequest, 'markdown' | 'format'> & {
  format: 'pdf' | 'epub' | 'markdown' | 'text'
  source_file: File
}

export type LibraryImportResult = {
  work_id: string
  edition_id: string
  asset_id: string
  format: 'pdf' | 'epub' | 'markdown' | 'text'
  node_count: number
  passage_count?: number
  existing: boolean
}

export type LibraryImportJob = {
  id: string
  state: 'pending' | 'running' | 'completed' | 'failed'
  result?: LibraryImportResult
  error?: string
  created_at: string
}

export type LocalLLMConfig = {
  enabled: boolean
  base_url: string
  text_model: string
  vision_model?: string
  api_key?: string
  clear_api_key?: boolean
  timeout_seconds?: number
}

export type LocalLLMStatus = {
  config: Omit<LocalLLMConfig, 'api_key' | 'clear_api_key'> & { api_key_configured: boolean }
  configured: boolean
  enabled: boolean
  reachable: boolean
  text_model_available: boolean
  vision_model_available?: boolean
  error?: string
}

export type LocalTranslationResult = {
  text: string
  target_language: string
  provider: string
  model: string
}

export type LibraryStructuralNode = {
  id: string
  edition_id: string
  parent_id?: string
  kind: 'part' | 'chapter' | 'section' | 'subsection' | 'appendix'
  ordinal: number
  title: string
  start_offset?: number
  end_offset?: number
  explicit: boolean
}

export type SourceLocator = {
  kind: string
  display: string
  parser_version: string
  normalization_version: string
  text?: {
    heading_path?: string[]
    source_start?: number
    source_end?: number
    normalized_start?: number
    normalized_end?: number
  }
  pdf?: Record<string, unknown>
  epub?: Record<string, unknown>
  web?: Record<string, unknown>
}

export type LibraryPassageResult = {
  passage: {
    id: string
    edition_id: string
    source_asset_id: string
    structural_node_id: string
    text: string
    locator: SourceLocator
    fingerprint: string
  }
  score: number
}

export type BookMemoryProposal = {
  id: string
  workspace: string
  content: string
  confidence: number
  status: 'suggested' | 'accepted' | 'rejected'
  memory_id?: string
  citations?: Array<{ id: string; locator: SourceLocator; short_quote?: string }>
  created_at: string
  reviewed_at?: string
}

export type LibraryQueryRequest = {
  workspace: string
  principal_id?: string
  organization_ids?: string[]
  question: string
  limit?: number
  propose_memory?: boolean
  memory_content?: string
}

export type Diagram = {
  lang: string
  code: string
}

export type MemoryEntry = {
  id: string
  type: MemoryType
  content: string
  diagram?: Diagram
  workspace: string
  entities: string[]
  tags: string[]
  confidence: number
  created_at: string
  updated_at: string
  last_accessed_at: string
  access_count: number
  decay_score: number
  salience_score?: number
  suppression_score?: number
  useful_count?: number
  ignored_count?: number
  rejected_count?: number
  harmful_count?: number
  last_helpful_at?: string
  last_rejected_at?: string
  suppression_until?: string
  familiarity_band_last?: string
  superseded_by?: string
  relations?: Relation[]
  storage_tier: StorageTier
  importance: number
  pinned: boolean
  promoted_at?: string
  demoted_at?: string
  tier?: string
  score?: number
  score_breakdown?: Record<string, number>
  match_reason?: string
  band?: string
  exclusion_reasons?: string[]
  source?: {
    type: string
    session_id?: string
    file_path?: string
    line_range?: number[]
    note_id?: string
    note_revision?: number
    note_path?: string
    heading?: string
  }
}

export type Relation = {
  target_id: string
  type: string
  weight: number
  metadata?: Record<string, string>
}

export type RetrievalPolicy = {
  min_semantic_score: number
  min_total_score: number
  relative_score_cutoff: number
  weak_semantic_score: number
  weak_total_score: number
  weak_relative_cutoff: number
}

export type ProjectListItem = {
  name: string
  db_path: string
  workspace_root?: string
  size_bytes: number
  memory_count: number
  last_activity: string
}

export type ProjectStudyError = {
  path: string
  reason: string
}

export type ProjectStudyResult = {
  sources_scanned: number
  scanned_files: number
  skipped: number
  extracted: number
  written_ids?: string[]
  errors?: ProjectStudyError[]
  dry_run: boolean
  offset: number
  page_files: number
  next_offset: number
  has_more: boolean
}

export type CountMap = Record<string, number>

export type TokenMetricTotals = {
  records: number
  returned_tokens: number
  baseline_tokens: number
  saved_tokens: number
}

export type TokenMetricOperationTotals = TokenMetricTotals & {
  operation: string
}

export type TokenMetricGroupTotals = TokenMetricTotals & {
  run_label: string
  memory_enabled: boolean
  operations?: TokenMetricOperationTotals[]
}

export type LLMUsageTotals = {
  records: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

export type LLMUsageGroupTotals = LLMUsageTotals & {
  run_label: string
  memory_enabled: boolean
}

export type RetrievalReachMemory = {
  id: string
  type: MemoryType
  storage_tier: StorageTier
  access_count: number
  last_accessed_at?: string
  pinned: boolean
  preview: string
}

export type SchedulerWorkspaceSummary = {
  workspace: string
  memory_count: number
  last_activity_at?: string
  last_scheduled_at?: string
  last_completed_at?: string
  last_result?: string
  last_skip_reason?: string
  last_duration_ms?: number
  last_impacts?: number
  last_error?: string
  hygiene_overdue: boolean
  eligible_daily: boolean
  current_skip_reason?: string
  run_in_progress: boolean
}

export type SchedulerRunHistory = {
  id: string
  workspace: string
  started_at: string
  completed_at?: string
  trigger: string
  result: string
  skip_reason?: string
  duration_ms: number
  decay_updated: number
  consolidated: number
  conflicts_found: number
  evicted: number
  promoted: number
  demoted: number
  error?: string
}

export type SchedulerSummary = {
  enabled: boolean
  started_at?: string
  last_tick_at?: string
  next_tick_at?: string
  workspace?: SchedulerWorkspaceSummary
}

export type FeedbackStats = {
  workspace: string
  average_week: number
  average_month: number
  average_year: number
  total_feedback_count: number
  score_distribution?: Record<string, number>
  average_useful_count?: number
  average_total_count?: number
  average_useful_ratio?: number
}

export type AdvisorDimension = {
  key: 'quality' | 'efficiency' | 'hygiene' | 'coverage' | 'trust'
  label: string
  score: number
  weight: number
  available: boolean
  detail: string
}

export type AdvisorRecommendation = {
  id: string
  severity: 'critical' | 'warn' | 'info'
  category: string
  title: string
  detail: string
  metric?: string
}

export type AdvisorReport = {
  workspace: string
  score: number
  grade: 'A' | 'B' | 'C' | 'D' | 'F' | 'N/A'
  neutral: boolean
  dimensions: AdvisorDimension[]
  recommendations: AdvisorRecommendation[]
  evidence: {
    memory_count: number
    active_memory_count: number
    scored_request_count: number
    useful_ratio_sample_count: number
    recall_metric_records: number
  }
}

export type DashboardStats = {
  workspace: string
  advisor?: AdvisorReport
  feedback_stats?: FeedbackStats
  memory_count: number
  db_size_bytes: number
  memory_type_counts?: CountMap
  storage_tier_counts?: CountMap
  diagram_count?: number
  pinned_count?: number
  retrieve_count_total?: number
  retrieved_memory_count?: number
  never_reached_memory_count?: number
  retrieval_coverage_percent?: number
  never_reached_percent?: number
  low_reach_percentile?: number
  low_reach_threshold?: number
  low_reach_memory_count?: number
  top_retrieved_memories?: RetrievalReachMemory[]
  last_memory_updated_at?: string
  last_memory_accessed_at?: string
  last_activity?: string
  token_metrics: TokenMetricTotals
  token_metrics_by_operation?: TokenMetricOperationTotals[]
  token_metrics_by_group: TokenMetricGroupTotals[]
  raw_token_metrics_by_group: TokenMetricGroupTotals[]
  token_metrics_by_group_all?: TokenMetricGroupTotals[]
  recall_token_metrics?: TokenMetricTotals
  llm_usage_totals: LLMUsageTotals
  llm_usage_by_group: LLMUsageGroupTotals[]
  raw_llm_usage_by_group: LLMUsageGroupTotals[]
  llm_usage_by_group_all?: LLMUsageGroupTotals[]
  overall_token_savings_percent?: number
  recall_token_savings_percent?: number
  token_savings_percent: number
  scheduler?: SchedulerSummary
}

export type SessionEntry = {
  workspace: string
  session_id: string
  project_root?: string
  cwd?: string
  started_at?: string
  ended_at?: string
  observation_count: number
  last_seen_at: string
}

export type ObservationEntry = {
  id: string
  workspace: string
  session_id: string
  occurred_at: string
  kind: string
  tool_name?: string
  summary: string
  created_at: string
}

export type ReplayEvent = {
  event_id: string
  session_id: string
  occurred_at: string
  kind: string
  actor?: string
  summary: string
  tool_name?: string
  related_observation_ids?: string[]
  related_memory_ids?: string[]
  schema_version?: string
  capture_mode?: string
}

export type ObservationPromotionResult = {
  workspace: string
  session_id: string
  requested_type: MemoryType
  observations: number
  created_id: string
  deduplicated: boolean
  rejected: boolean
  reject_reason?: string
  storage_tier?: StorageTier
  route_rule?: string
  route_reason?: string
  content_hash?: string
  confidence?: number
  promotion_chars?: number
}

export type SearchResponse = {
  results: MemoryEntry[]
  strong_results?: MemoryEntry[]
  weak_results?: MemoryEntry[]
  suppressed_results?: MemoryEntry[]
  result_bands?: Record<string, number>
  retrieval_policy?: RetrievalPolicy
  total_tokens: number
  search_time_ms: number
  workspace: string
  requested_query: string
}

export type RecallPreviewIncluded = {
  id: string
  type: MemoryType
  tier: StorageTier
  score: number
  tokens: number
  score_breakdown?: Record<string, number>
}

export type RecallPreviewResponse = {
  context_block: string
  tokens_used: number
  tokens_budget: number
  memories_included: RecallPreviewIncluded[]
  weak_memories?: MemoryEntry[]
  suppressed_memories?: MemoryEntry[]
  memories_included_full?: MemoryEntry[]
  memories_clipped: Array<{ id: string; reason: string; would_add_tokens: number }>
  tier_distribution: Record<string, number>
  clipping: unknown
  workspace: string
  requested_task: string
  requested_explain: boolean
  requested_top_k: number
  requested_budget: number
  retrieval_policy?: RetrievalPolicy
  graph_route?: GraphRouteDecision
  graph_context?: GraphRecallContext
  graph_request_id?: string
}

export type BenchmarkClusterSummary = {
  cluster_id: string
  cluster_title: string
  cases: number
  task_success_rate: number
  off_task_success_rate: number
  task_success_delta: number
  answer_fact_coverage: number
  off_answer_fact_coverage: number
  answer_fact_coverage_delta: number
  answer_completeness: number
  off_answer_completeness: number
  answer_completeness_delta: number
  avg_on_runtime_ms: number
  avg_off_runtime_ms: number
  runtime_delta_ms: number
  avg_on_investigation_effort: number
  avg_off_investigation_effort: number
  investigation_effort_delta: number
  continuation_score: number
  continuation_verdict: string
  precision: number
  recall: number
  gold_recall: number
  keyword_coverage: number
  ndcg: number
  f1: number
  token_efficiency: number
  baseline_tokens: number
  returned_tokens: number
  saved_tokens: number
  cost_with_memory: number
  cost_without_memory: number
  cost_saved: number
  cost_saved_pct: number
  combined_score: number
  verdict: string
}

export type BenchmarkRun = {
  id: number
  workspace: string
  run_id: string
  seed_count: number
  case_count: number
  case_limit: number
  top_k: number
  budget: number
  seed_duration_ms: number
  on_duration_ms: number
  off_duration_ms: number
  precision: number
  recall: number
  gold_recall: number
  keyword_coverage: number
  ndcg: number
  f1: number
  token_efficiency: number
  baseline_tokens: number
  returned_tokens: number
  saved_tokens: number
  cost_with_memory: number
  cost_without_memory: number
  cost_saved: number
  cost_saved_pct: number
  combined_score: number
  verdict: string
  off_cases: number
  off_disabled_count: number
  off_all_disabled: boolean
  off_returned_tokens: number
  off_baseline_tokens: number
  off_saved_tokens: number
  task_success_rate: number
  off_task_success_rate: number
  task_success_delta: number
  answer_fact_coverage: number
  off_answer_fact_coverage: number
  answer_fact_coverage_delta: number
  answer_completeness: number
  off_answer_completeness: number
  answer_completeness_delta: number
  avg_on_runtime_ms: number
  avg_off_runtime_ms: number
  runtime_delta_ms: number
  avg_on_investigation_effort: number
  avg_off_investigation_effort: number
  investigation_effort_delta: number
  continuation_score: number
  continuation_verdict: string
  generator_manifest?: Record<string, unknown>
  run_manifest?: Record<string, unknown>
  clusters: BenchmarkClusterSummary[]
  created_at: string
}

export type PinMemoryResponse = {
  workspace: string
  memory_id: string
  pinned: boolean
  updated_memory: MemoryEntry
}

export type DeleteMemoriesResponse = {
  workspace: string
  memory_ids: string[]
  deleted_count: number
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  headers.set('accept', 'application/json')
  if (!(typeof FormData !== 'undefined' && init?.body instanceof FormData)) {
    headers.set('content-type', 'application/json')
  }
  const res = await fetch(path, {
    ...init,
    headers,
  })
  const body = await res.text()
  let json: unknown
  try {
    json = JSON.parse(body)
  } catch {
    const contentType = res.headers.get('content-type') ?? 'unknown content type'
    const preview = body.trim().replace(/\s+/g, ' ').slice(0, 160)
    const detail = preview ? `: ${preview}` : ''
    throw new Error(
      `API ${res.status} ${path} returned a non-JSON response (${contentType})${detail}`,
    )
  }
  const env = json as {
    ok?: boolean
    data?: unknown
    error?: { code?: string; message?: string }
  }
  if (!res.ok || env.ok === false) {
    const msg = env.error?.message ? String(env.error.message) : `HTTP ${res.status}`
    throw new Error(msg)
  }
  return env.data as T
}

export async function downloadPortableMigration(workspace: string, passphrase: string): Promise<Blob> {
  const response = await fetch('/api/v1/migrations/portable-export', {
    method: 'POST',
    cache: 'no-store',
    headers: { accept: 'application/octet-stream', 'content-type': 'application/json' },
    body: JSON.stringify({ workspace, passphrase }),
  })
  if (!response.ok) {
    const envelope = await response.json().catch(() => ({})) as { error?: { message?: string } }
    throw new Error(envelope.error?.message || 'The encrypted migration bundle could not be created.')
  }
  return response.blob()
}

export function listProjects(): Promise<{ projects: ProjectListItem[] }> {
  return api('/api/v1/projects/list', { method: 'GET' })
}

export function studyProject(input: {
  workspace: string
  depth: 'shallow' | 'medium' | 'deep'
  dry_run: boolean
  max_files: number
  offset: number
}): Promise<ProjectStudyResult> {
  return api('/api/v1/projects/study', { method: 'POST', body: JSON.stringify(input) })
}

export function listClientProfiles(): Promise<{ profiles: ClientProfile[] }> {
  return api('/api/v1/client-profiles', { method: 'GET' })
}

export function createClientProfile(input: {
  id: string
  display_name: string
  client_kind: ClientKind
  tool_profile: ClientToolProfile
}): Promise<{ profile: ClientProfile }> {
  return api('/api/v1/client-profiles', { method: 'POST', body: JSON.stringify(input) })
}

export function updateClientProfile(input: {
  id: string
  display_name: string
  client_kind: ClientKind
  tool_profile: ClientToolProfile
  expected_revision: number
}): Promise<{ profile: ClientProfile }> {
  const { id, ...body } = input
  return api(`/api/v1/client-profiles/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) })
}

export function deleteClientProfile(input: { id: string; expected_revision: number }): Promise<{ deleted: boolean; id: string }> {
  const query = new URLSearchParams({ expected_revision: String(input.expected_revision) })
  return api(`/api/v1/client-profiles/${encodeURIComponent(input.id)}?${query.toString()}`, { method: 'DELETE' })
}

export function getDeploymentProfile(): Promise<{ profile: DeploymentProfile }> {
  return api('/api/v1/deployment-profile', { method: 'GET' })
}

export function updateDeploymentProfile(input: {
  monthly_infrastructure_operations_budget_usd: number
  decision_status: DeploymentDecisionStatus
  expected_revision: number
}): Promise<{ profile: DeploymentProfile }> {
  return api('/api/v1/deployment-profile', { method: 'PUT', body: JSON.stringify(input) })
}

export function listNotes(input: { workspace: string; include_deleted?: boolean }): Promise<{ workspace: string; notes: NoteDocument[] }> {
  const qs = new URLSearchParams({ workspace: input.workspace })
  if (input.include_deleted) qs.set('include_deleted', 'true')
  return api(`/api/v1/notes?${qs.toString()}`, { method: 'GET' })
}

export function getNote(input: { workspace: string; note_id: string }): Promise<{ note: NoteDocument }> {
  const qs = new URLSearchParams({ workspace: input.workspace, note_id: input.note_id })
  return api(`/api/v1/notes/get?${qs.toString()}`, { method: 'GET' })
}

export function createNote(input: {
  workspace: string
  path: string
  title: string
  body?: string
  properties?: Record<string, unknown>
  author_kind?: string
}): Promise<{ note: NoteDocument }> {
  return api('/api/v1/notes/create', { method: 'POST', body: JSON.stringify(input) })
}

export function updateNote(input: {
  workspace: string
  note_id: string
  expected_revision: number
  path: string
  title: string
  body: string
  properties: Record<string, unknown>
  author_kind?: string
}): Promise<{ note: NoteDocument }> {
  return api('/api/v1/notes/update', { method: 'POST', body: JSON.stringify(input) })
}

export function trashNote(input: { workspace: string; note_id: string }): Promise<{ note: NoteDocument }> {
  return api('/api/v1/notes/trash', { method: 'POST', body: JSON.stringify(input) })
}

export function restoreNote(input: { workspace: string; note_id: string }): Promise<{ note: NoteDocument }> {
  return api('/api/v1/notes/restore', { method: 'POST', body: JSON.stringify(input) })
}

export function deleteNotePermanently(input: { workspace: string; note_id: string }): Promise<{ note: { deleted: boolean; note_id: string } }> {
  return api('/api/v1/notes/delete', { method: 'POST', body: JSON.stringify(input) })
}

export function listNoteRevisions(input: { workspace: string; note_id: string }): Promise<{ revisions: NoteRevision[] }> {
  const qs = new URLSearchParams({ workspace: input.workspace, note_id: input.note_id })
  return api(`/api/v1/notes/revisions?${qs.toString()}`, { method: 'GET' })
}

export function restoreNoteRevision(input: {
  workspace: string
  note_id: string
  revision: number
  expected_revision: number
}): Promise<{ note: NoteDocument }> {
  return api('/api/v1/notes/revisions/restore', { method: 'POST', body: JSON.stringify(input) })
}

export function listNoteBacklinks(input: { workspace: string; note_id: string }): Promise<{ backlinks: NoteLink[] }> {
  const qs = new URLSearchParams({ workspace: input.workspace, note_id: input.note_id })
  return api(`/api/v1/notes/backlinks?${qs.toString()}`, { method: 'GET' })
}

export function retryNoteIndex(input: { workspace: string; note_id: string }): Promise<{ note: NoteDocument }> {
  return api('/api/v1/notes/index/retry', { method: 'POST', body: JSON.stringify(input) })
}

export function importLibraryBook(input: LibraryImportRequest | LibraryFileImportRequest): Promise<LibraryImportJob> {
  if ('source_file' in input) {
    const form = new FormData()
    form.append('workspace', input.workspace)
    if (input.library_id) form.append('library_id', input.library_id)
    form.append('library_kind', input.library_kind ?? 'personal')
    if (input.organization_id) form.append('organization_id', input.organization_id)
    if (input.principal_id) form.append('principal_id', input.principal_id)
    form.append('title', input.title)
    form.append('edition_label', input.edition_label)
    form.append('language', input.language)
    form.append('format', input.format)
    form.append('rights_basis', input.rights_basis)
    form.append('source', input.source_file)
    return api('/api/v1/library/imports', { method: 'POST', body: form })
  }
  return api('/api/v1/library/imports', { method: 'POST', body: JSON.stringify(input) })
}

export function getRightsAttestationStatus(): Promise<RightsAttestationStatus> {
  return api('/api/v1/rights-attestation/status', { method: 'GET' })
}

export function acceptRightsAttestation(input: {
  policy_version: string
  accepted_statement_ids: string[]
}): Promise<RightsAttestationStatus> {
  return api('/api/v1/rights-attestation/accept', { method: 'POST', body: JSON.stringify(input) })
}

export function getLibraryLocalLLMStatus(): Promise<LocalLLMStatus> {
  return api('/api/v1/library/local-llm', { method: 'GET' })
}

export function testLibraryLocalLLM(input: LocalLLMConfig): Promise<LocalLLMStatus> {
  return api('/api/v1/library/local-llm/test', { method: 'POST', body: JSON.stringify(input) })
}

export function saveLibraryLocalLLM(input: LocalLLMConfig): Promise<LocalLLMStatus> {
  return api('/api/v1/library/local-llm', { method: 'PUT', body: JSON.stringify(input) })
}

export function translateLibraryAnswer(input: { workspace: string; text: string; target_language: string }, signal?: AbortSignal): Promise<LocalTranslationResult> {
  return api('/api/v1/library/local-llm/translate', { method: 'POST', body: JSON.stringify(input), signal })
}

export function getLibraryStructure(input: { workspace: string; principal_id?: string; edition_id: string }): Promise<{ edition_id: string; nodes: LibraryStructuralNode[] }> {
  const qs = new URLSearchParams({ workspace: input.workspace, edition_id: input.edition_id })
  if (input.principal_id) qs.set('principal_id', input.principal_id)
  return api(`/api/v1/library/structure?${qs.toString()}`, { method: 'GET' })
}

export function queryLibrary(input: LibraryQueryRequest): Promise<{ results: LibraryPassageResult[]; proposal?: BookMemoryProposal }> {
  return api('/api/v1/library/query', { method: 'POST', body: JSON.stringify(input) })
}

export function reviewLibraryMemory(input: { workspace: string; proposal_id: string; principal_id?: string; decision: 'accept' | 'reject' }): Promise<BookMemoryProposal> {
  return api('/api/v1/library/memory-review', { method: 'POST', body: JSON.stringify(input) })
}

export function getStats(workspace?: string): Promise<DashboardStats> {
  const qs = workspace ? `?workspace=${encodeURIComponent(workspace)}` : ''
  return api(`/api/v1/stats${qs}`, { method: 'GET' })
}

export function getAdvisor(workspace?: string): Promise<AdvisorReport> {
  const qs = workspace ? `?workspace=${encodeURIComponent(workspace)}` : ''
  return api(`/api/v1/advisor${qs}`, { method: 'GET' })
}

export function listSchedulerHistory(input: { workspace: string; limit?: number }): Promise<{ workspace: string; limit: number; history: SchedulerRunHistory[] }> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
  return api(`/api/v1/scheduler/history?${qs.toString()}`, { method: 'GET' })
}

export function listSessions(input: { workspace: string; limit?: number }): Promise<{ workspace: string; limit: number; sessions: SessionEntry[] }> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
  return api(`/api/v1/sessions?${qs.toString()}`, { method: 'GET' })
}

export function listObservations(input: {
  workspace: string
  session_id?: string
  limit?: number
  from?: string
  to?: string
}): Promise<{ workspace: string; session_id: string; limit: number; observations: ObservationEntry[] }> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  if (input.session_id) qs.set('session_id', input.session_id)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
  if (input.from) qs.set('from', input.from)
  if (input.to) qs.set('to', input.to)
  return api(`/api/v1/observations?${qs.toString()}`, { method: 'GET' })
}

export function listReplayEvents(input: { workspace: string; session_id: string; limit?: number; cursor?: string }): Promise<{ workspace: string; session_id: string; events: ReplayEvent[]; count: number; next_cursor?: string }> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  qs.set('session_id', input.session_id)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
  if (input.cursor) qs.set('cursor', input.cursor)
  return api(`/api/v1/replay/events?${qs.toString()}`, { method: 'GET' })
}

export function listBenchmarkRuns(input: { workspace: string; limit?: number }): Promise<{ workspace: string; limit: number; runs: BenchmarkRun[] }> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
  return api(`/api/v1/benchmark/runs?${qs.toString()}`, { method: 'GET' })
}

export function promoteObservations(input: {
  workspace: string
  session_id: string
  max_items?: number
  type?: MemoryType
}): Promise<ObservationPromotionResult> {
  return api('/api/v1/observations/promote', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export type RecentMemoriesResponse = {
  results: MemoryEntry[]
  workspace: string
  limit: number
}

export function listRecentMemories(input: { workspace: string; limit?: number; ungrouped?: boolean }): Promise<RecentMemoriesResponse> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
  if (input.ungrouped) qs.set('ungrouped', 'true')
  return api(`/api/v1/memories/recent?${qs.toString()}`, { method: 'GET' })
}

export function searchMemories(input: {
  workspace: string
  query: string
  top_k: number
  explain: boolean
  filters: {
    types: MemoryType[]
    tiers: StorageTier[]
    outcome_result?: OutcomeResult
    min_confidence?: number
    min_decay_score?: number
    min_semantic_score?: number
    min_total_score?: number
    relative_cutoff?: number
    entities?: string[]
    date_from?: string
    date_to?: string
  }
}): Promise<SearchResponse> {
  return api('/api/v1/memories/search', {
    method: 'POST',
    body: JSON.stringify({
      workspace: input.workspace,
      query: input.query,
      top_k: input.top_k,
      mode: 'search',
      explain: input.explain,
      filters: input.filters,
    }),
  })
}

export function setMemoryPinned(input: { workspace: string; memory_id: string; pinned: boolean }): Promise<PinMemoryResponse> {
  return api('/api/v1/memories/pin', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export function deleteMemories(input: { workspace: string; memory_ids: string[] }): Promise<DeleteMemoriesResponse> {
  return api('/api/v1/memories/delete', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export function recallPreview(input: {
  workspace: string
  task_description: string
  top_k: number
  token_budget: number
  explain: boolean
  include_memories: boolean
  graph_mode?: 'basic' | 'auto' | 'local_graph' | 'global'
  graph_required?: boolean
  graph_allow_stale?: boolean
}, signal?: AbortSignal): Promise<RecallPreviewResponse> {
  return api('/api/v1/memories/recall/preview', {
    method: 'POST',
    body: JSON.stringify(input),
    signal,
  })
}

function graphQuery(scope: { workspaceId: string }, configurationId?: string): string {
  const query = new URLSearchParams({ workspace: scope.workspaceId })
  if (configurationId) query.set('configuration_id', configurationId)
  return query.toString()
}

export function getGraphReadiness(scope: { workspaceId: string }, signal?: AbortSignal): Promise<GraphReadiness> {
  return api(`/api/v1/graph-index/readiness?${graphQuery(scope)}`, { signal })
}

export function getGraphStatus(scope: { workspaceId: string }, signal?: AbortSignal): Promise<GraphStatus> {
  return api(`/api/v1/graph-index/status?${graphQuery(scope)}`, { signal })
}

export function getGraphSnapshot(scope: { workspaceId: string }, signal?: AbortSignal): Promise<GraphSnapshot> {
  return api(`/api/v1/graph-index/explorer?${graphQuery(scope)}`, { signal })
}

export async function operateGraph(scope: { workspaceId: string }, configurationId: string, action: GraphOperationAction, expectedRevision?: string, jobId?: string): Promise<GraphStatus> {
  const result = await api<{ status: GraphStatus }>('/api/v1/graph-index/operations', { method: 'POST', body: JSON.stringify({ scope: { workspace_id: scope.workspaceId }, configuration_id: configurationId, action, expected_revision: expectedRevision || '', job_id: jobId || '', idempotency_key: crypto.randomUUID() }) })
  return result.status
}

export async function reviewGraph(scope: { workspaceId: string }, input: GraphReviewInput): Promise<void> {
  await api('/api/v1/graph-index/review', { method: 'POST', body: JSON.stringify({ scope: { workspace_id: scope.workspaceId }, action: input.action, target_kind: input.targetKind, target_id: input.targetId, from: input.from, to: input.to, expected_version: input.expectedVersion, reason: input.reason || '' }) })
}

export async function submitGraphFeedback(scope: { workspaceId: string }, requestId: string, targetKind: string, targetId: string, outcome: string, reason?: string): Promise<void> {
  await api('/api/v1/graph-index/feedback', { method: 'POST', body: JSON.stringify({ scope: { workspace_id: scope.workspaceId }, request_id: requestId, target_kind: targetKind, target_id: targetId, outcome, reason: reason || '', created_at: new Date().toISOString() }) })
}

export type RetrievalRequestLog = {
  id: string
  workspace: string
  request_type: string
  query: string
  score: number
  reason: string
  useful_count?: number
  total_count?: number
  created_at: string
}

export type SolutionEpisodeRecord = {
  episode: {
    id: string
    workspace: string
    session_id: string
    principal_id: string
    client_id: string
    goal_summary: string
    status: 'active' | 'paused' | 'completed' | 'partial' | 'abandoned' | 'cancelled'
    retention_class: 'transient' | 'standard' | 'pinned'
    version: number
    superseded_by?: string
    created_at: string
    updated_at: string
  }
  summary?: {
    id: string
    outcome: 'success' | 'failure' | 'partial'
    summary: string
    evidence: Array<{ kind: string; target_id: string; locator?: string; resolution?: string }>
    risks?: string[]
    next_guidance?: string
    validation: 'proposed' | 'verified' | 'rejected'
    created_at: string
  }
  pinned: boolean
  step_count: number
}

export type SolutionEpisodeDetailRecord = SolutionEpisodeRecord & {
  steps: Array<{
    id: string
    ordinal: number
    kind: string
    status: string
    summary: string
    rationale_summary?: string
    confidence: number
    references?: Array<{ kind: string; target_id: string; locator?: string; resolution?: string }>
    created_at: string
  }>
  promotions: Array<{ id: string; kind: string; memory_type?: string; target_id?: string; state: string; created_at: string }>
  promotion_targets: Array<{
    promotion: { id: string; kind: string; memory_type?: string; target_id?: string; state: string; created_at: string }
    memory?: MemoryEntry
    availability: string
  }>
  step_reviews: Array<{ step_id: string; misleading: boolean; redacted: boolean; reason?: string; reason_class?: string; updated_at: string }>
  path_feedback: Array<{ id: string; target_kind: string; target_id: string; outcome: 'helpful' | 'ignored' | 'rejected' | 'harmful'; created_at: string }>
}

export function listSolutionEpisodes(input: { workspace: string; limit?: number }): Promise<{ episodes: SolutionEpisodeRecord[] }> {
  return api(`/api/v1/solutions/activity?workspace=${encodeURIComponent(input.workspace)}&limit=${input.limit || 100}`, { method: 'GET' })
}

export function getSolutionEpisode(input: { workspace: string; episode_id: string }): Promise<{ detail: SolutionEpisodeDetailRecord }> {
  return api(`/api/v1/solutions/activity?workspace=${encodeURIComponent(input.workspace)}&episode_id=${encodeURIComponent(input.episode_id)}`, { method: 'GET' })
}

export function reviewSolutionEpisode(input: {
  workspace: string
  principal_id: string
  episode_id: string
  action: 'pin' | 'misleading' | 'redact' | 'correct' | 'supersede' | 'delete'
  step_id?: string
  reason?: string
  reason_class?: string
  summary?: string
  successor_episode_id?: string
  idempotency_key?: string
  pinned?: boolean
}): Promise<{ reviewed: boolean; result?: unknown }> {
  return api('/api/v1/solutions/review', { method: 'POST', body: JSON.stringify(input) })
}

export function listFeedback(input: { workspace: string }): Promise<RetrievalRequestLog[]> {
  return api(`/api/v1/feedback?workspace=${encodeURIComponent(input.workspace)}`, {
    method: 'GET',
  })
}

export function submitRequestFeedback(input: {
  workspace: string
  request_id: string
  score: number
  reason: string
  useful_count?: number
  total_count?: number
}): Promise<{ ok: boolean }> {
  return api('/api/v1/requests/feedback', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export type SkillInfo = {
  name: string
  display_name: string
  description: string
  content: string
  path: string
}

export type SkillLifecycleSummary = { id: string; workspace: string; name: string; description: string; risk_tier: 'low' | 'medium' | 'high'; owner_group: string; status: 'active' | 'archived'; generation: number }
export type SkillLifecycleRevision = { id: string; workspace: string; skill_id: string; number: number; state: 'draft' | 'testing' | 'canary' | 'active' | 'previous' | 'disabled' | 'rejected'; bundle_digest: string; risk_tier: string; candidate_id?: string; parent_revision_ids?: string[]; source_memory_ids?: string[]; source_tool_lesson_ids?: string[]; source_episode_ids?: string[]; created_by: string; created_at: string }
export type SkillLifecycleActivation = { id: string; environment: string; skill_id: string; active_revision_id: string; active_digest: string; last_known_good_revision_id?: string; canary_revision_id?: string; generation: number; policy_decision_id: string; materialization: string }
export type SkillLifecycleEvaluation = { id: string; revision_id: string; verdict: 'pass' | 'fail' | 'inconclusive'; evaluator: string; evaluator_version: string; completed_at: string; case_results?: Array<{ case_id: string; passed: boolean; independently_verified: boolean; failure_class?: string }> }
export type SkillLifecyclePolicyDecision = { id: string; revision_id: string; decision: 'promote' | 'canary' | 'approval_required' | 'pause' | 'reject'; reason_codes: string[]; evaluation_run_ids: string[]; decided_at: string }
export type SkillLifecycleDetail = { skill: SkillLifecycleSummary; revisions: SkillLifecycleRevision[]; evaluations?: SkillLifecycleEvaluation[]; policy_decisions?: SkillLifecyclePolicyDecision[]; activation?: SkillLifecycleActivation }

export function listSkillLifecycle(input: { workspace: string }): Promise<SkillLifecycleSummary[]> {
  return api<{ skills: SkillLifecycleSummary[] }>(`/api/v1/skills/lifecycle/list?workspace=${encodeURIComponent(input.workspace)}`, { method: 'GET' }).then((res) => res.skills || [])
}
export function inspectSkillLifecycle(input: { workspace: string; skill_id: string; environment?: string }): Promise<SkillLifecycleDetail> {
  return api(`/api/v1/skills/inspect?workspace=${encodeURIComponent(input.workspace)}&skill_id=${encodeURIComponent(input.skill_id)}&environment=${encodeURIComponent(input.environment || 'local')}`, { method: 'GET' })
}
export function operateSkillLifecycle(input: { workspace: string; actor: string; operation: string; payload: Record<string, unknown> }): Promise<{ operation: string; result: unknown }> {
  return api('/api/v1/skills/lifecycle', { method: 'POST', body: JSON.stringify(input) })
}

export function listSkills(input: { workspace: string }): Promise<SkillInfo[]> {
  return api<{ skills: SkillInfo[] }>(`/api/v1/skills?workspace=${encodeURIComponent(input.workspace)}`, {
    method: 'GET',
  }).then((res) => res.skills || [])
}
import type { GraphOperationAction, GraphReadiness, GraphReviewInput, GraphRouteDecision, GraphRecallContext, GraphSnapshot, GraphStatus } from './knowledgeGateway'
