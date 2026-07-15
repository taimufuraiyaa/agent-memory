export type OutcomeResult = 'success' | 'failure' | 'partial'
export type MemoryType = 'episodic' | 'semantic' | 'procedural' | 'outcome'
export type StorageTier = 'markdown' | 'vector' | 'vector+graph' | 'document' | 'cold'

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
  size_bytes: number
  memory_count: number
  last_activity: string
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
  const res = await fetch(path, {
    ...init,
    headers: {
      'content-type': 'application/json',
      ...(init?.headers ?? {}),
    },
  })
  const json = (await res.json()) as unknown
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

export function listProjects(): Promise<{ projects: ProjectListItem[] }> {
  return api('/api/v1/projects/list', { method: 'GET' })
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

export function listRecentMemories(input: { workspace: string; limit?: number }): Promise<RecentMemoriesResponse> {
  const qs = new URLSearchParams()
  qs.set('workspace', input.workspace)
  if (typeof input.limit === 'number') qs.set('limit', String(input.limit))
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
}): Promise<RecallPreviewResponse> {
  return api('/api/v1/memories/recall/preview', {
    method: 'POST',
    body: JSON.stringify(input),
  })
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

export function listSkills(input: { workspace: string }): Promise<SkillInfo[]> {
  return api<{ skills: SkillInfo[] }>(`/api/v1/skills?workspace=${encodeURIComponent(input.workspace)}`, {
    method: 'GET',
  }).then((res) => res.skills || [])
}
