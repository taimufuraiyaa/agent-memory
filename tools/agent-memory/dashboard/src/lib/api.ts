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

export function getStats(workspace?: string): Promise<Record<string, unknown>> {
  const qs = workspace ? `?workspace=${encodeURIComponent(workspace)}` : ''
  return api(`/api/v1/stats${qs}`, { method: 'GET' })
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
