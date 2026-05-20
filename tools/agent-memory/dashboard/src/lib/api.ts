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
  superseded_by?: string
  storage_tier: StorageTier
  importance: number
  pinned: boolean
  promoted_at?: string
  demoted_at?: string
  tier?: string
  score?: number
  score_breakdown?: Record<string, number>
  match_reason?: string
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

export type TokenMetricGroupTotals = TokenMetricTotals & {
  run_label: string
  memory_enabled: boolean
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

export type DashboardStats = {
  workspace: string
  memory_count: number
  db_size_bytes: number
  memory_type_counts?: CountMap
  storage_tier_counts?: CountMap
  diagram_count?: number
  pinned_count?: number
  last_memory_updated_at?: string
  last_memory_accessed_at?: string
  last_activity?: string
  token_metrics: TokenMetricTotals
  token_metrics_by_group: TokenMetricGroupTotals[]
  raw_token_metrics_by_group: TokenMetricGroupTotals[]
  token_metrics_by_group_all?: TokenMetricGroupTotals[]
  llm_usage_totals: LLMUsageTotals
  llm_usage_by_group: LLMUsageGroupTotals[]
  raw_llm_usage_by_group: LLMUsageGroupTotals[]
  llm_usage_by_group_all?: LLMUsageGroupTotals[]
  token_savings_percent: number
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
  memories_included_full?: MemoryEntry[]
  memories_clipped: Array<{ id: string; reason: string; would_add_tokens: number }>
  tier_distribution: Record<string, number>
  clipping: unknown
  workspace: string
  requested_task: string
  requested_explain: boolean
  requested_top_k: number
  requested_budget: number
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
