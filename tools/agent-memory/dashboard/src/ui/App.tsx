import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  deleteMemories,
  getStats,
  listBenchmarkRuns,
  listObservations,
  listSessions,
  listProjects,
  listRecentMemories,
  promoteObservations,
  recallPreview,
  searchMemories,
  setMemoryPinned,
  type CountMap,
  type BenchmarkClusterSummary,
  type BenchmarkRun,
  type DashboardStats,
  type LLMUsageGroupTotals,
  type MemoryEntry,
  type MemoryType,
  type ObservationEntry,
  type ObservationPromotionResult,
  type OutcomeResult,
  type ProjectListItem,
  type RecallPreviewResponse,
  type SearchResponse,
  type SessionEntry,
  type StorageTier,
  type TokenMetricGroupTotals,
  type TokenMetricOperationTotals,
  type TokenMetricTotals,
} from '../lib/api'
import { DiagramViewer, renderDiagramMarkupForExport } from './DiagramViewer'
import { MarkdownView } from './MarkdownView'

type ChatMode = 'search' | 'recall'
type Surface = 'overview' | 'search' | 'recall' | 'diagnostics' | 'sessions' | 'benchmark' | 'wiki'
type WikiViewMode = 'article' | 'raw'
type WikiMode = 'search' | 'recall' | 'recents'

type ChatMessage = {
  id: string
  role: 'user' | 'assistant' | 'system'
  mode: ChatMode
  text: string
  createdAt: number
  payload?: {
    results?: MemoryEntry[]
    search?: SearchResponse
    recall?: RecallPreviewResponse
  }
  pending?: boolean
  error?: string
}

type WikiSearchState = {
  mode: WikiMode
  query: string
  searched: boolean
  results: MemoryEntry[]
  weakResults: MemoryEntry[]
}

const allTypes: Array<{ key: MemoryType; label: string }> = [
  { key: 'semantic', label: 'semantic' },
  { key: 'procedural', label: 'procedural' },
  { key: 'outcome', label: 'outcome' },
  { key: 'episodic', label: 'episodic' },
]

const allTiers: Array<{ key: StorageTier; label: string }> = [
  { key: 'vector', label: 'vector' },
  { key: 'markdown', label: 'markdown' },
  { key: 'vector+graph', label: 'vector+graph' },
  { key: 'document', label: 'document' },
]

function formatTS(s?: string): string {
  if (!s) return ''
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleString()
}

function formatClock(ts: number): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatNumber(value?: number): string {
  return typeof value === 'number' ? value.toLocaleString() : '0'
}

function formatPercent(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '0.0%'
  return `${value.toFixed(1)}%`
}

function formatUnitPercent(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '0.0%'
  return `${(value * 100).toFixed(1)}%`
}

function formatDuration(ms?: number): string {
  if (typeof ms !== 'number' || Number.isNaN(ms) || ms <= 0) return '0s'
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(seconds >= 10 ? 0 : 1)}s`
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.round(seconds % 60)
  return `${minutes}m ${remainder}s`
}

const SEARCH_DEFAULT_MIN_SEMANTIC_SCORE = 0.3
const ALL_PROJECTS_SCOPE = '__all_projects__'

const wikiSuggestionPresets = [
  { label: 'pinned threads', query: 'show pinned rules and long-lived facts' },
  { label: 'recent research', query: 'what did we recently learn' },
  { label: 'diagrams', query: 'architecture diagram mermaid flow' },
  { label: 'failures', query: 'recent failures regressions incidents' },
] as const

const semanticFloorPresets = [
  { label: 'diagnose 0.00', value: 0 },
  { label: 'default 0.30', value: 0.3 },
  { label: 'medium 0.40', value: 0.4 },
  { label: 'high 0.55', value: 0.55 },
] as const

type RelevanceTone = 'high' | 'medium' | 'low' | 'weak'

function formatScore(value?: number, digits = 3): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return 'n/a'
  return value.toFixed(digits)
}

function clampUnitScore(value: number): number {
  if (!Number.isFinite(value)) return SEARCH_DEFAULT_MIN_SEMANTIC_SCORE
  return Math.min(1, Math.max(0, value))
}

function parseUnitScore(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  if (Number.isNaN(parsed)) return undefined
  return clampUnitScore(parsed)
}

function getSemanticSimilarity(memory: MemoryEntry): number | undefined {
  const value = memory.score_breakdown?.semantic_similarity
  if (typeof value !== 'number' || Number.isNaN(value)) return undefined
  return clampUnitScore(value)
}

function getSemanticRelevance(value?: number): { label: string; tone: RelevanceTone } {
  if (typeof value !== 'number' || Number.isNaN(value)) return { label: 'Weak', tone: 'weak' }
  if (value >= 0.55) return { label: 'High', tone: 'high' }
  if (value >= 0.4) return { label: 'Medium', tone: 'medium' }
  if (value >= 0.3) return { label: 'Low', tone: 'low' }
  return { label: 'Weak', tone: 'weak' }
}

function zeroTokenTotals(): TokenMetricTotals {
  return {
    records: 0,
    returned_tokens: 0,
    baseline_tokens: 0,
    saved_tokens: 0,
  }
}

function getOperationTotals(items: TokenMetricOperationTotals[] | undefined, operation: string): TokenMetricTotals | null {
  return items?.find((item) => item.operation === operation) ?? null
}

function getGroupOperationTotals(group: TokenMetricGroupTotals, operation: string): TokenMetricTotals | null {
  return group.operations?.find((item) => item.operation === operation) ?? null
}

function formatBytes(bytes?: number): string {
  if (typeof bytes !== 'number' || Number.isNaN(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  const digits = value >= 100 || index === 0 ? 0 : 1
  return `${value.toFixed(digits)} ${units[index]}`
}

function toTitle(value: string): string {
  return value
    .split(/[_+\-\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function sortCountEntries(counts?: CountMap): Array<[string, number]> {
  return Object.entries(counts ?? {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
}

function sumCounts(counts?: CountMap): number {
  return Object.values(counts ?? {}).reduce((total, value) => total + value, 0)
}

function chartColor(index: number): string {
  return `var(--chart-${(index % 6) + 1})`
}

function formatLegendIndex(index: number): string {
  return `[${String(index + 1).padStart(2, '0')}]`
}

function buildPieGradient(entries: Array<[string, number]>): string {
  const total = sumCounts(Object.fromEntries(entries))
  if (total <= 0 || entries.length === 0) return 'conic-gradient(var(--bg-input) 0deg 360deg)'

  let current = 0
  const stops = entries.map(([, value], index) => {
    const start = current
    current += (value / total) * 360
    return `${chartColor(index)} ${start}deg ${current}deg`
  })
  return `conic-gradient(${stops.join(', ')})`
}

function pillList(items: string[]): React.ReactNode {
  if (!items.length) return <span className="muted">-</span>
  return (
    <div className="pills">
      {items.map((x) => (
        <span className="pill" key={x}>
          {x}
        </span>
      ))}
    </div>
  )
}

function makeID(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `${Date.now()}-${Math.random().toString(16).slice(2)}`
  }
}

function hasDiagram(m: MemoryEntry): boolean {
  if (m.diagram && m.diagram.code) return true
  if (m.content && (m.content.includes('```mermaid') || m.content.includes('```graph') || m.content.includes('```chart'))) return true
  return false
}

function buildMemoryKey(memory: MemoryEntry): string {
  return `${memory.workspace}:${memory.id}`
}

function compareMemoryRelevance(a: MemoryEntry, b: MemoryEntry): number {
  const semanticDelta = (getSemanticSimilarity(b) ?? -1) - (getSemanticSimilarity(a) ?? -1)
  if (semanticDelta !== 0) return semanticDelta
  const scoreDelta = (b.score ?? -1) - (a.score ?? -1)
  if (scoreDelta !== 0) return scoreDelta
  const accessDelta = (b.access_count ?? 0) - (a.access_count ?? 0)
  if (accessDelta !== 0) return accessDelta
  const updatedDelta = new Date(b.updated_at || b.created_at).getTime() - new Date(a.updated_at || a.created_at).getTime()
  if (updatedDelta !== 0) return updatedDelta
  return a.id.localeCompare(b.id)
}

function compareMemoryRecency(a: MemoryEntry, b: MemoryEntry): number {
  return new Date(b.updated_at || b.created_at).getTime() - new Date(a.updated_at || a.created_at).getTime()
}

function mergeMemoryResults(items: MemoryEntry[]): MemoryEntry[] {
  const merged = new Map<string, MemoryEntry>()
  for (const item of items) {
    const key = buildMemoryKey(item)
    const current = merged.get(key)
    if (!current || compareMemoryRelevance(item, current) < 0) {
      merged.set(key, item)
    }
  }
  return Array.from(merged.values()).sort(compareMemoryRelevance)
}

function escapeHTML(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

async function buildConsolidatedExportHTML(memories: MemoryEntry[], theme: 'light' | 'dark'): Promise<string> {
  const sections = await Promise.all(
    memories.map(async (memory) => {
      const semantic = getSemanticSimilarity(memory)
      const diagramMarkup = memory.diagram ? await renderDiagramMarkupForExport(memory.diagram, theme) : ''
      return `
        <section class="memory">
          <div class="badges">
            <span>${escapeHTML(memory.workspace)}</span>
            <span>${escapeHTML(memory.type)}</span>
            <span>${escapeHTML(memory.storage_tier)}</span>
            <span>semantic ${escapeHTML(semantic ? semantic.toFixed(2) : 'n/a')}</span>
          </div>
          <div class="content-block">${escapeHTML(memory.content)}</div>
          ${diagramMarkup}
        </section>
      `
    }),
  )
  const joinedSections = sections.join('\n')
  return `<!doctype html>
  <html>
    <head>
      <meta charset="utf-8" />
      <title>Consolidated Wiki View</title>
      <style>
        body { font-family: Menlo, Monaco, monospace; background: #faf7ef; color: #1e1b18; padding: 32px; }
        h1 { margin: 0 0 24px; font-size: 24px; }
        .memory { border: 1px solid #7a7165; padding: 16px; margin-bottom: 18px; }
        .badges { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
        .badges span { border: 1px solid #7a7165; padding: 4px 8px; }
        .content-block, pre { white-space: pre-wrap; word-break: break-word; border: 1px dotted #7a7165; padding: 12px; background: #f3f0e8; }
        .export-diagram { margin-top: 14px; border: 1px dotted #7a7165; padding: 12px; background: #f3f0e8; }
        .export-diagram svg { width: 100%; height: auto; max-height: 720px; }
        .export-diagram-note { margin-top: 8px; color: #6b5f52; }
      </style>
    </head>
    <body>
      <h1>Consolidated Wiki View</h1>
      ${joinedSections}
    </body>
  </html>`
}

function getHealthState(stats: DashboardStats | null, statsErr: string) {
  if (statsErr) {
    return {
      tone: 'bad' as const,
      label: 'Degraded',
      detail: statsErr,
    }
  }
  if (!stats) {
    return {
      tone: 'warn' as const,
      label: 'Loading',
      detail: 'Collecting workspace diagnostics.',
    }
  }
  if (stats.memory_count === 0) {
    return {
      tone: 'warn' as const,
      label: 'Empty',
      detail: 'Workspace is healthy but has no stored memories yet.',
    }
  }
  return {
    tone: 'good' as const,
    label: 'Healthy',
    detail: 'Stats loaded and the workspace responds normally.',
  }
}

export function App() {
  const [surface, setSurface] = useState<Surface>('wiki')
  const [mode, setMode] = useState<ChatMode>('search')

  const [projects, setProjects] = useState<ProjectListItem[]>([])
  const [workspace, setWorkspace] = useState<string>('')
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [statsErr, setStatsErr] = useState<string>('')
  const [benchmarkRuns, setBenchmarkRuns] = useState<BenchmarkRun[]>([])
  const [benchmarkBusy, setBenchmarkBusy] = useState<boolean>(false)
  const [benchmarkErr, setBenchmarkErr] = useState<string>('')
  const [sessions, setSessions] = useState<SessionEntry[]>([])
  const [sessionsBusy, setSessionsBusy] = useState<boolean>(false)
  const [sessionsErr, setSessionsErr] = useState<string>('')
  const [selectedSessionID, setSelectedSessionID] = useState<string>('')
  const [observations, setObservations] = useState<ObservationEntry[]>([])
  const [observationsBusy, setObservationsBusy] = useState<boolean>(false)
  const [observationsErr, setObservationsErr] = useState<string>('')
  const [promotionBusyFor, setPromotionBusyFor] = useState<string>('')
  const [promotionResults, setPromotionResults] = useState<Record<string, ObservationPromotionResult>>({})
  const [overviewExperimentFocusKey, setOverviewExperimentFocusKey] = useState<number>(0)
  const [rawStatsOpen, setRawStatsOpen] = useState<boolean>(false)
  const [deepSearchPrompt, setDeepSearchPrompt] = useState<{ open: boolean; query: string }>({
    open: false,
    query: '',
  })

  const [draft, setDraft] = useState<string>('')
  const [topK, setTopK] = useState<number>(10)
  const [explain, setExplain] = useState<boolean>(true)
  const [advancedOpen, setAdvancedOpen] = useState<boolean>(false)

  const [types, setTypes] = useState<Set<MemoryType>>(new Set(['semantic']))
  const [tiers, setTiers] = useState<Set<StorageTier>>(new Set(['vector']))

  const [outcome, setOutcome] = useState<OutcomeResult | ''>('')
  const [minConfidence, setMinConfidence] = useState<string>('')
  const [minDecay, setMinDecay] = useState<string>('')
  const [minSemantic, setMinSemantic] = useState<string>(SEARCH_DEFAULT_MIN_SEMANTIC_SCORE.toFixed(2))
  const [minTotal, setMinTotal] = useState<string>('')
  const [relativeCutoff, setRelativeCutoff] = useState<string>('')
  const [entities, setEntities] = useState<string>('')
  const [fromDate, setFromDate] = useState<string>('')
  const [toDate, setToDate] = useState<string>('')

  const [recallTopK, setRecallTopK] = useState<number>(50)
  const [budget, setBudget] = useState<number>(4000)
  const [busy, setBusy] = useState<boolean>(false)
  const [recentsBusy, setRecentsBusy] = useState<boolean>(false)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [theme, setTheme] = useState<'light' | 'dark'>('dark')
  const [selectedMemory, setSelectedMemory] = useState<MemoryEntry | null>(null)
  const [diagramPreviewOpen, setDiagramPreviewOpen] = useState<boolean>(false)
  const [inputFocused, setInputFocused] = useState<boolean>(false)
  const [composerFocused, setComposerFocused] = useState<boolean>(false)
  const [wikiQuery, setWikiQuery] = useState<string>('')
  const [wikiMode, setWikiMode] = useState<WikiMode>('search')
  const [wikiScope, setWikiScope] = useState<string>(ALL_PROJECTS_SCOPE)
  const [wikiViewMode, setWikiViewMode] = useState<WikiViewMode>('article')
  const [wikiOptionsOpen, setWikiOptionsOpen] = useState<boolean>(false)
  const [wikiBusy, setWikiBusy] = useState<boolean>(false)
  const [wikiError, setWikiError] = useState<string>('')
  const [wikiSearch, setWikiSearch] = useState<WikiSearchState>({ mode: 'search', query: '', searched: false, results: [], weakResults: [] })
  const [wikiRecall, setWikiRecall] = useState<RecallPreviewResponse | null>(null)
  const [wikiSelectedIds, setWikiSelectedIds] = useState<Set<string>>(new Set())
  const [wikiConsolidatedOpen, setWikiConsolidatedOpen] = useState<boolean>(false)
  const [wikiPinBusyIds, setWikiPinBusyIds] = useState<Set<string>>(new Set())
  const [wikiDeleteBusy, setWikiDeleteBusy] = useState<boolean>(false)
  const [wikiDiagramMemory, setWikiDiagramMemory] = useState<MemoryEntry | null>(null)
  const threadRef = useRef<HTMLDivElement>(null)
  const composerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent | TouchEvent) => {
      if (composerRef.current && !composerRef.current.contains(event.target as Node)) {
        setAdvancedOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    document.addEventListener('touchstart', handleClickOutside)
    return () => {
      document.removeEventListener('mousedown', handleClickOutside)
      document.removeEventListener('touchstart', handleClickOutside)
    }
  }, [])

  useEffect(() => {
    document.body.classList.remove('light', 'dark')
    document.body.classList.add(theme)
  }, [theme])

  useEffect(() => {
    if (threadRef.current) {
      threadRef.current.scrollTop = 0
    }
  }, [surface])

  useEffect(() => {
    if ((surface === 'search' || surface === 'recall') && threadRef.current) {
      threadRef.current.scrollTop = threadRef.current.scrollHeight
    }
  }, [messages, surface])

  useEffect(() => {
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (wikiDiagramMemory) {
        setWikiDiagramMemory(null)
        return
      }
      if (wikiConsolidatedOpen) {
        setWikiConsolidatedOpen(false)
        return
      }
      if (diagramPreviewOpen) {
        setDiagramPreviewOpen(false)
        return
      }
      if (deepSearchPrompt.open) {
        setDeepSearchPrompt({ open: false, query: '' })
        return
      }
      if (rawStatsOpen) {
        setRawStatsOpen(false)
        return
      }
      if (selectedMemory) {
        setSelectedMemory(null)
      }
    }
    window.addEventListener('keydown', handleEscape)
    return () => window.removeEventListener('keydown', handleEscape)
  }, [deepSearchPrompt.open, diagramPreviewOpen, rawStatsOpen, selectedMemory, wikiConsolidatedOpen, wikiDiagramMemory])

  const selectedProject = useMemo(() => projects.find((x) => x.name === workspace), [projects, workspace])

  const projectLabel = useMemo(() => {
    if (!selectedProject) return workspace || 'workspace'
    return `${selectedProject.name} (${selectedProject.memory_count} mem)`
  }, [selectedProject, workspace])

  const healthState = useMemo(() => getHealthState(stats, statsErr), [stats, statsErr])
  const sessionsUnavailable = useMemo(() => sessionsErr.toLowerCase().includes('route not enabled'), [sessionsErr])
  const selectedSession = useMemo(
    () => sessions.find((session) => session.session_id === selectedSessionID) ?? sessions[0],
    [selectedSessionID, sessions],
  )
  const dashboardWikiLauncher = surface !== 'wiki' && mode === 'search'
  const composerExpanded = composerFocused || advancedOpen || draft.trim().length > 0
  const semanticThreshold = useMemo(() => parseUnitScore(minSemantic) ?? SEARCH_DEFAULT_MIN_SEMANTIC_SCORE, [minSemantic])
  const semanticThresholdRelevance = useMemo(() => getSemanticRelevance(semanticThreshold), [semanticThreshold])
  const selectedSemanticSimilarity = useMemo(
    () => (selectedMemory ? getSemanticSimilarity(selectedMemory) : undefined),
    [selectedMemory],
  )
  const selectedSemanticRelevance = useMemo(
    () => getSemanticRelevance(selectedSemanticSimilarity),
    [selectedSemanticSimilarity],
  )

  const diagramMemories = useMemo(() => {
    const map = new Map<string, MemoryEntry>()
    for (const msg of messages) {
      if (msg.payload?.results) {
        for (const m of msg.payload.results) {
          if (hasDiagram(m)) map.set(m.id, m)
        }
      }
      if (msg.payload?.recall?.memories_included_full) {
        for (const m of msg.payload.recall.memories_included_full) {
          if (hasDiagram(m)) map.set(m.id, m)
        }
      }
    }
    return Array.from(map.values())
  }, [messages])
  const wikiAllFragments = useMemo(() => mergeMemoryResults([...wikiSearch.results, ...wikiSearch.weakResults]), [wikiSearch])
  const wikiPinnedResults = useMemo(() => wikiAllFragments.filter((memory) => memory.pinned), [wikiAllFragments])
  const wikiPinnedKeys = useMemo(() => new Set(wikiPinnedResults.map((memory) => buildMemoryKey(memory))), [wikiPinnedResults])
  const wikiMainResults = useMemo(
    () => wikiSearch.results.filter((memory) => !wikiPinnedKeys.has(buildMemoryKey(memory))),
    [wikiPinnedKeys, wikiSearch.results],
  )
  const wikiWeakResults = useMemo(
    () => wikiSearch.weakResults.filter((memory) => !wikiPinnedKeys.has(buildMemoryKey(memory))),
    [wikiPinnedKeys, wikiSearch.weakResults],
  )
  const wikiSelectedFragments = useMemo(
    () => wikiAllFragments.filter((memory) => wikiSelectedIds.has(buildMemoryKey(memory))),
    [wikiAllFragments, wikiSelectedIds],
  )

  useEffect(() => {
    let cancelled = false
    listProjects()
      .then((r) => {
        if (cancelled) return
        setProjects(r.projects ?? [])
        setWorkspace((prev) => prev || (r.projects?.[0]?.name ?? ''))
      })
      .catch(() => {
        if (cancelled) return
        setProjects([])
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    if (!workspace) return
    setStatsErr('')
    getStats(workspace)
      .then((s) => {
        if (cancelled) return
        setStats(s)
      })
      .catch((e) => {
        if (cancelled) return
        setStats(null)
        setStatsErr(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [workspace])

  useEffect(() => {
    if (!workspace) return
    setSelectedSessionID('')
    setObservations([])
    setObservationsErr('')
    setPromotionResults({})
    setBenchmarkRuns([])
    setBenchmarkErr('')
  }, [workspace])

  useEffect(() => {
    let cancelled = false
    if (!workspace) return
    setBenchmarkBusy(true)
    setBenchmarkErr('')
    listBenchmarkRuns({ workspace, limit: 12 })
      .then((response) => {
        if (cancelled) return
        setBenchmarkRuns(response.runs ?? [])
      })
      .catch((e) => {
        if (cancelled) return
        setBenchmarkRuns([])
        setBenchmarkErr(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (cancelled) return
        setBenchmarkBusy(false)
      })
    return () => {
      cancelled = true
    }
  }, [workspace])

  useEffect(() => {
    let cancelled = false
    if (!workspace) return
    setSessionsBusy(true)
    setSessionsErr('')
    listSessions({ workspace, limit: 12 })
      .then((response) => {
        if (cancelled) return
        setSessions(response.sessions ?? [])
      })
      .catch((e) => {
        if (cancelled) return
        setSessions([])
        setSessionsErr(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (cancelled) return
        setSessionsBusy(false)
      })
    return () => {
      cancelled = true
    }
  }, [workspace])

  useEffect(() => {
    if (!sessions.length) {
      setSelectedSessionID('')
      return
    }
    setSelectedSessionID((current) => {
      if (current && sessions.some((session) => session.session_id === current)) return current
      return sessions[0]?.session_id ?? ''
    })
  }, [sessions])

  useEffect(() => {
    let cancelled = false
    if (!workspace || !selectedSessionID) {
      setObservations([])
      setObservationsErr('')
      return
    }
    setObservationsBusy(true)
    setObservationsErr('')
    listObservations({ workspace, session_id: selectedSessionID, limit: 80 })
      .then((response) => {
        if (cancelled) return
        setObservations(response.observations ?? [])
      })
      .catch((e) => {
        if (cancelled) return
        setObservations([])
        setObservationsErr(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (cancelled) return
        setObservationsBusy(false)
      })
    return () => {
      cancelled = true
    }
  }, [selectedSessionID, workspace])

  const filters = useMemo(() => {
    const parsedEntities = entities
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    return {
      types: Array.from(types),
      tiers: Array.from(tiers),
      outcome_result: outcome || undefined,
      min_confidence: minConfidence.trim() ? Number(minConfidence) : undefined,
      min_decay_score: minDecay.trim() ? Number(minDecay) : undefined,
      min_semantic_score: semanticThreshold,
      min_total_score: minTotal.trim() ? Number(minTotal) : undefined,
      relative_cutoff: relativeCutoff.trim() ? Number(relativeCutoff) : undefined,
      entities: parsedEntities.length ? parsedEntities : undefined,
      date_from: fromDate || undefined,
      date_to: toDate || undefined,
    }
  }, [entities, fromDate, minConfidence, minDecay, minTotal, outcome, relativeCutoff, semanticThreshold, tiers, toDate, types])

  function openSearch() {
    setMode('search')
    setWikiMode('search')
    setSurface('wiki')
    setSelectedMemory(null)
  }

  function openRecall() {
    setMode('recall')
    setWikiMode('recall')
    setSurface('wiki')
    setSelectedMemory(null)
  }

  function openSessions() {
    setSurface('sessions')
    setSelectedMemory(null)
  }

  function openBenchmark() {
    setSurface('benchmark')
    setSelectedMemory(null)
  }

  function openWiki(modeOverride?: WikiMode) {
    if (modeOverride) {
      setWikiMode(modeOverride)
      if (modeOverride === 'search' || modeOverride === 'recall') {
        setMode(modeOverride)
      }
    }
    setSurface('wiki')
    setSelectedMemory(null)
  }

  function resetWikiTransientState() {
    setWikiError('')
    setSelectedMemory(null)
    setWikiSelectedIds(new Set())
    setWikiConsolidatedOpen(false)
  }

  async function promoteSelectedSession(type: MemoryType = 'episodic') {
    if (!workspace || !selectedSession) return
    setPromotionBusyFor(selectedSession.session_id)
    try {
      const result = await promoteObservations({
        workspace,
        session_id: selectedSession.session_id,
        max_items: 200,
        type,
      })
      setPromotionResults((current) => ({
        ...current,
        [selectedSession.session_id]: result,
      }))
    } catch (e) {
      setPromotionResults((current) => ({
        ...current,
        [selectedSession.session_id]: {
          workspace,
          session_id: selectedSession.session_id,
          requested_type: type,
          observations: observations.length,
          created_id: '',
          deduplicated: false,
          rejected: true,
          reject_reason: e instanceof Error ? e.message : String(e),
        },
      }))
    } finally {
      setPromotionBusyFor('')
    }
  }

  async function showRecentsCapture() {
    if (recentsBusy) return
    openWiki('recents')
    setRecentsBusy(true)
    resetWikiTransientState()
    try {
      const limit = Math.max(1, topK)
      let recentResults: MemoryEntry[] = []
      if (wikiScope === ALL_PROJECTS_SCOPE) {
        const targets = projects.map((project) => project.name).filter(Boolean)
        if (targets.length === 0) throw new Error('No projects available to load recents.')
        const responses = await Promise.all(targets.map((projectName) => listRecentMemories({ workspace: projectName, limit })))
        recentResults = responses.flatMap((response) => response.results ?? [])
      } else {
        const targetWorkspace = (wikiScope || workspace).trim()
        if (!targetWorkspace) throw new Error('No project selected for recents.')
        const response = await listRecentMemories({ workspace: targetWorkspace, limit })
        recentResults = response.results ?? []
      }
      const mergedResults = mergeMemoryResults(recentResults).sort(compareMemoryRecency).slice(0, limit)
      setWikiSearch({
        mode: 'recents',
        query: 'recent memories',
        searched: true,
        results: mergedResults,
        weakResults: [],
      })
      setWikiRecall(null)
      threadRef.current?.scrollTo({ top: 0, behavior: 'smooth' })
    } catch (e) {
      setWikiSearch({ mode: 'recents', query: 'recent memories', searched: true, results: [], weakResults: [] })
      setWikiError(e instanceof Error ? e.message : String(e))
    } finally {
      setRecentsBusy(false)
    }
  }

  async function runSearchFlow(query: string) {
    openSearch()
    await runWikiSearch(query)
  }

  async function runRecallFlow(task: string) {
    openRecall()
    await runWikiRecall(task)
  }

  async function runDeepSearch(query: string) {
    const q = query.trim()
    if (!q) return
    openRecall()
    setDeepSearchPrompt({ open: false, query: '' })
    await runWikiRecall(q)
  }

  async function submit() {
    const text = draft.trim()
    if (!text) return
    setDraft('')
    if (mode === 'search') {
      await runSearchFlow(text)
      return
    }
    await runRecallFlow(text)
  }

  function focusExperimentComparisons() {
    setSurface('overview')
    setOverviewExperimentFocusKey((current) => current + 1)
  }

  function openGuidedDiagrams() {
    if (diagramMemories.length > 0) {
      if (diagramMemories.length === 1) {
        setSelectedMemory(diagramMemories[0])
      } else {
        setDiagramPreviewOpen(true)
      }
      return
    }
    void runSearchFlow('architecture diagram mermaid flow')
  }

  function toggleWikiSelection(memory: MemoryEntry) {
    const key = buildMemoryKey(memory)
    setWikiSelectedIds((current) => {
      const next = new Set(current)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  function applyWikiMemoryUpdate(updatedMemory: MemoryEntry) {
    const apply = (items: MemoryEntry[]) => items.map((memory) => (buildMemoryKey(memory) === buildMemoryKey(updatedMemory) ? updatedMemory : memory))
    setWikiSearch((current) => ({
      ...current,
      results: apply(current.results),
      weakResults: apply(current.weakResults),
    }))
    setSelectedMemory((current) => (current && buildMemoryKey(current) === buildMemoryKey(updatedMemory) ? updatedMemory : current))
  }

  function removeWikiMemories(deletedKeys: Set<string>) {
    setWikiSearch((current) => ({
      ...current,
      results: current.results.filter((memory) => !deletedKeys.has(buildMemoryKey(memory))),
      weakResults: current.weakResults.filter((memory) => !deletedKeys.has(buildMemoryKey(memory))),
    }))
    setWikiSelectedIds((current) => {
      const next = new Set(current)
      for (const key of deletedKeys) next.delete(key)
      return next
    })
    setSelectedMemory((current) => (current && deletedKeys.has(buildMemoryKey(current)) ? null : current))
  }

  async function toggleWikiPin(memory: MemoryEntry) {
    const key = buildMemoryKey(memory)
    if (wikiPinBusyIds.has(key)) return
    setWikiPinBusyIds((current) => new Set(current).add(key))
    try {
      const response = await setMemoryPinned({
        workspace: memory.workspace,
        memory_id: memory.id,
        pinned: !memory.pinned,
      })
      applyWikiMemoryUpdate(response.updated_memory)
    } catch (e) {
      setWikiError(e instanceof Error ? e.message : String(e))
    } finally {
      setWikiPinBusyIds((current) => {
        const next = new Set(current)
        next.delete(key)
        return next
      })
    }
  }

  async function runWikiSearch(queryOverride?: string) {
    const text = (queryOverride ?? wikiQuery).trim()
    if (!text) return
    const targetWorkspace = wikiScope === ALL_PROJECTS_SCOPE ? ALL_PROJECTS_SCOPE : (wikiScope || workspace).trim()
    if (!targetWorkspace) {
      setWikiError('No projects available to search.')
      return
    }
    openWiki('search')
    setWikiBusy(true)
    resetWikiTransientState()
    setWikiQuery(text)
    try {
      const response = await searchMemories({
        workspace: targetWorkspace,
        query: text,
        top_k: topK,
        explain,
        filters,
      })
      const mergedResults = mergeMemoryResults(response.results ?? [])
      const resultKeys = new Set(mergedResults.map((memory) => buildMemoryKey(memory)))
      const mergedWeak = mergeMemoryResults((response.weak_results ?? []).filter((memory) => !resultKeys.has(buildMemoryKey(memory))))
      setWikiSearch({ mode: 'search', query: text, searched: true, results: mergedResults, weakResults: mergedWeak })
      setWikiRecall(null)
      threadRef.current?.scrollTo({ top: 0, behavior: 'smooth' })
    } catch (e) {
      setWikiSearch({ mode: 'search', query: text, searched: true, results: [], weakResults: [] })
      setWikiError(e instanceof Error ? e.message : String(e))
    } finally {
      setWikiBusy(false)
    }
  }

  async function runWikiRecall(taskOverride?: string) {
    const text = (taskOverride ?? wikiQuery).trim()
    if (!text) return
    const targetWorkspace = (wikiScope === ALL_PROJECTS_SCOPE ? workspace : wikiScope || workspace).trim()
    if (!targetWorkspace) {
      setWikiError('No project selected for recall.')
      return
    }
    if (wikiScope === ALL_PROJECTS_SCOPE) {
      setWikiError('Recall preview currently requires a single project scope.')
      return
    }
    openWiki('recall')
    setWikiBusy(true)
    resetWikiTransientState()
    setWikiQuery(text)
    try {
      const response = await recallPreview({
        workspace: targetWorkspace,
        task_description: text,
        top_k: recallTopK,
        token_budget: budget,
        explain,
        include_memories: true,
      })
      const mergedResults = mergeMemoryResults(response.memories_included_full ?? [])
      const resultKeys = new Set(mergedResults.map((memory) => buildMemoryKey(memory)))
      const mergedWeak = mergeMemoryResults((response.weak_memories ?? []).filter((memory) => !resultKeys.has(buildMemoryKey(memory))))
      setWikiSearch({ mode: 'recall', query: text, searched: true, results: mergedResults, weakResults: mergedWeak })
      setWikiRecall(response)
      threadRef.current?.scrollTo({ top: 0, behavior: 'smooth' })
    } catch (e) {
      setWikiSearch({ mode: 'recall', query: text, searched: true, results: [], weakResults: [] })
      setWikiRecall(null)
      setWikiError(e instanceof Error ? e.message : String(e))
    } finally {
      setWikiBusy(false)
    }
  }

  async function exportWikiSelection() {
    if (wikiSelectedFragments.length === 0) return
    try {
      const html = await buildConsolidatedExportHTML(wikiSelectedFragments, theme)
      const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `wiki-selection-${Date.now()}.html`
      anchor.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      setWikiError(e instanceof Error ? e.message : String(e))
    }
  }

  async function printWikiSelection() {
    if (wikiSelectedFragments.length === 0) return
    try {
      const popup = window.open('', '_blank', 'noopener,noreferrer,width=960,height=720')
      if (!popup) return
      const html = (await buildConsolidatedExportHTML(wikiSelectedFragments, theme)).replace(/^<!doctype html>\s*/i, '')
      popup.document.open()
      popup.document.documentElement.innerHTML = html
      popup.document.close()
      popup.focus()
      popup.print()
    } catch (e) {
      setWikiError(e instanceof Error ? e.message : String(e))
    }
  }

  async function deleteWikiSelection() {
    if (wikiDeleteBusy || wikiSelectedFragments.length === 0) return
    const confirmed = window.confirm(
      `Delete ${wikiSelectedFragments.length} selected memor${wikiSelectedFragments.length === 1 ? 'y' : 'ies'}? This cannot be undone.`,
    )
    if (!confirmed) return
    setWikiDeleteBusy(true)
    setWikiError('')
    try {
      const grouped = new Map<string, string[]>()
      for (const memory of wikiSelectedFragments) {
        const current = grouped.get(memory.workspace) ?? []
        current.push(memory.id)
        grouped.set(memory.workspace, current)
      }
      await Promise.all(
        Array.from(grouped.entries()).map(([targetWorkspace, memoryIDs]) =>
          deleteMemories({
            workspace: targetWorkspace,
            memory_ids: memoryIDs,
          }),
        ),
      )
      removeWikiMemories(new Set(wikiSelectedFragments.map((memory) => buildMemoryKey(memory))))
    } catch (e) {
      setWikiError(e instanceof Error ? e.message : String(e))
    } finally {
      setWikiDeleteBusy(false)
    }
  }

  function clearWikiView() {
    setWikiQuery('')
    setWikiError('')
    setWikiSearch({ mode: wikiMode, query: '', searched: false, results: [], weakResults: [] })
    setWikiRecall(null)
    setWikiSelectedIds(new Set())
    setWikiConsolidatedOpen(false)
    setSelectedMemory(null)
    setWikiDiagramMemory(null)
    setWikiOptionsOpen(false)
  }

  return (
    <div className={surface === 'wiki' ? 'shell chatShell shellWikiMode' : 'shell chatShell'}>
      {surface !== 'wiki' ? (
        <header className="topbar chatTopbar">
        <div className="topbarLeft">
          <div className="brand">
            <div className="brandMark" aria-hidden="true">
              <span className="brandMarkText">[+]</span>
            </div>
            <div className="brandText">
              <div className="brandTitle">
                agent-memory/dashboard <span className="brandVersion">v0.7</span>
              </div>
            </div>
          </div>
          <select
            className="projectSelect"
            value={workspace}
            onChange={(e) => {
              setWorkspace(e.target.value)
              setSelectedMemory(null)
            }}
            aria-label="Switch workspace"
          >
            {projects.length === 0 ? <option value="">(no workspaces)</option> : null}
            {projects.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name} ({p.memory_count})
              </option>
            ))}
          </select>
        </div>

        <nav className="topbarCenter" aria-label="Mode switcher">
          <button className={surface === 'overview' ? 'navItem navItemOn' : 'navItem'} onClick={() => setSurface('overview')} type="button">
            <span className="navKey">[01]</span>
            <span className="navLabel">Overview</span>
          </button>
          <button className={surface === 'sessions' ? 'navItem navItemOn' : 'navItem'} onClick={openSessions} type="button" aria-label="Sessions">
            <span className="navKey">[02]</span>
            <span className="navLabel">Sessions</span>
          </button>
          <button className={surface === 'diagnostics' ? 'navItem navItemOn' : 'navItem'} onClick={() => setSurface('diagnostics')} type="button" aria-label="Diagnostics">
            <span className="navKey">[03]</span>
            <span className="navLabel">Diagnostics</span>
          </button>
          <button className={surface === 'benchmark' ? 'navItem navItemOn' : 'navItem'} onClick={openBenchmark} type="button" aria-label="Benchmark">
            <span className="navKey">[04]</span>
            <span className="navLabel">Benchmark</span>
          </button>
          <button className="navItem" onClick={() => openWiki()} type="button" aria-label="Wiki">
            <span className="navKey">[05]</span>
            <span className="navLabel">Wiki</span>
          </button>
        </nav>

        <div className="topbarRight">
          <button
            className="iconBtn iconBtnInfo"
            onClick={() => setRawStatsOpen(true)}
            aria-label="Raw stats payload"
            title="Raw stats payload"
          >
            <span>raw</span>
          </button>
          <button
            className="iconBtn"
            onClick={() => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))}
            title={theme === 'dark' ? 'Switch to Light Mode' : 'Switch to Dark Mode'}
            aria-label={theme === 'dark' ? 'Switch to Light Mode' : 'Switch to Dark Mode'}
          >
            <span>{theme === 'dark' ? 'light' : 'dark'}</span>
          </button>
        </div>
        </header>
      ) : null}

      <div className="chatLayout">
        <main className="chatMain">
          <div className="chatFeed">
            {diagramMemories.length > 0 && (surface === 'search' || surface === 'recall') ? (
              <button
                className="floatingDiagramBtn"
                onClick={() => {
                  if (diagramMemories.length === 1) {
                    setSelectedMemory(diagramMemories[0])
                  } else {
                    setDiagramPreviewOpen(true)
                  }
                }}
                aria-label="View diagrams"
                title="Quick access to diagrams"
              >
                <span>[diagrams]</span>
                <span className="floatingDiagramBadge">{diagramMemories.length}</span>
              </button>
            ) : null}

            <div className="thread" ref={threadRef}>
              {surface === 'overview' ? (
                <OverviewPanel
                  workspace={workspace}
                  project={selectedProject}
                  stats={stats}
                  statsErr={statsErr}
                  healthState={healthState}
                  diagramCount={diagramMemories.length}
                  experimentFocusKey={overviewExperimentFocusKey}
                  onCompareRuns={focusExperimentComparisons}
                  onInspectFailures={() => void runSearchFlow('recent failures errors regressions')}
                  onReviewLastSession={openSessions}
                  onRunDiagramAction={openGuidedDiagrams}
                />
              ) : null}

              {surface === 'sessions' ? (
                <SessionsPanel
                  workspace={workspace}
                  sessions={sessions}
                  sessionsBusy={sessionsBusy}
                  sessionsErr={sessionsErr}
                  sessionsUnavailable={sessionsUnavailable}
                  selectedSessionID={selectedSession?.session_id ?? ''}
                  observations={observations}
                  observationsBusy={observationsBusy}
                  observationsErr={observationsErr}
                  promotionResult={selectedSession ? promotionResults[selectedSession.session_id] : undefined}
                  promotionBusy={Boolean(selectedSession && promotionBusyFor === selectedSession.session_id)}
                  onSelectSession={setSelectedSessionID}
                  onPromote={promoteSelectedSession}
                />
              ) : null}

              {surface === 'diagnostics' ? (
                <DiagnosticsPanel
                  workspaceLabel={projectLabel}
                  project={selectedProject}
                  stats={stats}
                  statsErr={statsErr}
                  healthState={healthState}
                  onOpenRaw={() => setRawStatsOpen(true)}
                />
              ) : null}

              {surface === 'benchmark' ? (
                <BenchmarkPanel
                  workspace={workspace}
                  runs={benchmarkRuns}
                  busy={benchmarkBusy}
                  error={benchmarkErr}
                />
              ) : null}

              {surface === 'wiki' ? (
                <WikiPanel
                  theme={theme}
                  workspace={workspace}
                  projects={projects}
                  mode={wikiMode}
                  query={wikiQuery}
                  scope={wikiScope}
                  viewMode={wikiViewMode}
                  optionsOpen={wikiOptionsOpen}
                  searched={wikiSearch.searched}
                  busy={wikiBusy}
                  error={wikiError}
                  results={wikiMainResults}
                  pinnedResults={wikiPinnedResults}
                  weakResults={wikiWeakResults}
                  recall={wikiRecall}
                  recentsBusy={recentsBusy}
                  selectedCount={wikiSelectedFragments.length}
                  selectedIds={wikiSelectedIds}
                  explain={explain}
                  topK={topK}
                  recallTopK={recallTopK}
                  budget={budget}
                  semanticThreshold={semanticThreshold}
                  minTotal={minTotal}
                  minConfidence={minConfidence}
                  outcome={outcome}
                  types={types}
                  tiers={tiers}
                  onQueryChange={setWikiQuery}
                  onModeChange={(nextMode: WikiMode) => {
                    setWikiMode(nextMode)
                    setWikiError('')
                    setWikiSearch((current) => (current.mode === nextMode ? current : { ...current, mode: nextMode, searched: false }))
                    if (nextMode !== 'recall') setWikiRecall(null)
                    if (nextMode === 'search') setMode('search')
                    if (nextMode === 'recall') setMode('recall')
                    if (nextMode === 'recents') void showRecentsCapture()
                  }}
                  onScopeChange={setWikiScope}
                  onViewModeChange={setWikiViewMode}
                  onExitWiki={() => setSurface('overview')}
                  onOpenRaw={() => setRawStatsOpen(true)}
                  onToggleTheme={() => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))}
                  onClearView={clearWikiView}
                  onToggleOptions={() => setWikiOptionsOpen((current) => !current)}
                  onSubmit={() => {
                    if (wikiMode === 'recall') {
                      void runWikiRecall()
                      return
                    }
                    if (wikiMode === 'recents') {
                      void showRecentsCapture()
                      return
                    }
                    void runWikiSearch()
                  }}
                  onSuggestion={(query) => void runWikiSearch(query)}
                  onToggleSelection={toggleWikiSelection}
                  onOpenMemory={setSelectedMemory}
                  onOpenDiagram={setWikiDiagramMemory}
                  onTogglePin={(memory) => void toggleWikiPin(memory)}
                  isPinned={(memory) => memory.pinned}
                  isPinBusy={(memory) => wikiPinBusyIds.has(buildMemoryKey(memory))}
                  onOpenConsolidated={() => setWikiConsolidatedOpen(true)}
                  onDownloadSelection={exportWikiSelection}
                  onPrintSelection={printWikiSelection}
                  onDeleteSelection={() => void deleteWikiSelection()}
                  deleteBusy={wikiDeleteBusy}
                  onSetMinSemantic={(value) => setMinSemantic(value.toFixed(2))}
                  onSetExplain={setExplain}
                  onSetTopK={setTopK}
                  onSetRecallTopK={setRecallTopK}
                  onSetBudget={setBudget}
                  onSetMinTotal={setMinTotal}
                  onSetMinConfidence={setMinConfidence}
                  onSetOutcome={setOutcome}
                  onToggleType={(memoryType, checked) => {
                    const next = new Set(types)
                    if (checked) next.add(memoryType)
                    else next.delete(memoryType)
                    setTypes(next)
                  }}
                  onToggleTier={(tier, checked) => {
                    const next = new Set(tiers)
                    if (checked) next.add(tier)
                    else next.delete(tier)
                    setTiers(next)
                  }}
                  onCollapseOptions={() => setWikiOptionsOpen(false)}
                />
              ) : null}

              {(surface === 'search' || surface === 'recall') && messages.length === 0 ? (
                <QueryEmptyState mode={mode} onOpenOverview={() => setSurface('overview')} />
              ) : null}

              {(surface === 'search' || surface === 'recall') &&
                messages.map((m) => (
                  <Message
                    key={m.id}
                    m={m}
                    theme={theme}
                    selectedId={selectedMemory?.id}
                    onSelectMemory={setSelectedMemory}
                  />
                ))}
            </div>

            {surface !== 'wiki' ? (
              <div
                className={dashboardWikiLauncher ? 'composerDock composerDockLauncher' : (composerExpanded ? 'composerDock composerDockExpanded' : 'composerDock composerDockCollapsed')}
                ref={composerRef}
                onFocusCapture={() => setComposerFocused(true)}
                onBlurCapture={(e) => {
                  const nextTarget = e.relatedTarget
                  if (nextTarget instanceof Node && composerRef.current?.contains(nextTarget)) return
                  setComposerFocused(false)
                  if (!draft.trim()) setAdvancedOpen(false)
                }}
              >
                {dashboardWikiLauncher ? (
                  <button className="composerWikiLauncherBtn" type="button" onClick={() => openWiki('search')}>
                    Explore wiki
                  </button>
                ) : (
                  <div className={composerExpanded ? 'composer composerExpanded' : 'composer composerCollapsed'}>
                    {advancedOpen ? (
                      <div className="composerAdvanced">
                        <div className="composerRow">
                          <div className="composerRowTitle">Mode</div>
                          <div className="modePills modePillsInline">
                            <button className={mode === 'search' ? 'modePill modePillOn' : 'modePill'} onClick={openSearch} type="button">
                              Search
                            </button>
                            <button className={mode === 'recall' ? 'modePill modePillOn' : 'modePill'} onClick={openRecall} type="button">
                              Recall Preview
                            </button>
                          </div>
                        </div>
                        <label className="check">
                          <input type="checkbox" checked={explain} onChange={(e) => setExplain(e.target.checked)} />
                          Explain scoring
                        </label>
                        {mode === 'search' ? (
                          <>
                            <div className="row row2">
                              <div>
                                <label className="label">Top K</label>
                                <input className="input" type="number" min={1} max={200} value={topK} onChange={(e) => setTopK(Number(e.target.value))} />
                              </div>
                              <div>
                                <label className="label">Outcome</label>
                                <select className="input" value={outcome} onChange={(e) => setOutcome(e.target.value as OutcomeResult | '')}>
                                  <option value="">any</option>
                                  <option value="success">success</option>
                                  <option value="failure">failure</option>
                                  <option value="partial">partial</option>
                                </select>
                              </div>
                            </div>

                            <label className="label">Types</label>
                            <div className="chips">
                              {allTypes.map((t) => (
                                <label key={t.key} className={types.has(t.key) ? 'chip chipOn' : 'chip'}>
                                  <input
                                    type="checkbox"
                                    checked={types.has(t.key)}
                                    onChange={(e) => {
                                      const next = new Set(types)
                                      if (e.target.checked) next.add(t.key)
                                      else next.delete(t.key)
                                      setTypes(next)
                                    }}
                                  />
                                  {t.label}
                                </label>
                              ))}
                            </div>

                            <label className="label">Tiers</label>
                            <div className="chips">
                              {allTiers.map((t) => (
                                <label key={t.key} className={tiers.has(t.key) ? 'chip chipOn' : 'chip'}>
                                  <input
                                    type="checkbox"
                                    checked={tiers.has(t.key)}
                                    onChange={(e) => {
                                      const next = new Set(tiers)
                                      if (e.target.checked) next.add(t.key)
                                      else next.delete(t.key)
                                      setTiers(next)
                                    }}
                                  />
                                  {t.label}
                                </label>
                              ))}
                            </div>

                            <div className="row row2">
                              <div>
                                <label className="label">Min confidence</label>
                                <input className="input" inputMode="decimal" value={minConfidence} onChange={(e) => setMinConfidence(e.target.value)} placeholder="0.00 - 1.00" />
                              </div>
                              <div>
                                <label className="label">Min decay</label>
                                <input className="input" inputMode="decimal" value={minDecay} onChange={(e) => setMinDecay(e.target.value)} placeholder="0.00 - 1.00" />
                              </div>
                            </div>

                            <div className="semanticFilterCard">
                              <div className="semanticFilterHeader">
                                <div>
                                  <label className="label" htmlFor="min-semantic-score">
                                    Min semantic score
                                  </label>
                                  <div className="semanticFilterHint">
                                    Search defaults to `0.30`. Raise it for stricter relevance or lower it only when diagnosing weak matches.
                                  </div>
                                </div>
                                <button
                                  className="btn btnGhost semanticPresetReset"
                                  type="button"
                                  onClick={() => setMinSemantic(SEARCH_DEFAULT_MIN_SEMANTIC_SCORE.toFixed(2))}
                                >
                                  reset 0.30
                                </button>
                              </div>
                              <input
                                id="min-semantic-score"
                                className="semanticSlider"
                                type="range"
                                min={0}
                                max={1}
                                step={0.05}
                                value={semanticThreshold}
                                onChange={(e) => setMinSemantic(Number(e.target.value).toFixed(2))}
                              />
                              <div className="semanticFilterSummary">
                                <div className="semanticThresholdValue">{semanticThreshold.toFixed(2)}</div>
                                <div className="semanticThresholdCopy">
                                  <div className="semanticThresholdLabel">Active search floor</div>
                                  <div className="semanticThresholdHint">Sent to backend as `min_semantic_score`.</div>
                                </div>
                                <span className={`memPill relevancePill relevancePill${toTitle(semanticThresholdRelevance.tone)}`}>
                                  {semanticThresholdRelevance.label}
                                </span>
                              </div>
                              <div className="semanticPresetRow">
                                {semanticFloorPresets.map((preset) => (
                                  <button
                                    key={preset.label}
                                    className={semanticThreshold === preset.value ? 'semanticPreset semanticPresetOn' : 'semanticPreset'}
                                    type="button"
                                    onClick={() => setMinSemantic(preset.value.toFixed(2))}
                                  >
                                    {preset.label}
                                  </button>
                                ))}
                              </div>
                              <div className="semanticFilterScale">
                                Weak &lt; 0.30 | Low 0.30+ | Medium 0.40+ | High 0.55+
                              </div>
                            </div>

                            <div className="row row2">
                              <div>
                                <label className="label">Min total</label>
                                <input className="input" inputMode="decimal" value={minTotal} onChange={(e) => setMinTotal(e.target.value)} placeholder="0.00 - 1.00" />
                              </div>
                              <div>
                                <label className="label">Relative cutoff</label>
                                <input className="input" inputMode="decimal" value={relativeCutoff} onChange={(e) => setRelativeCutoff(e.target.value)} placeholder="0.00 - 1.00" />
                              </div>
                            </div>

                            <label className="label">Entities (comma-separated)</label>
                            <input className="input" value={entities} onChange={(e) => setEntities(e.target.value)} placeholder="orders, kafka, schema" />

                            <div className="row row2">
                              <div>
                                <label className="label">From</label>
                                <input className="input" type="date" value={fromDate} onChange={(e) => setFromDate(e.target.value)} />
                              </div>
                              <div>
                                <label className="label">To</label>
                                <input className="input" type="date" value={toDate} onChange={(e) => setToDate(e.target.value)} />
                              </div>
                            </div>
                          </>
                        ) : (
                          <div className="row row2">
                            <div>
                              <label className="label">Top K</label>
                              <input className="input" type="number" min={1} max={500} value={recallTopK} onChange={(e) => setRecallTopK(Number(e.target.value))} />
                            </div>
                            <div>
                              <label className="label">Budget</label>
                              <input className="input" type="number" min={1} value={budget} onChange={(e) => setBudget(Number(e.target.value))} />
                            </div>
                          </div>
                        )}
                      </div>
                    ) : null}

                    <textarea
                      className="composerInput"
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      onFocus={() => setInputFocused(true)}
                      onBlur={() => setInputFocused(false)}
                      placeholder={mode === 'search' ? 'Explore wiki...' : 'Describe the task to recall...'}
                      rows={inputFocused || composerFocused || draft.trim().length > 0 || advancedOpen ? 3 : 1}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && !e.shiftKey) {
                          e.preventDefault()
                          submit()
                        }
                      }}
                    />
                    <div className={composerExpanded ? 'composerToolbar' : 'composerToolbar composerToolbarCollapsed'}>
                      <div className="composerToolbarLeft">
                        <span className="muted small">
                          {surface === 'overview'
                            ? 'Ready to explore wiki.'
                            : surface === 'diagnostics'
                              ? 'Diagnostics open.'
                              : surface === 'sessions'
                                ? 'Sessions open.'
                                : 'Searching this workspace.'}
                        </span>
                      </div>
                      <div className="composerToolbarRight">
                        <button className="btn btnGhost" type="button" onClick={() => setAdvancedOpen((v) => !v)}>
                          {advancedOpen ? '[-] filters' : '[+] filters'}
                        </button>
                        <button className="sendBtn" type="button" onClick={submit} disabled={!workspace || busy || !draft.trim()} title="Send query">
                          <span className="sendBtnLabel">RUN</span>
                        </button>
                      </div>
                    </div>
                  </div>
                )}
                <div className={composerExpanded ? 'composerFoot' : 'composerFoot composerFootCollapsed'}>
                  <span className="muted small" style={{ display: 'block', textAlign: 'center' }}>
                    Served locally by <span className="mono">agent-memory serve</span>. Markdown is sanitized; Mermaid renders when present.
                  </span>
                </div>
              </div>
            ) : null}
          </div>

          {selectedMemory ? (
            <aside className="detailDrawer" aria-label="Memory details">
              <div className="detailDrawerTop">
                <div className="detailDrawerHeader">
                  <div className="detailDrawerTitle">Memory Details</div>
                  <div className="mono detailDrawerID">{selectedMemory.id}</div>
                </div>
                <button className="btn btnGhost" onClick={() => setSelectedMemory(null)} aria-label="Close details" style={{ padding: '8px 12px' }}>
                  [x]
                </button>
              </div>

              <div className="detailDrawerBody">
                <div className="detailPills">
                  <span className="memPill">{selectedMemory.type}</span>
                  <span className="memPill">{selectedMemory.storage_tier}</span>
                  {selectedMemory.band ? <span className="memPill memPillAccent">{selectedMemory.band}</span> : null}
                  {typeof selectedSemanticSimilarity === 'number' ? (
                    <span className={`memPill relevancePill relevancePill${toTitle(selectedSemanticRelevance.tone)}`}>
                      {selectedSemanticRelevance.label} semantic {formatScore(selectedSemanticSimilarity, 2)}
                    </span>
                  ) : null}
                </div>

                <div className="detailSection">
                  <div className="detailSectionTitle">Content</div>
                  <div className="detailContentCard">
                    <MarkdownView markdown={selectedMemory.content} clamp={false} theme={theme} />
                    {selectedMemory.diagram ? (
                      <div className="diagramBlock">
                        <DiagramViewer diagram={selectedMemory.diagram} theme={theme} />
                      </div>
                    ) : null}
                  </div>
                </div>

                <div className="detailSection">
                  <div className="detailSectionTitle">Memory Facts</div>
                  <div className="memMetaGrid">
                    {typeof selectedSemanticSimilarity === 'number' ? (
                      <div className="memMeta memMetaSemantic">
                        <div className="memMetaLabel">Semantic Similarity</div>
                        <div className="memMetaValue memMetaPrimaryValue">{formatScore(selectedSemanticSimilarity, 3)}</div>
                      </div>
                    ) : null}
                    {typeof selectedSemanticSimilarity === 'number' ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Relevance</div>
                        <div className="memMetaValue">
                          <span className={`memPill relevancePill relevancePill${toTitle(selectedSemanticRelevance.tone)}`}>{selectedSemanticRelevance.label}</span>
                        </div>
                      </div>
                    ) : null}
                    <div className="memMeta">
                      <div className="memMetaLabel">Updated</div>
                      <div className="memMetaValue">{formatTS(selectedMemory.updated_at)}</div>
                    </div>
                    <div className="memMeta">
                      <div className="memMetaLabel">Created</div>
                      <div className="memMetaValue">{formatTS(selectedMemory.created_at)}</div>
                    </div>
                    <div className="memMeta">
                      <div className="memMetaLabel">Confidence</div>
                      <div className="memMetaValue">{selectedMemory.confidence.toFixed(2)}</div>
                    </div>
                    {typeof selectedMemory.score === 'number' ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Blended Score</div>
                        <div className="memMetaValue">{formatScore(selectedMemory.score, 3)}</div>
                      </div>
                    ) : null}
                    {typeof selectedMemory.salience_score === 'number' ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Salience</div>
                        <div className="memMetaValue">{selectedMemory.salience_score.toFixed(2)}</div>
                      </div>
                    ) : null}
                    {typeof selectedMemory.suppression_score === 'number' ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Suppression</div>
                        <div className="memMetaValue">{selectedMemory.suppression_score.toFixed(2)}</div>
                      </div>
                    ) : null}
                    {typeof selectedMemory.access_count === 'number' ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Retrieved</div>
                        <div className="memMetaValue">{formatNumber(selectedMemory.access_count)} times</div>
                      </div>
                    ) : null}
                    {selectedMemory.exclusion_reasons?.length ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Excluded By</div>
                        <div className="memMetaValue">{pillList(selectedMemory.exclusion_reasons)}</div>
                      </div>
                    ) : null}
                    <div className="memMeta">
                      <div className="memMetaLabel">Entities</div>
                      <div className="memMetaValue">{pillList(selectedMemory.entities ?? [])}</div>
                    </div>
                    <div className="memMeta">
                      <div className="memMetaLabel">Tags</div>
                      <div className="memMetaValue">{pillList(selectedMemory.tags ?? [])}</div>
                    </div>
                  </div>
                </div>

                {selectedMemory.score_breakdown ? (
                  <div className="detailSection">
                    <div className="detailSectionTitle">Score Breakdown</div>
                    <pre className="pre">{JSON.stringify(selectedMemory.score_breakdown, null, 2)}</pre>
                  </div>
                ) : null}
              </div>
            </aside>
          ) : null}
        </main>
      </div>

      {rawStatsOpen ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setRawStatsOpen(false)
          }}
          role="presentation"
        >
          <div className="modalPanel" role="dialog" aria-modal="true" aria-label="Raw stats payload">
            <div className="modalTop">
              <div className="modalTitle">Raw Stats Payload</div>
              <button className="btn btnGhost" onClick={() => setRawStatsOpen(false)}>
                Close
              </button>
            </div>
            <div className="modalBody">
              <div className="muted small">
                Workspace: <span className="mono">{projectLabel}</span>
              </div>
              {statsErr ? <div className="callout calloutBad">{statsErr}</div> : null}
              <pre className="pre">{stats ? JSON.stringify(stats, null, 2) : 'Loading...'}</pre>
            </div>
          </div>
        </div>
      ) : null}

      {deepSearchPrompt.open ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setDeepSearchPrompt({ open: false, query: '' })
          }}
          role="presentation"
        >
          <div className="modalPanel" role="dialog" aria-modal="true" aria-label="Deep search">
            <div className="modalTop">
              <div className="modalTitle">No results</div>
              <button className="btn btnGhost" onClick={() => setDeepSearchPrompt({ open: false, query: '' })}>
                Close
              </button>
            </div>
            <div className="modalBody">
              <div className="muted">Try a deep search (Recall Preview) for:</div>
              <div className="mono" style={{ marginTop: 8, marginBottom: 12 }}>
                {deepSearchPrompt.query}
              </div>
              <div className="modalActions">
                <button className="btn" onClick={() => setDeepSearchPrompt({ open: false, query: '' })}>
                  Cancel
                </button>
                <button className="btn btnPrimary" onClick={() => runDeepSearch(deepSearchPrompt.query)}>
                  Deep Search
                </button>
              </div>
            </div>
          </div>
        </div>
      ) : null}

      {diagramPreviewOpen ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setDiagramPreviewOpen(false)
          }}
          role="presentation"
        >
          <div className="modalPanel" role="dialog" aria-modal="true" aria-label="Diagrams list">
            <div className="modalTop">
              <div className="modalTitle">Available Diagrams ({diagramMemories.length})</div>
              <button className="btn btnGhost" onClick={() => setDiagramPreviewOpen(false)}>
                Close
              </button>
            </div>
            <div className="modalBody">
              <div className="muted">Tap a card to instantly open its full detail view and interactive architecture canvas:</div>
              <div className="diagramPreviewGrid">
                {diagramMemories.map((m) => {
                  const snippet = m.diagram?.code || m.content.split('```mermaid')[1]?.split('```')[0] || m.content
                  return (
                    <div
                      key={m.id}
                      className="diagramPreviewCard"
                      onClick={() => {
                        setSelectedMemory(m)
                        setDiagramPreviewOpen(false)
                      }}
                    >
                      <div className="diagramPreviewHeader">
                        <span className="diagramPreviewID">{m.id.slice(0, 16)}...</span>
                        <span className="memPill">{m.type}</span>
                      </div>
                      <div className="diagramPreviewSnippet">{snippet.trim().slice(0, 140)}...</div>
                    </div>
                  )
                })}
              </div>
            </div>
          </div>
        </div>
      ) : null}

      {wikiConsolidatedOpen ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setWikiConsolidatedOpen(false)
          }}
          role="presentation"
        >
          <div className="modalPanel wikiConsolidatedModal" role="dialog" aria-modal="true" aria-label="Consolidated wiki view">
            <div className="modalTop">
              <div className="modalTitle">Consolidated Wiki View</div>
              <div className="modalActions">
                <button className="btn btnGhost" onClick={exportWikiSelection}>
                  Download
                </button>
                <button className="btn btnGhost" onClick={printWikiSelection}>
                  Print
                </button>
                <button className="btn btnGhost" onClick={() => setWikiConsolidatedOpen(false)}>
                  Close
                </button>
              </div>
            </div>
            <div className="modalBody wikiConsolidatedBody">
              {wikiSelectedFragments.map((memory) => (
                <article key={buildMemoryKey(memory)} className="wikiConsolidatedFragment">
                  <div className="wikiFragmentBadges">
                    <span className="memPill">{memory.workspace}</span>
                    <span className="memPill">{memory.type}</span>
                    <span className="memPill">{memory.storage_tier}</span>
                    {typeof getSemanticSimilarity(memory) === 'number' ? (
                      <span className={`memPill relevancePill relevancePill${toTitle(getSemanticRelevance(getSemanticSimilarity(memory)).tone)}`}>
                        {getSemanticRelevance(getSemanticSimilarity(memory)).label} {formatScore(getSemanticSimilarity(memory), 2)}
                      </span>
                    ) : null}
                  </div>
                  <MarkdownView markdown={memory.content} clamp={false} theme={theme} />
                  {hasDiagram(memory) ? (
                    <div className="diagramBlock">
                      <DiagramViewer diagram={memory.diagram ?? { lang: 'mermaid', code: memory.content.split('```mermaid')[1]?.split('```')[0] ?? '' }} theme={theme} />
                    </div>
                  ) : null}
                </article>
              ))}
            </div>
          </div>
        </div>
      ) : null}

      {wikiDiagramMemory?.diagram ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setWikiDiagramMemory(null)
          }}
          role="presentation"
        >
          <div className="modalPanel wikiDiagramModal" role="dialog" aria-modal="true" aria-label="Wiki diagram viewer">
            <div className="modalTop">
              <div>
                <div className="modalTitle">Wiki Diagram</div>
                <div className="muted small">
                  {wikiDiagramMemory.workspace} / {wikiDiagramMemory.type} / {wikiDiagramMemory.id}
                </div>
              </div>
              <div className="modalActions">
                <button className="btn btnGhost" onClick={() => setSelectedMemory(wikiDiagramMemory)}>
                  Open Memory
                </button>
                <button className="btn btnGhost" onClick={() => setWikiDiagramMemory(null)}>
                  Close
                </button>
              </div>
            </div>
            <div className="modalBody wikiDiagramModalBody">
              <div className="wikiFragmentBadges">
                <span className="memPill">{wikiDiagramMemory.workspace}</span>
                <span className="memPill">{wikiDiagramMemory.type}</span>
                <span className="memPill">{wikiDiagramMemory.storage_tier}</span>
              </div>
              <DiagramViewer diagram={wikiDiagramMemory.diagram} theme={theme} />
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}

function OverviewPanel({
  workspace,
  project,
  stats,
  statsErr,
  healthState,
  diagramCount,
  experimentFocusKey,
  onCompareRuns,
  onInspectFailures,
  onReviewLastSession,
  onRunDiagramAction,
}: {
  workspace: string
  project?: ProjectListItem
  stats: DashboardStats | null
  statsErr: string
  healthState: { tone: 'good' | 'warn' | 'bad'; label: string; detail: string }
  diagramCount: number
  experimentFocusKey: number
  onCompareRuns: () => void
  onInspectFailures: () => void
  onReviewLastSession: () => void
  onRunDiagramAction: () => void
}) {
  const typeEntries = sortCountEntries(stats?.memory_type_counts)
  const tierEntries = sortCountEntries(stats?.storage_tier_counts)
  const tokenGroups = stats?.token_metrics_by_group_all ?? []
  const llmGroups = stats?.llm_usage_by_group_all ?? []
  const recallTokenTotals = stats?.recall_token_metrics ?? getOperationTotals(stats?.token_metrics_by_operation, 'recall') ?? zeroTokenTotals()
  const recallTokenSavingsPercent = stats?.recall_token_savings_percent ?? stats?.token_savings_percent ?? 0
  const totalMemories = stats?.memory_count ?? project?.memory_count ?? 0
  const retrievedMemoryCount = stats?.retrieved_memory_count ?? 0
  const neverReachedMemoryCount = stats?.never_reached_memory_count ?? Math.max(0, totalMemories - retrievedMemoryCount)
  const retrieveCountTotal = stats?.retrieve_count_total ?? 0
  const retrievalCoveragePercent = stats?.retrieval_coverage_percent ?? (totalMemories > 0 ? (retrievedMemoryCount / totalMemories) * 100 : 0)
  const lowReachPercentile = stats?.low_reach_percentile ?? 25
  const lowReachThreshold = stats?.low_reach_threshold ?? 0
  const lowReachMemoryCount = stats?.low_reach_memory_count ?? 0
  const topRetrievedMemories = stats?.top_retrieved_memories ?? []
  const experimentsRef = useRef<HTMLElement>(null)

  useEffect(() => {
    if (experimentFocusKey <= 0) return
    experimentsRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [experimentFocusKey])

  return (
    <section className="surfaceStack">
      <div className="overviewHero">
        <div className="overviewHeroCopy">
          <div className="overviewEyebrow">Control Surface</div>
          <h1 className="overviewTitle">{workspace || 'Select a workspace'}</h1>
          <p className="overviewText">
            See what this workspace remembers, how experiments are trending, and whether the system is healthy before you run another query.
          </p>
          <div className="overviewMetaRow asciiChipRow">
            <span className="asciiChipRowItem">
              <span className="asciiChipIndex">[01]</span>
              <span className="asciiChipLead">.-</span>
              <span className={`statusBadge statusBadge${toTitle(healthState.tone)}`}>{healthState.label}</span>
            </span>
            <span className="asciiChipRowItem">
              <span className="asciiChipIndex">[02]</span>
              <span className="asciiChipLead">.-</span>
              <span className="overviewMetaItem">Last activity {formatTS(stats?.last_activity || project?.last_activity) || 'n/a'}</span>
            </span>
          </div>
        </div>
      </div>

      {statsErr ? <div className="callout calloutBad">{statsErr}</div> : null}

      <section className="starterStrip">
        <article className="starterCard">
          <div className="starterCardEyebrow">Guided</div>
          <div className="starterCardTitle">Compare Memory ON/OFF</div>
          <p className="starterCardText">Jump to the grouped token and LLM comparison section.</p>
          <button className="btn" type="button" onClick={onCompareRuns}>
            Compare
          </button>
        </article>

        <article className="starterCard">
          <div className="starterCardEyebrow">Guided</div>
          <div className="starterCardTitle">Inspect Recent Failures</div>
          <p className="starterCardText">Run a failure-focused search across recent incidents and regressions.</p>
          <button className="btn" type="button" onClick={onInspectFailures}>
            Inspect
          </button>
        </article>

        <article className="starterCard">
          <div className="starterCardEyebrow">Guided</div>
          <div className="starterCardTitle">Open Diagrams</div>
          <p className="starterCardText">
            {diagramCount > 0
              ? `Open the ${diagramCount} loaded visual memories, or fall back to search if needed.`
              : 'Search for architecture and Mermaid-style memories.'}
          </p>
          <button className="btn" type="button" onClick={onRunDiagramAction}>
            {diagramCount > 0 ? 'Open' : 'Find'}
          </button>
        </article>

        <article className="starterCard">
          <div className="starterCardEyebrow">Guided</div>
          <div className="starterCardTitle">Review Last Session</div>
          <p className="starterCardText">Open the latest captured session timeline.</p>
          <button className="btn" type="button" onClick={onReviewLastSession}>
            Review
          </button>
        </article>
      </section>

      <div className="statsGrid">
        <MetricCard title="Memories" value={formatNumber(totalMemories)} detail="Current workspace volume" />
        <MetricCard title="Storage" value={formatBytes(stats?.db_size_bytes ?? project?.size_bytes)} detail="SQLite footprint on disk" />
        <MetricCard title="Recall Savings" value={formatPercent(recallTokenSavingsPercent)} detail={`${formatNumber(recallTokenTotals.records)} recall operations`} />
        <MetricCard title="LLM Usage" value={formatNumber(stats?.llm_usage_totals.total_tokens)} detail={`${formatNumber(stats?.llm_usage_totals.records)} provider reports`} />
        <MetricCard title="Retrieved" value={formatNumber(retrievedMemoryCount)} detail={`${formatPercent(retrievalCoveragePercent)} ever surfaced`} />
        <MetricCard title="Never Reached" value={formatNumber(neverReachedMemoryCount)} detail={`${formatNumber(retrieveCountTotal)} total retrieval events`} />
        <MetricCard title="Low Reach" value={formatNumber(lowReachMemoryCount)} detail={`Bottom ${formatNumber(lowReachPercentile)}% of reached memories`} />
        <MetricCard title="Pinned" value={formatNumber(stats?.pinned_count)} detail="Pinned memories retained" />
        <MetricCard title="Diagrams" value={formatNumber(stats?.diagram_count)} detail="Memories with visual payloads" />
      </div>

      <div className="overviewColumns">
        <BreakdownCard title="Memory Types" subtitle={`${formatNumber(sumCounts(stats?.memory_type_counts))} total`}>
          <PieChartBreakdown entries={typeEntries} emptyLabel="No type distribution yet." />
        </BreakdownCard>

        <BreakdownCard title="Storage Tiers" subtitle={`${formatNumber(sumCounts(stats?.storage_tier_counts))} classified`}>
          <PieChartBreakdown entries={tierEntries} emptyLabel="No tier distribution yet." />
        </BreakdownCard>

        <BreakdownCard title="Retrieval Reach" subtitle={`${formatPercent(retrievalCoveragePercent)} coverage`}>
          <div className="diagnosticsList">
            <DiagnosticRow label="Retrieved Memories" value={formatNumber(retrievedMemoryCount)} />
            <DiagnosticRow label="Never Reached" value={formatNumber(neverReachedMemoryCount)} />
            <DiagnosticRow label={`Low Reach (P${formatNumber(lowReachPercentile)})`} value={formatNumber(lowReachMemoryCount)} />
            <DiagnosticRow label="Total Retrieval Events" value={formatNumber(retrieveCountTotal)} />
            <DiagnosticRow label="Low-Reach Threshold" value={`<= ${formatNumber(lowReachThreshold)} hits`} />
            <DiagnosticRow label="Last Accessed" value={formatTS(stats?.last_memory_accessed_at) || 'n/a'} />
          </div>
        </BreakdownCard>
      </div>

      <section className="comparisonSection">
        <div className="comparisonHeader">
          <div>
            <div className="breakdownTitle">Most Reached Memories</div>
            <div className="breakdownSubtitle">Top memories by retrieval count. Use this to spot hot memories and dead zones.</div>
          </div>
        </div>
        {topRetrievedMemories.length === 0 ? (
          <div className="emptyInline">No retrieval activity yet. Once memories are surfaced by search or recall, they will appear here.</div>
        ) : (
          <div className="diagnosticsList">
            {topRetrievedMemories.map((memory) => (
              <DiagnosticRow
                key={memory.id}
                label={`${memory.preview} (${toTitle(memory.type)} / ${toTitle(memory.storage_tier)}${memory.pinned ? ' / pinned' : ''})`}
                value={`${formatNumber(memory.access_count)} hits`}
              />
            ))}
          </div>
        )}
      </section>

      <section ref={experimentsRef}>
        <ComparisonSection title="Recall Savings Comparison" description="Recall-only token savings grouped by run label and memory enabled state." emptyLabel="No grouped recall metrics yet. Run labeled ON/OFF recall experiments to populate this view.">
          {tokenGroups.map((group, index) => (
            <TokenGroupCard key={`token-${group.run_label}-${group.memory_enabled ? 'on' : 'off'}`} group={group} index={index} />
          ))}
        </ComparisonSection>

        <ComparisonSection title="LLM Usage Comparison" description="Provider-reported usage grouped by run label and memory enabled state." emptyLabel="No grouped LLM usage yet. Ingest provider usage metrics to populate this view.">
          {llmGroups.map((group, index) => (
            <LLMGroupCard key={`llm-${group.run_label}-${group.memory_enabled ? 'on' : 'off'}`} group={group} index={index} />
          ))}
        </ComparisonSection>
      </section>
    </section>
  )
}

function BenchmarkPanel({
  workspace,
  runs,
  busy,
  error,
}: {
  workspace: string
  runs: BenchmarkRun[]
  busy: boolean
  error: string
}) {
  const latest = runs[0]

  return (
    <section className="surfaceStack">
      <div className="diagnosticsHero">
        <div>
          <div className="overviewEyebrow">Benchmark</div>
          <h2 className="sectionTitle">{workspace || 'Workspace'} Quality Benchmark</h2>
          <p className="sectionText">Review memory ON/OFF benchmark quality, token efficiency, cluster-level coverage, and recent run history.</p>
        </div>
        {latest ? (
          <div className="diagnosticsHeroSide">
            <span className="statusBadge statusBadgeGood">{latest.verdict}</span>
            <span className="muted small">{formatTS(latest.created_at) || 'n/a'}</span>
          </div>
        ) : null}
      </div>

      {error ? <div className="callout calloutBad">{error}</div> : null}
      {busy ? <div className="emptyInline">Loading benchmark runs...</div> : null}
      {!busy && !error && !latest ? (
        <div className="emptyStateCard">
          <div className="overviewEyebrow">No Benchmark Runs</div>
          <div className="sectionTitle">This workspace has not ingested benchmark results yet.</div>
          <p className="sectionText">Run the benchmark pipeline and persist the scored report to populate this panel.</p>
        </div>
      ) : null}

      {latest ? (
        <>
          <div className="benchmarkStatsGrid">
            <MetricCard title="Combined Score" value={latest.combined_score.toFixed(3)} detail={latest.verdict} />
            <MetricCard title="Cases" value={formatNumber(latest.case_count)} detail={`${formatNumber(latest.seed_count)} seeds`} />
            <MetricCard title="Precision" value={formatUnitPercent(latest.precision)} detail={`Gold recall ${formatUnitPercent(latest.gold_recall)}`} />
            <MetricCard title="NDCG" value={latest.ndcg.toFixed(3)} detail={`F1 ${latest.f1.toFixed(3)}`} />
            <MetricCard title="Token Efficiency" value={formatUnitPercent(latest.token_efficiency)} detail={`${formatNumber(latest.saved_tokens)} tokens saved`} />
            <MetricCard title="Cost Saved" value={formatUnitPercent(latest.cost_saved_pct)} detail={`$${latest.cost_saved.toFixed(2)} saved`} />
          </div>

          <div className="benchmarkColumns">
            <BreakdownCard title="Quality Metrics" subtitle={`Latest run ${latest.run_id}`}>
              <div className="diagnosticsList">
                <DiagnosticRow label="Precision" value={formatUnitPercent(latest.precision)} />
                <DiagnosticRow label="Recall" value={formatUnitPercent(latest.recall)} />
                <DiagnosticRow label="Gold Recall" value={formatUnitPercent(latest.gold_recall)} />
                <DiagnosticRow label="Keyword Coverage" value={formatUnitPercent(latest.keyword_coverage)} />
                <DiagnosticRow label="NDCG" value={latest.ndcg.toFixed(3)} />
                <DiagnosticRow label="F1" value={latest.f1.toFixed(3)} />
              </div>
            </BreakdownCard>

            <BreakdownCard title="Efficiency And Runtime" subtitle={`top_k ${latest.top_k} | budget ${latest.budget}`}>
              <div className="diagnosticsList">
                <DiagnosticRow label="Baseline Tokens" value={formatNumber(latest.baseline_tokens)} />
                <DiagnosticRow label="Returned Tokens" value={formatNumber(latest.returned_tokens)} />
                <DiagnosticRow label="Saved Tokens" value={formatNumber(latest.saved_tokens)} />
                <DiagnosticRow label="Seed Duration" value={formatDuration(latest.seed_duration_ms)} />
                <DiagnosticRow label="ON Duration" value={formatDuration(latest.on_duration_ms)} />
                <DiagnosticRow label="OFF Duration" value={formatDuration(latest.off_duration_ms)} />
              </div>
            </BreakdownCard>
          </div>

          <section className="comparisonSection">
            <div className="comparisonHeader">
              <div>
                <div className="breakdownTitle">Cluster Breakdown</div>
                <div className="breakdownSubtitle">Per-cluster quality and efficiency summaries for the latest benchmark run.</div>
              </div>
            </div>
            <div className="benchmarkClusterGrid">
              {latest.clusters.map((cluster, index) => (
                <BenchmarkClusterCard key={`${cluster.cluster_id}-${index}`} cluster={cluster} index={index} />
              ))}
            </div>
          </section>

          <section className="comparisonSection">
            <div className="comparisonHeader">
              <div>
                <div className="breakdownTitle">Run History</div>
                <div className="breakdownSubtitle">Newest benchmark runs first, grouped in the same workspace.</div>
              </div>
            </div>
            <div className="diagnosticsList">
              {runs.map((run) => (
                <DiagnosticRow
                  key={run.run_id}
                  label={`${run.run_id} | ${formatTS(run.created_at) || 'n/a'} | ${formatNumber(run.case_count)} cases | ${run.verdict}`}
                  value={`${run.combined_score.toFixed(3)} / ${formatUnitPercent(run.token_efficiency)}`}
                />
              ))}
            </div>
          </section>
        </>
      ) : null}
    </section>
  )
}

function BenchmarkClusterCard({ cluster, index }: { cluster: BenchmarkClusterSummary; index: number }) {
  return (
    <article className="groupCard benchmarkClusterCard">
      <div className="groupCardTop">
        <div className="groupHeading">
          <span className="groupIndex">{formatLegendIndex(index)}</span>
          <span className="groupLead">.-</span>
          <div className="groupTitle">{cluster.cluster_title}</div>
        </div>
        <span className="groupBadge groupBadgeOn">{cluster.verdict}</span>
      </div>
      <div className="groupMeta mono">cluster:{cluster.cluster_id}</div>
      <div className="diagnosticsList" style={{ marginTop: 14 }}>
        <DiagnosticRow label="Cases" value={formatNumber(cluster.cases)} />
        <DiagnosticRow label="Combined Score" value={cluster.combined_score.toFixed(3)} />
        <DiagnosticRow label="Precision" value={formatUnitPercent(cluster.precision)} />
        <DiagnosticRow label="Gold Recall" value={formatUnitPercent(cluster.gold_recall)} />
        <DiagnosticRow label="Keyword Coverage" value={formatUnitPercent(cluster.keyword_coverage)} />
        <DiagnosticRow label="Token Efficiency" value={formatUnitPercent(cluster.token_efficiency)} />
      </div>
    </article>
  )
}

function DiagnosticsPanel({
  workspaceLabel,
  project,
  stats,
  statsErr,
  healthState,
  onOpenRaw,
}: {
  workspaceLabel: string
  project?: ProjectListItem
  stats: DashboardStats | null
  statsErr: string
  healthState: { tone: 'good' | 'warn' | 'bad'; label: string; detail: string }
  onOpenRaw: () => void
}) {
  const recallTokenTotals = stats?.recall_token_metrics ?? getOperationTotals(stats?.token_metrics_by_operation, 'recall') ?? zeroTokenTotals()
  const recallTokenSavingsPercent = stats?.recall_token_savings_percent ?? stats?.token_savings_percent ?? 0
  const searchTokenTotals = getOperationTotals(stats?.token_metrics_by_operation, 'search') ?? zeroTokenTotals()
  const totalMemories = stats?.memory_count ?? project?.memory_count ?? 0
  const retrievedMemoryCount = stats?.retrieved_memory_count ?? 0
  const neverReachedMemoryCount = stats?.never_reached_memory_count ?? Math.max(0, totalMemories - retrievedMemoryCount)
  const retrieveCountTotal = stats?.retrieve_count_total ?? 0
  const retrievalCoveragePercent = stats?.retrieval_coverage_percent ?? (totalMemories > 0 ? (retrievedMemoryCount / totalMemories) * 100 : 0)
  const lowReachPercentile = stats?.low_reach_percentile ?? 25
  const lowReachThreshold = stats?.low_reach_threshold ?? 0
  const lowReachMemoryCount = stats?.low_reach_memory_count ?? 0
  const topRetrievedMemories = stats?.top_retrieved_memories ?? []
  return (
    <section className="surfaceStack">
      <div className="diagnosticsHero">
        <div>
          <div className="overviewEyebrow">Diagnostics</div>
          <h2 className="sectionTitle">{workspaceLabel || 'Workspace'} Health</h2>
          <p className="sectionText">Current runtime state, storage status, and experiment signal summary.</p>
        </div>
        <div className="diagnosticsHeroSide">
          <span className={`statusBadge statusBadge${toTitle(healthState.tone)}`}>{healthState.label}</span>
          <button className="btn" type="button" onClick={onOpenRaw}>
            Raw Payload
          </button>
        </div>
      </div>

      {statsErr ? <div className="callout calloutBad">{statsErr}</div> : null}

      <div className="statsGrid">
        <MetricCard title="DB Size" value={formatBytes(stats?.db_size_bytes ?? project?.size_bytes)} detail="Local store footprint" />
        <MetricCard title="Last Activity" value={formatTS(stats?.last_activity || project?.last_activity) || 'n/a'} detail="Most recent update or access" />
        <MetricCard title="Last Memory Update" value={formatTS(stats?.last_memory_updated_at) || 'n/a'} detail="Latest memory write timestamp" />
        <MetricCard title="Memories" value={formatNumber(totalMemories)} detail="Current workspace volume" />
        <MetricCard title="Retrieved" value={formatNumber(retrievedMemoryCount)} detail={`${formatPercent(retrievalCoveragePercent)} ever surfaced`} />
        <MetricCard title="Never Reached" value={formatNumber(neverReachedMemoryCount)} detail="Memories with zero retrieval count" />
        <MetricCard title="Low Reach" value={formatNumber(lowReachMemoryCount)} detail={`Bottom ${formatNumber(lowReachPercentile)}% of reached memories`} />
      </div>

      <div className="diagnosticsGrid">
        <BreakdownCard title="Memory Footprint" subtitle="Current retained memory shape">
          <div className="diagnosticsList">
            <DiagnosticRow label="Pinned Memories" value={formatNumber(stats?.pinned_count)} />
            <DiagnosticRow label="Diagram Memories" value={formatNumber(stats?.diagram_count)} />
            <DiagnosticRow label="Default Workspace" value={workspaceLabel || 'n/a'} />
          </div>
        </BreakdownCard>

        <BreakdownCard title="Experiment Signals" subtitle="Quick operational summary">
          <div className="diagnosticsList">
            <DiagnosticRow label="Recall Records" value={formatNumber(recallTokenTotals.records)} />
            <DiagnosticRow label="Recall Tokens Saved" value={formatNumber(recallTokenTotals.saved_tokens)} />
            <DiagnosticRow label="Recall Savings Rate" value={formatPercent(recallTokenSavingsPercent)} />
            <DiagnosticRow label="Search Records" value={formatNumber(searchTokenTotals.records)} />
            <DiagnosticRow label="LLM Records" value={formatNumber(stats?.llm_usage_totals.records)} />
          </div>
        </BreakdownCard>

        <BreakdownCard title="Retrieval Reachability" subtitle="Which memories are reached and which remain untouched">
          <div className="diagnosticsList">
            <DiagnosticRow label="Total Retrieval Events" value={formatNumber(retrieveCountTotal)} />
            <DiagnosticRow label="Retrieved Memories" value={formatNumber(retrievedMemoryCount)} />
            <DiagnosticRow label="Never Reached Memories" value={formatNumber(neverReachedMemoryCount)} />
            <DiagnosticRow label={`Low Reach Memories (P${formatNumber(lowReachPercentile)})`} value={formatNumber(lowReachMemoryCount)} />
            <DiagnosticRow label="Low-Reach Threshold" value={`<= ${formatNumber(lowReachThreshold)} hits`} />
            <DiagnosticRow label="Coverage Rate" value={formatPercent(retrievalCoveragePercent)} />
            <DiagnosticRow label="Last Accessed Memory" value={formatTS(stats?.last_memory_accessed_at) || 'n/a'} />
          </div>
          {topRetrievedMemories.length > 0 ? (
            <div className="diagnosticsList" style={{ marginTop: 16 }}>
              {topRetrievedMemories.map((memory) => (
                <DiagnosticRow
                  key={memory.id}
                  label={`${memory.preview} (${toTitle(memory.type)} / ${toTitle(memory.storage_tier)}${memory.pinned ? ' / pinned' : ''})`}
                  value={`${formatNumber(memory.access_count)} hits`}
                />
              ))}
            </div>
          ) : (
            <div className="muted">No memories have been retrieved yet for this workspace.</div>
          )}
        </BreakdownCard>
      </div>
    </section>
  )
}

function SessionsPanel({
  workspace,
  sessions,
  sessionsBusy,
  sessionsErr,
  sessionsUnavailable,
  selectedSessionID,
  observations,
  observationsBusy,
  observationsErr,
  promotionResult,
  promotionBusy,
  onSelectSession,
  onPromote,
}: {
  workspace: string
  sessions: SessionEntry[]
  sessionsBusy: boolean
  sessionsErr: string
  sessionsUnavailable: boolean
  selectedSessionID: string
  observations: ObservationEntry[]
  observationsBusy: boolean
  observationsErr: string
  promotionResult?: ObservationPromotionResult
  promotionBusy: boolean
  onSelectSession: (sessionID: string) => void
  onPromote: (type?: MemoryType) => void
}) {
  const selectedSession = sessions.find((session) => session.session_id === selectedSessionID) ?? sessions[0]

  return (
    <section className="surfaceStack">
      <div className="diagnosticsHero">
        <div>
          <div className="overviewEyebrow">Session Explorer</div>
          <h2 className="sectionTitle">{workspace || 'Workspace'} Sessions</h2>
          <p className="sectionText">Inspect one session at a time and promote it when the signal is worth keeping.</p>
        </div>
      </div>

      {sessionsUnavailable ? (
        <div className="emptyStateCard">
          <div className="overviewEyebrow">Auto-Capture Disabled</div>
          <div className="sectionTitle">Sessions Are Not Available Yet</div>
          <p className="sectionText">
            The session routes are feature-gated. Enable observation capture to populate this explorer, then reload the dashboard.
          </p>
        </div>
      ) : null}

      {!sessionsUnavailable && sessionsErr ? <div className="callout calloutBad">{sessionsErr}</div> : null}

      {!sessionsUnavailable && !sessionsBusy && sessions.length === 0 && !sessionsErr ? (
        <div className="emptyStateCard">
          <div className="overviewEyebrow">No Sessions Yet</div>
          <div className="sectionTitle">This Workspace Has No Captured Sessions</div>
          <p className="sectionText">
            Auto-capture is enabled, but there are no recent sessions for this workspace yet. Once observations are ingested, they will appear here ordered by recent activity.
          </p>
        </div>
      ) : null}

      {!sessionsUnavailable && sessions.length > 0 ? (
        <section className="comparisonSection">
          <div className="comparisonHeader">
            <div>
              <div className="breakdownTitle">Session Timeline</div>
              <div className="breakdownSubtitle">Pick a session to answer basic "what happened?" questions and optionally promote it into memory.</div>
            </div>
          </div>

          <div className="sessionExplorerLayout">
            <div className="sessionRail">
              {sessions.map((session, index) => {
                const isSelected = session.session_id === selectedSession?.session_id
                const promoted = promotionResult && session.session_id === promotionResult.session_id
                return (
                  <button
                    key={session.session_id}
                    type="button"
                    className={isSelected ? 'sessionRailCard sessionRailCardOn' : 'sessionRailCard'}
                    onClick={() => onSelectSession(session.session_id)}
                  >
                    <div className="sessionRailTop">
                      <div className="sessionRailHeading">
                        <span className="sessionRailIndex">{formatLegendIndex(index)}</span>
                        <span className="sessionRailLead">.-</span>
                        <div className="sessionRailTitle">{session.session_id}</div>
                      </div>
                      <span className="groupBadge groupBadgeOn">{formatNumber(session.observation_count)} obs</span>
                    </div>
                    <div className="sessionRailMeta">
                      <span className="sessionRailStamp">last_seen:</span> {formatTS(session.last_seen_at) || 'n/a'}
                    </div>
                    {promoted ? (
                      <div className="sessionRailMeta">
                        <span className="sessionRailStamp">promotion:</span>{' '}
                        {promotionResult.deduplicated ? 'Already promoted' : promotionResult.rejected ? 'Promotion rejected' : 'Promotion recorded'}
                      </div>
                    ) : null}
                  </button>
                )
              })}
            </div>

            <div className="sessionDetailPanel">
              {selectedSession ? (
                <>
                  <div className="sessionDetailHeader">
                    <div>
                      <div className="sessionCardTitle">{selectedSession.session_id}</div>
                      <div className="sessionCardMeta">Observation timeline for the selected session</div>
                      <div className="sessionDetailSummary asciiChipRow">
                        <span className="asciiChipRowItem">
                          <span className="asciiChipIndex">[01]</span>
                          <span className="asciiChipLead">.-</span>
                          <span className="overviewMetaItem">{formatNumber(selectedSession.observation_count)} observations</span>
                        </span>
                        <span className="asciiChipRowItem">
                          <span className="asciiChipIndex">[02]</span>
                          <span className="asciiChipLead">.-</span>
                          <span className="overviewMetaItem">last seen {formatTS(selectedSession.last_seen_at) || 'n/a'}</span>
                        </span>
                      </div>
                    </div>
                    <div className="sessionDetailActions">
                      <button className="btn btnPrimary" type="button" disabled={promotionBusy} onClick={() => onPromote('episodic')}>
                        {promotionBusy ? 'Promoting...' : 'Promote Episodic'}
                      </button>
                      <button className="btn" type="button" disabled={promotionBusy} onClick={() => onPromote('procedural')}>
                        Promote Procedural
                      </button>
                      <button className="btn" type="button" disabled={promotionBusy} onClick={() => onPromote('semantic')}>
                        Promote Semantic
                      </button>
                    </div>
                  </div>

                  <div className="diagnosticsGrid">
                    <BreakdownCard title="Session Facts" subtitle="High-level session metadata">
                      <div className="diagnosticsList">
                        <DiagnosticRow label="Last Seen" value={formatTS(selectedSession.last_seen_at) || 'n/a'} />
                        <DiagnosticRow label="Started" value={formatTS(selectedSession.started_at) || 'n/a'} />
                        <DiagnosticRow label="Ended" value={formatTS(selectedSession.ended_at) || 'open'} />
                        <DiagnosticRow label="Project Root" value={selectedSession.project_root || 'n/a'} />
                        <DiagnosticRow label="CWD" value={selectedSession.cwd || 'n/a'} />
                      </div>
                    </BreakdownCard>

                    <BreakdownCard title="Promotion Status" subtitle="Visible once a promotion attempt has been made">
                      {promotionResult ? (
                        <div className="diagnosticsList">
                          <DiagnosticRow label="Requested Type" value={promotionResult.requested_type} />
                          <DiagnosticRow
                            label="Result"
                            value={
                              promotionResult.rejected
                                ? 'rejected'
                                : promotionResult.deduplicated
                                  ? 'deduplicated'
                                  : promotionResult.created_id
                                    ? 'created'
                                    : 'empty'
                            }
                          />
                          <DiagnosticRow label="Created ID" value={promotionResult.created_id || 'n/a'} />
                          <DiagnosticRow label="Observations" value={formatNumber(promotionResult.observations)} />
                          <DiagnosticRow label="Storage Tier" value={promotionResult.storage_tier || 'n/a'} />
                          <DiagnosticRow label="Confidence" value={typeof promotionResult.confidence === 'number' ? promotionResult.confidence.toFixed(2) : 'n/a'} />
                          {promotionResult.reject_reason ? <DiagnosticRow label="Reason" value={promotionResult.reject_reason} /> : null}
                        </div>
                      ) : (
                        <div className="muted">No promotion has been run for this session in the current dashboard view yet.</div>
                      )}
                    </BreakdownCard>
                  </div>

                  {observationsErr ? <div className="callout calloutBad">{observationsErr}</div> : null}

                  <section className="timelineSection">
                    <div className="comparisonHeader">
                      <div>
                        <div className="breakdownTitle">Observation Timeline</div>
                        <div className="breakdownSubtitle">Newest first. Each observation is already privacy-scrubbed and summarized.</div>
                      </div>
                    </div>

                    {observationsBusy ? <div className="emptyInline">Loading observations...</div> : null}
                    {!observationsBusy && observations.length === 0 ? <div className="emptyInline">No observations for this session yet.</div> : null}
                    {!observationsBusy && observations.length > 0 ? (
                      <div className="timelineList">
                        {observations.map((observation) => (
                          <article key={observation.id} className="timelineCard">
                            <div className="timelineRail" aria-hidden="true">
                              <span className="timelineRailDot" />
                              <span className="timelineRailLine" />
                            </div>
                            <div className="timelineCardBody">
                              <div className="timelineTop">
                                <div>
                                  <div className="timelineTitle">
                                    <span className="timelinePrefix">.-</span>
                                    <span>{toTitle(observation.kind)}</span>
                                  </div>
                                  <div className="timelineMeta">{formatTS(observation.occurred_at) || 'n/a'}</div>
                                </div>
                                <span className="memPill timelineToolPill">{observation.tool_name || 'system'}</span>
                              </div>
                              <div className="timelineSummary">{observation.summary}</div>
                              <div className="timelineFooter">
                                <span className="mono timelineStamp">id:{observation.id.slice(0, 16)}...</span>
                                <span className="muted small timelineStamp">created:{formatTS(observation.created_at) || 'n/a'}</span>
                              </div>
                            </div>
                          </article>
                        ))}
                      </div>
                    ) : null}
                  </section>
                </>
              ) : null}
            </div>
          </div>
        </section>
      ) : null}
    </section>
  )
}

function WikiPanel({
  theme,
  workspace,
  projects,
  mode,
  query,
  scope,
  viewMode,
  optionsOpen,
  searched,
  busy,
  error,
  results,
  pinnedResults,
  weakResults,
  recall,
  recentsBusy,
  selectedCount,
  selectedIds,
  explain,
  topK,
  recallTopK,
  budget,
  semanticThreshold,
  minTotal,
  minConfidence,
  outcome,
  types,
  tiers,
  onQueryChange,
  onModeChange,
  onScopeChange,
  onViewModeChange,
  onExitWiki,
  onOpenRaw,
  onToggleTheme,
  onClearView,
  onToggleOptions,
  onSubmit,
  onSuggestion,
  onToggleSelection,
  onOpenMemory,
  onOpenDiagram,
  onTogglePin,
  isPinned,
  isPinBusy,
  onOpenConsolidated,
  onDownloadSelection,
  onPrintSelection,
  onDeleteSelection,
  deleteBusy,
  onSetMinSemantic,
  onSetExplain,
  onSetTopK,
  onSetRecallTopK,
  onSetBudget,
  onSetMinTotal,
  onSetMinConfidence,
  onSetOutcome,
  onToggleType,
  onToggleTier,
  onCollapseOptions,
}: {
  theme: 'light' | 'dark'
  workspace: string
  projects: ProjectListItem[]
  mode: WikiMode
  query: string
  scope: string
  viewMode: WikiViewMode
  optionsOpen: boolean
  searched: boolean
  busy: boolean
  error: string
  results: MemoryEntry[]
  pinnedResults: MemoryEntry[]
  weakResults: MemoryEntry[]
  recall: RecallPreviewResponse | null
  recentsBusy: boolean
  selectedCount: number
  selectedIds: Set<string>
  explain: boolean
  topK: number
  recallTopK: number
  budget: number
  semanticThreshold: number
  minTotal: string
  minConfidence: string
  outcome: OutcomeResult | ''
  types: Set<MemoryType>
  tiers: Set<StorageTier>
  onQueryChange: (value: string) => void
  onModeChange: (value: WikiMode) => void
  onScopeChange: (value: string) => void
  onViewModeChange: (value: WikiViewMode) => void
  onExitWiki: () => void
  onOpenRaw: () => void
  onToggleTheme: () => void
  onClearView: () => void
  onToggleOptions: () => void
  onSubmit: () => void
  onSuggestion: (query: string) => void
  onToggleSelection: (memory: MemoryEntry) => void
  onOpenMemory: (memory: MemoryEntry) => void
  onOpenDiagram: (memory: MemoryEntry) => void
  onTogglePin: (memory: MemoryEntry) => void
  isPinned: (memory: MemoryEntry) => boolean
  isPinBusy: (memory: MemoryEntry) => boolean
  onOpenConsolidated: () => void
  onDownloadSelection: () => void
  onPrintSelection: () => void
  onDeleteSelection: () => void
  deleteBusy: boolean
  onSetMinSemantic: (value: number) => void
  onSetExplain: (value: boolean) => void
  onSetTopK: (value: number) => void
  onSetRecallTopK: (value: number) => void
  onSetBudget: (value: number) => void
  onSetMinTotal: (value: string) => void
  onSetMinConfidence: (value: string) => void
  onSetOutcome: (value: OutcomeResult | '') => void
  onToggleType: (memoryType: MemoryType, checked: boolean) => void
  onToggleTier: (tier: StorageTier, checked: boolean) => void
  onCollapseOptions: () => void
}) {
  const hasResults = pinnedResults.length > 0 || results.length > 0 || weakResults.length > 0
  const scopeLabel = scope === ALL_PROJECTS_SCOPE ? 'all projects' : scope
  const isRecentsMode = mode === 'recents'
  const isRecallMode = mode === 'recall'
  const working = busy || recentsBusy
  const showResultSurface = searched || working || Boolean(error)
  const resultTitle = isRecentsMode ? 'recent memories' : query
  const leadLabel = isRecallMode ? 'recall preview' : isRecentsMode ? 'recent stream' : 'stitched view'
  const inputPlaceholder = isRecallMode ? 'describe the task to recall...' : isRecentsMode ? 'recents loads from the selected scope' : 'search the wiki...'
  const submitLabel = working ? 'WAIT' : isRecentsMode ? 'LOAD' : isRecallMode ? 'RECALL' : 'GO'
  const loadingLabel = isRecallMode ? 'recalling knowledge' : isRecentsMode ? 'loading recents' : 'searching wiki'
  const [weakTailOpen, setWeakTailOpen] = useState<boolean>(results.length === 0 && weakResults.length > 0)
  const [dockFocused, setDockFocused] = useState<boolean>(false)
  const dockShellRef = useRef<HTMLDivElement>(null)
  const dockExpanded = dockFocused || optionsOpen

  useEffect(() => {
    if (weakResults.length === 0) {
      setWeakTailOpen(false)
      return
    }
    if (results.length === 0) {
      setWeakTailOpen(true)
    }
  }, [results.length, weakResults.length])

  useEffect(() => {
    const handleOutsidePointer = (event: MouseEvent | TouchEvent) => {
      if (!dockShellRef.current) return
      if (dockShellRef.current.contains(event.target as Node)) return
      setDockFocused(false)
      onCollapseOptions()
    }
    document.addEventListener('mousedown', handleOutsidePointer)
    document.addEventListener('touchstart', handleOutsidePointer)
    return () => {
      document.removeEventListener('mousedown', handleOutsidePointer)
      document.removeEventListener('touchstart', handleOutsidePointer)
    }
  }, [onCollapseOptions])

  return (
    <section className="wikiSurface">
      <div className="wikiCanvas">
        <div className="wikiUtilityCluster">
          <div className="wikiModeRail" role="tablist" aria-label="Wiki modes">
            <button className={mode === 'search' ? 'memPill memPillAccent wikiModePill' : 'memPill wikiModePill'} type="button" onClick={() => onModeChange('search')}>
              search
            </button>
            <button className={mode === 'recall' ? 'memPill memPillAccent wikiModePill' : 'memPill wikiModePill'} type="button" onClick={() => onModeChange('recall')}>
              recall
            </button>
            <button className={mode === 'recents' ? 'memPill memPillAccent wikiModePill' : 'memPill wikiModePill'} type="button" onClick={() => onModeChange('recents')}>
              recents
            </button>
          </div>
          <div className="wikiUtilityActions">
            <button className="btn btnGhost" type="button" onClick={onExitWiki}>
              [dashboard]
            </button>
            <button className="btn btnGhost" type="button" onClick={onClearView}>
              [clear]
            </button>
            <button className="btn btnGhost" type="button" onClick={onOpenRaw}>
              [raw]
            </button>
            <button className="btn btnGhost" type="button" onClick={onToggleTheme}>
              [{theme === 'dark' ? 'light' : 'dark'}]
            </button>
          </div>
        </div>

        {!showResultSurface ? (
          <section className="wikiHero">
            <div className="wikiHeroMark">agent-memory/wiki :: {workspace || 'workspace'}</div>
            <h1 className="wikiHeroTitle">{isRecallMode ? 'task becomes recall' : isRecentsMode ? 'time becomes wiki' : 'memory becomes wiki'}</h1>
            <p className="wikiHeroText">
              {isRecallMode
                ? 'Recall preview distills the most useful knowledge for a task and keeps the included memories in one stitched reading flow.'
                : isRecentsMode
                  ? 'Recents turns the latest captured memories into one quiet stream so new findings, incidents, and diagrams stay easy to scan.'
                  : 'Browse what the system has learned across projects, outcomes, diagrams, and long-lived operational knowledge.'}
            </p>
            {isRecentsMode ? (
              <div className="wikiSuggestionRow">
                <span className="memPill">latest additions</span>
                <span className="memPill">{scopeLabel || 'workspace'}</span>
                <button className="wikiSuggestion" type="button" onClick={onSubmit}>
                  [load recents]
                </button>
              </div>
            ) : (
              <div className="wikiSuggestionRow">
                {wikiSuggestionPresets.map((item) => (
                  <button
                    key={item.label}
                    className="wikiSuggestion"
                    type="button"
                    onClick={() => {
                      onModeChange('search')
                      onQueryChange(item.query)
                      onSuggestion(item.query)
                    }}
                  >
                    [{item.label}]
                  </button>
                ))}
              </div>
            )}
          </section>
        ) : (
          <section className="wikiResultSurface">
            <div className="wikiResultHeader">
              <div className="wikiResultHeaderCopy">
                <div className="wikiHeroMark">{leadLabel}</div>
                <h2 className="sectionTitle">{resultTitle}</h2>
                <div className="wikiMetaRow">
                  <span className="memPill">{scopeLabel || 'workspace'}</span>
                  <span className="memPill">{mode}</span>
                  <span className="memPill">{viewMode === 'article' ? 'wiki article' : 'raw'}</span>
                  <span className="memPill">{formatNumber(results.length + weakResults.length)} fragments</span>
                  {isRecallMode && recall ? <span className="memPill">{formatNumber(recall.tokens_used)} / {formatNumber(recall.tokens_budget)} tokens</span> : null}
                </div>
              </div>
              {selectedCount > 0 ? (
                <div className="wikiSelectionBadge">
                  <span className="memPill">{selectedCount} selected</span>
                  <details className="wikiSelectionMenu">
                    <summary className="wikiSelectionSummary">
                      <span className="memPill">consolidate v</span>
                    </summary>
                    <div className="wikiSelectionActions">
                      <button className="btn btnGhost" type="button" onClick={onOpenConsolidated}>
                        Open
                      </button>
                      <button className="btn btnGhost" type="button" onClick={onDownloadSelection}>
                        Download
                      </button>
                      <button className="btn btnGhost" type="button" onClick={onPrintSelection}>
                        Print
                      </button>
                      <button className="btn btnGhost wikiDeleteAction" type="button" onClick={onDeleteSelection} disabled={deleteBusy}>
                        {deleteBusy ? 'Deleting...' : 'Delete'}
                      </button>
                    </div>
                  </details>
                </div>
              ) : null}
            </div>

            {error ? <div className="callout calloutBad">{error}</div> : null}
            {working ? (
              <section className="wikiLoadingCard" aria-live="polite" aria-busy="true">
                <div className="wikiLoadingTopline">
                  <span className="memPill memPillAccent">{loadingLabel}</span>
                  <span className="memPill wikiQuietBadge">{scopeLabel || 'workspace'}</span>
                  <span className="memPill wikiQuietBadge">{mode}</span>
                </div>
                <div className="wikiLoadingTrack" aria-hidden="true">
                  <span className="wikiLoadingTrackFill" />
                </div>
                <div className="wikiLoadingPulseRow" aria-hidden="true">
                  <span className="wikiLoadingPulse wikiLoadingPulseA" />
                  <span className="wikiLoadingPulse wikiLoadingPulseB" />
                  <span className="wikiLoadingPulse wikiLoadingPulseC" />
                </div>
                <div className="muted small">Stitching fragments into one wiki view.</div>
              </section>
            ) : null}
            {!working && !error && !hasResults ? <div className="emptyStateCard">No results found for this wiki view.</div> : null}

            {isRecallMode && recall?.context_block ? (
              <section className="wikiRecallCard">
                <div className="wikiMetaRow">
                  <span className="memPill">context block</span>
                  <span className="memPill">top-k {formatNumber(recall.requested_top_k)}</span>
                  <span className="memPill">budget {formatNumber(recall.requested_budget)}</span>
                </div>
                <pre className="wikiRecallContext">{recall.context_block}</pre>
              </section>
            ) : null}

            {pinnedResults.length > 0 ? (
              <section className="wikiPinnedRail">
                <div className="wikiSectionLead">[pin rail]</div>
                <div className="wikiArticleList">
                  {pinnedResults.map((memory) => (
                    <WikiMemoryFragment
                      key={buildMemoryKey(memory)}
                      memory={memory}
                      theme={theme}
                      raw={viewMode === 'raw'}
                      selected={selectedIds.has(buildMemoryKey(memory))}
                      pinned={isPinned(memory)}
                      pinBusy={isPinBusy(memory)}
                      onToggleSelection={onToggleSelection}
                      onOpenMemory={onOpenMemory}
                      onOpenDiagram={onOpenDiagram}
                      onTogglePin={onTogglePin}
                    />
                  ))}
                </div>
              </section>
            ) : null}

            {results.length > 0 ? (
              <div className={viewMode === 'raw' ? 'wikiArticleList wikiArticleListRaw' : 'wikiArticleList'}>
                {results.map((memory) => (
                  <WikiMemoryFragment
                    key={buildMemoryKey(memory)}
                    memory={memory}
                    theme={theme}
                    raw={viewMode === 'raw'}
                    selected={selectedIds.has(buildMemoryKey(memory))}
                    pinned={isPinned(memory)}
                    pinBusy={isPinBusy(memory)}
                    onToggleSelection={onToggleSelection}
                    onOpenMemory={onOpenMemory}
                    onOpenDiagram={onOpenDiagram}
                    onTogglePin={onTogglePin}
                  />
                ))}
              </div>
            ) : null}

            {weakResults.length > 0 ? (
              <section className="wikiWeakTail">
                <button className="wikiWeakTailToggle" type="button" onClick={() => setWeakTailOpen((current) => !current)} aria-expanded={weakTailOpen}>
                  <span className="wikiMetaRow">
                    <span className="memPill wikiQuietBadge">weak</span>
                    <span className="memPill wikiQuietBadge">tail</span>
                    <span className="memPill wikiQuietBadge">lower confidence</span>
                    <span className="memPill wikiQuietBadge">{formatNumber(weakResults.length)} fragments</span>
                  </span>
                  <span className="wikiWeakTailToggleText">[{weakTailOpen ? 'collapse' : 'expand'}]</span>
                </button>
                {weakTailOpen ? (
                  <div className={viewMode === 'raw' ? 'wikiArticleList wikiArticleListRaw' : 'wikiArticleList'}>
                    {weakResults.map((memory) => (
                      <WikiMemoryFragment
                        key={buildMemoryKey(memory)}
                        memory={memory}
                        theme={theme}
                        raw={viewMode === 'raw'}
                        weak
                        selected={selectedIds.has(buildMemoryKey(memory))}
                        pinned={isPinned(memory)}
                        pinBusy={isPinBusy(memory)}
                        onToggleSelection={onToggleSelection}
                        onOpenMemory={onOpenMemory}
                        onOpenDiagram={onOpenDiagram}
                        onTogglePin={onTogglePin}
                      />
                    ))}
                  </div>
                ) : null}
              </section>
            ) : null}
          </section>
        )}
      </div>

      <div className={dockExpanded ? (optionsOpen ? 'wikiDock wikiDockFocused wikiDockOpen' : 'wikiDock wikiDockFocused') : 'wikiDock wikiDockCollapsed'}>
        <div
          ref={dockShellRef}
          className="wikiDockShell"
          onFocusCapture={() => setDockFocused(true)}
          onBlurCapture={(e) => {
            const nextTarget = e.relatedTarget
            if (nextTarget instanceof Node && e.currentTarget.contains(nextTarget)) return
            setDockFocused(false)
          }}
        >
          <div className="wikiDockRow wikiDockRowPrimary">
            <textarea
              className="wikiSearchInput"
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder={inputPlaceholder}
              disabled={isRecentsMode}
              rows={dockExpanded || query.trim().length > 0 ? 3 : 1}
              aria-label="Wiki query"
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  onSubmit()
                }
              }}
            />
          </div>
          <div className="wikiDockRow wikiDockRowActions">
            <div className="wikiDockControlGroup">
              <select className="input wikiInlineSelect" value={scope} onChange={(e) => onScopeChange(e.target.value)} aria-label="Wiki project scope">
                <option value={ALL_PROJECTS_SCOPE}>all projects</option>
                {projects.map((project) => (
                  <option key={project.name} value={project.name}>
                    {project.name}
                  </option>
                ))}
              </select>
              <select className="input wikiInlineSelect" value={viewMode} onChange={(e) => onViewModeChange(e.target.value as WikiViewMode)} aria-label="Wiki result mode">
                <option value="article">wiki article</option>
                <option value="raw">raw</option>
              </select>
            </div>
            <div className="wikiDockButtonGroup">
              <button className="btn btnGhost" type="button" onClick={onToggleOptions}>
                {optionsOpen ? 'options -' : 'options v'}
              </button>
              <button className="btn btnGhost" type="button" onClick={onClearView}>
                clear
              </button>
              <button className={working ? 'sendBtn wikiSendBtnBusy' : 'sendBtn'} type="button" onClick={onSubmit} disabled={working || (!isRecentsMode && !query.trim())}>
                <span className="sendBtnLabel">{submitLabel}</span>
                {working ? <span className="wikiDockBusyDots" aria-hidden="true"><span>.</span><span>.</span><span>.</span></span> : null}
              </button>
            </div>
          </div>

          {optionsOpen ? (
            <div className="wikiDockAdvanced">
              <div className="wikiFilterGrid">
                {isRecallMode ? (
                  <>
                    <div className="row row2">
                      <div>
                        <label className="label">Recall top-k</label>
                        <input className="input" type="number" min={1} max={200} value={recallTopK} onChange={(e) => onSetRecallTopK(Number(e.target.value))} />
                      </div>
                      <div>
                        <label className="label">Token budget</label>
                        <input className="input" type="number" min={200} max={32000} step={100} value={budget} onChange={(e) => onSetBudget(Number(e.target.value))} />
                      </div>
                    </div>
                    <label className="check">
                      <input type="checkbox" checked={explain} onChange={(e) => onSetExplain(e.target.checked)} />
                      Explain recall policy
                    </label>
                    <div className="emptyStateCard">
                      Recall keeps a single-project scope for now and turns the included memories into one stitched article with a context block.
                    </div>
                  </>
                ) : isRecentsMode ? (
                  <>
                    <div>
                      <label className="label">Recent limit</label>
                      <input className="input" type="number" min={1} max={100} value={topK} onChange={(e) => onSetTopK(Number(e.target.value))} />
                    </div>
                    <div className="emptyStateCard">
                      Recents loads the latest captured memories from the selected scope and sorts them by freshest timestamp first.
                    </div>
                  </>
                ) : (
                  <>
                    <div className="semanticFilterCard">
                      <div className="semanticFilterHeader">
                        <div>
                          <label className="label">Semantic score</label>
                          <div className="semanticFilterHint">Primary relevance control reused from dashboard search.</div>
                        </div>
                        <button className="btn btnGhost semanticPresetReset" type="button" onClick={() => onSetMinSemantic(SEARCH_DEFAULT_MIN_SEMANTIC_SCORE)}>
                          reset
                        </button>
                      </div>
                      <input className="semanticSlider" type="range" min={0} max={1} step={0.05} value={semanticThreshold} onChange={(e) => onSetMinSemantic(Number(e.target.value))} />
                      <div className="semanticFilterSummary">
                        <div className="semanticThresholdValue">{semanticThreshold.toFixed(2)}</div>
                        <div className="semanticThresholdCopy">
                          <div className="semanticThresholdLabel">Active search floor</div>
                          <div className="semanticThresholdHint">Controls stitched-result strictness.</div>
                        </div>
                      </div>
                    </div>

                    <div className="row row2">
                      <div>
                        <label className="label">Top K</label>
                        <input className="input" type="number" min={1} max={200} value={topK} onChange={(e) => onSetTopK(Number(e.target.value))} />
                      </div>
                      <div>
                        <label className="label">Min total</label>
                        <input className="input" inputMode="decimal" value={minTotal} onChange={(e) => onSetMinTotal(e.target.value)} placeholder="0.00 - 1.00" />
                      </div>
                    </div>

                    <div className="row row2">
                      <div>
                        <label className="label">Min confidence</label>
                        <input className="input" inputMode="decimal" value={minConfidence} onChange={(e) => onSetMinConfidence(e.target.value)} placeholder="0.00 - 1.00" />
                      </div>
                      <label className="check">
                        <input type="checkbox" checked={explain} onChange={(e) => onSetExplain(e.target.checked)} />
                        Explain scoring
                      </label>
                    </div>

                    <div>
                      <label className="label">Outcome</label>
                      <select className="input" value={outcome} onChange={(e) => onSetOutcome(e.target.value as OutcomeResult | '')}>
                        <option value="">any</option>
                        <option value="success">success</option>
                        <option value="failure">failure</option>
                        <option value="partial">partial</option>
                      </select>
                    </div>

                    <div>
                      <label className="label">Types</label>
                      <div className="chips">
                        {allTypes.map((typeItem) => (
                          <label key={typeItem.key} className={types.has(typeItem.key) ? 'chip chipOn' : 'chip'}>
                            <input type="checkbox" checked={types.has(typeItem.key)} onChange={(e) => onToggleType(typeItem.key, e.target.checked)} />
                            {typeItem.label}
                          </label>
                        ))}
                      </div>
                    </div>

                    <div>
                      <label className="label">Tiers</label>
                      <div className="chips">
                        {allTiers.map((tierItem) => (
                          <label key={tierItem.key} className={tiers.has(tierItem.key) ? 'chip chipOn' : 'chip'}>
                            <input type="checkbox" checked={tiers.has(tierItem.key)} onChange={(e) => onToggleTier(tierItem.key, e.target.checked)} />
                            {tierItem.label}
                          </label>
                        ))}
                      </div>
                    </div>
                  </>
                )}
              </div>
              <div className="wikiDockFooter">
                <button className="btn btnGhost" type="button" onClick={onCollapseOptions}>
                  simple mode
                </button>
                <span className="muted small">
                  {isRecallMode
                    ? 'Recall distills task context into one article and keeps weaker fragments in a quiet tail.'
                    : isRecentsMode
                      ? 'Recents keeps the latest captured memories inside the same wiki reader.'
                      : 'Search stitches memories into one reading flow and keeps weaker fragments in a quiet tail.'}
                </span>
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </section>
  )
}

function WikiMemoryFragment({
  memory,
  theme,
  raw = false,
  weak = false,
  selected,
  pinned,
  pinBusy = false,
  onToggleSelection,
  onOpenMemory,
  onOpenDiagram,
  onTogglePin,
}: {
  memory: MemoryEntry
  theme: 'light' | 'dark'
  raw?: boolean
  weak?: boolean
  selected: boolean
  pinned: boolean
  pinBusy?: boolean
  onToggleSelection: (memory: MemoryEntry) => void
  onOpenMemory: (memory: MemoryEntry) => void
  onOpenDiagram: (memory: MemoryEntry) => void
  onTogglePin: (memory: MemoryEntry) => void
}) {
  const semanticSimilarity = getSemanticSimilarity(memory)
  const semanticRelevance = getSemanticRelevance(semanticSimilarity)
  const pinLabel = pinBusy ? 'wait' : pinned ? 'unpin' : 'pin'

  return (
    <article
      className={raw ? 'wikiFragment wikiFragmentRaw' : 'wikiFragment'}
      onClick={() => onOpenMemory(memory)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onOpenMemory(memory)
        }
      }}
      role="button"
      tabIndex={0}
      aria-label={`Open memory ${memory.id}`}
    >
      <div className="wikiFragmentTop">
        <button
          className={selected ? 'wikiSelect wikiSelectOn' : 'wikiSelect'}
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            onToggleSelection(memory)
          }}
          aria-label={selected ? 'Deselect memory fragment' : 'Select memory fragment'}
        >
          {selected ? '[x]' : '[ ]'}
        </button>
        <div className="wikiFragmentBadges">
          <span className="memPill">{memory.workspace}</span>
          <span className="memPill">{memory.type}</span>
          <span className="memPill">{memory.storage_tier}</span>
          {typeof semanticSimilarity === 'number' ? (
            <span className={`memPill relevancePill relevancePill${toTitle(semanticRelevance.tone)}`}>
              {semanticRelevance.label} {formatScore(semanticSimilarity, 2)}
            </span>
          ) : null}
          {weak ? <span className="memPill wikiQuietBadge">weak</span> : null}
        </div>
        <button
          className="btn btnGhost wikiPinButton"
          type="button"
          disabled={pinBusy}
          onClick={(e) => {
            e.stopPropagation()
            onTogglePin(memory)
          }}
        >
          [{pinLabel}]
        </button>
        {memory.diagram ? (
          <button
            className="btn btnGhost wikiDiagramButton"
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              onOpenDiagram(memory)
            }}
          >
            [diagram]
          </button>
        ) : null}
      </div>
      <div className="wikiFragmentBody">
        <MarkdownView markdown={memory.content} clamp={false} theme={theme} />
      </div>
      {memory.diagram ? (
        <div
          className="diagramBlock wikiFragmentDiagram"
          onClick={(e) => {
            e.stopPropagation()
          }}
        >
          <DiagramViewer diagram={memory.diagram} theme={theme} />
        </div>
      ) : null}
    </article>
  )
}

function QueryEmptyState({ mode, onOpenOverview }: { mode: ChatMode; onOpenOverview: () => void }) {
  return (
    <div className="emptyStateCard">
      <div className="overviewEyebrow">{mode === 'search' ? 'Search' : 'Recall'}</div>
      <div className="sectionTitle">{mode === 'search' ? 'Start a memory search' : 'Build a recall preview'}</div>
      <p className="sectionText">
        Use the composer below to query the selected workspace.
      </p>
      <button className="btn" type="button" onClick={onOpenOverview}>
        Overview
      </button>
    </div>
  )
}

function MetricCard({ title, value, detail }: { title: string; value: string; detail: string }) {
  return (
    <article className="metricCard">
      <div className="metricLabel">{title}</div>
      <div className="metricValue">{value}</div>
      <div className="metricDetail">{detail}</div>
    </article>
  )
}

function BreakdownCard({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <section className="breakdownCard">
      <div className="breakdownHeader">
        <div className="breakdownTitle">{title}</div>
        <div className="breakdownSubtitle">{subtitle}</div>
      </div>
      {children}
    </section>
  )
}

function PieChartBreakdown({ entries, emptyLabel }: { entries: Array<[string, number]>; emptyLabel: string }) {
  const total = entries.reduce((sum, [, value]) => sum + value, 0)
  if (entries.length === 0) return <div className="muted">{emptyLabel}</div>
  return (
    <div className="pieChartBlock">
      <div className="pieChartWrap" aria-hidden="true">
        <div className="pieChartVisual" style={{ background: buildPieGradient(entries) }}>
          <div className="pieChartCenter">
            <span className="pieChartTotal">{formatNumber(total)}</span>
            <span className="pieChartCaption">total</span>
          </div>
        </div>
      </div>
      <div className="breakdownList">
        {entries.map(([label, value], index) => {
          const percent = total > 0 ? (value / total) * 100 : 0
          return (
            <div key={label} className="breakdownRow">
              <div className="breakdownRowTop">
                <span className="breakdownLabelGroup">
                  <span className="breakdownIndex">{formatLegendIndex(index)}</span>
                  <span className="breakdownSwatch" style={{ background: chartColor(index) }} />
                  <span className="breakdownLead">.-</span>
                  <span className="breakdownLabelText">{toTitle(label)}</span>
                  <span className="breakdownLeader" aria-hidden="true" />
                </span>
                <span className="mono breakdownValue">
                  {formatNumber(value)} / {formatPercent(percent)}
                </span>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function ComparisonSection({
  title,
  description,
  emptyLabel,
  children,
}: {
  title: string
  description: string
  emptyLabel: string
  children: React.ReactNode
}) {
  const items = React.Children.toArray(children)
  return (
    <section className="comparisonSection">
      <div className="comparisonHeader">
        <div>
          <div className="breakdownTitle">{title}</div>
          <div className="breakdownSubtitle">{description}</div>
        </div>
      </div>
      {items.length === 0 ? <div className="emptyInline">{emptyLabel}</div> : <div className="comparisonGrid">{items}</div>}
    </section>
  )
}

function TokenGroupCard({ group, index }: { group: TokenMetricGroupTotals; index: number }) {
  const recall = getGroupOperationTotals(group, 'recall')
  const totals = recall ?? group
  const savingsLabel = recall ? 'of recall baseline' : 'of baseline'
  const recordsLabel = recall ? 'recall records' : 'records'
  return (
    <article className="groupCard">
      <div className="groupCardTop">
        <div className="groupHeading">
          <span className="groupIndex">{formatLegendIndex(index)}</span>
          <span className="groupLead">.-</span>
          <span className="groupTitle">{group.run_label || 'default'}</span>
        </div>
        <span className={group.memory_enabled ? 'groupBadge groupBadgeOn' : 'groupBadge groupBadgeOff'}>{group.memory_enabled ? 'memory on' : 'memory off'}</span>
      </div>
      <div className="groupMetric">{formatNumber(totals.saved_tokens)} saved</div>
      <div className="groupSub">{formatPercent(totals.baseline_tokens > 0 ? (totals.saved_tokens / totals.baseline_tokens) * 100 : 0)} {savingsLabel} across {formatNumber(totals.records)} {recordsLabel}</div>
      <div className="groupStats">
        <DiagnosticRow label="Returned" value={formatNumber(totals.returned_tokens)} />
        <DiagnosticRow label="Baseline" value={formatNumber(totals.baseline_tokens)} />
      </div>
    </article>
  )
}

function LLMGroupCard({ group, index }: { group: LLMUsageGroupTotals; index: number }) {
  return (
    <article className="groupCard">
      <div className="groupCardTop">
        <div className="groupHeading">
          <span className="groupIndex">{formatLegendIndex(index)}</span>
          <span className="groupLead">.-</span>
          <span className="groupTitle">{group.run_label || 'default'}</span>
        </div>
        <span className={group.memory_enabled ? 'groupBadge groupBadgeOn' : 'groupBadge groupBadgeOff'}>{group.memory_enabled ? 'memory on' : 'memory off'}</span>
      </div>
      <div className="groupMetric">{formatNumber(group.total_tokens)} total</div>
      <div className="groupSub">{formatNumber(group.records)} usage reports captured</div>
      <div className="groupStats">
        <DiagnosticRow label="Prompt" value={formatNumber(group.prompt_tokens)} />
        <DiagnosticRow label="Completion" value={formatNumber(group.completion_tokens)} />
      </div>
    </article>
  )
}

function DiagnosticRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="diagnosticRow">
      <span className="diagnosticLabelGroup">
        <span className="diagnosticIndex" aria-hidden="true" />
        <span className="diagnosticLead">.-</span>
        <span className="diagnosticLabel">{label}</span>
        <span className="diagnosticLeader" aria-hidden="true" />
      </span>
      <span className="diagnosticValue">{value}</span>
    </div>
  )
}

function Message({
  m,
  theme,
  selectedId,
  onSelectMemory,
}: {
  m: ChatMessage
  theme: 'light' | 'dark'
  selectedId?: string
  onSelectMemory: (m: MemoryEntry) => void
}) {
  const isUser = m.role === 'user'
  const isSystem = m.role === 'system'
  return (
    <div className={isSystem ? 'msg msgSystem' : isUser ? 'msg msgUser' : 'msg msgAssistant'}>
      <div className="msgInner">
        <div className="msgMeta">
          <span className="msgRole">{isSystem ? 'System' : isUser ? 'You' : 'Memory'}</span>
          <span className="msgTime">{formatClock(m.createdAt)}</span>
        </div>
        <div className="msgBody">
          <MarkdownView markdown={m.text} clamp={false} theme={theme} />
          {m.error ? <div className="callout calloutBad">{m.error}</div> : null}

          {m.payload?.results ? (
            <div className="assistantBlock">
              <div className="assistantHdr">
                <div className="assistantTitle">Results</div>
                <div className="muted small">{m.payload.results.length}</div>
              </div>
              {m.payload.search?.retrieval_policy ? (
                <details className="detailsFold">
                  <summary className="detailsSum">Retrieval policy</summary>
                  <pre className="pre">{JSON.stringify(m.payload.search.retrieval_policy, null, 2)}</pre>
                </details>
              ) : null}
              <div className="assistantList">
                {m.payload.results.map((r) => (
                  <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                ))}
              </div>
              {m.payload.search?.weak_results?.length ? (
                <>
                  <div className="assistantHdr">
                    <div className="assistantTitle">Weak familiarity</div>
                    <div className="muted small">{m.payload.search.weak_results.length}</div>
                  </div>
                  <div className="assistantList">
                    {m.payload.search.weak_results.map((r) => (
                      <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                    ))}
                  </div>
                </>
              ) : null}
              {m.payload.search?.suppressed_results?.length ? (
                <details className="detailsFold">
                  <summary className="detailsSum">Suppressed ({m.payload.search.suppressed_results.length})</summary>
                  <div className="assistantList">
                    {m.payload.search.suppressed_results.map((r) => (
                      <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                    ))}
                  </div>
                </details>
              ) : null}
            </div>
          ) : null}

          {m.payload?.recall ? (
            <div className="assistantBlock">
              <div className="assistantHdr">
                <div className="assistantTitle">Recall context</div>
                <div className="muted small">
                  {m.payload.recall.tokens_used}/{m.payload.recall.tokens_budget}
                </div>
              </div>
              <details className="detailsFold" open>
                <summary className="detailsSum">Context block</summary>
                <div className="detailsTools">
                  <button className="btn btnGhost" onClick={() => navigator.clipboard.writeText(m.payload!.recall!.context_block)}>
                    Copy
                  </button>
                </div>
                <pre className="pre preTall">{m.payload.recall.context_block}</pre>
              </details>

              <div className="assistantHdr">
                <div className="assistantTitle">Included memories</div>
                <div className="muted small">{m.payload.recall.memories_included_full?.length ?? 0}</div>
              </div>
              <div className="assistantList">
                {(m.payload.recall.memories_included_full ?? []).map((r) => (
                  <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                ))}
              </div>

              {m.payload.recall.retrieval_policy ? (
                <details className="detailsFold">
                  <summary className="detailsSum">Retrieval policy</summary>
                  <pre className="pre">{JSON.stringify(m.payload.recall.retrieval_policy, null, 2)}</pre>
                </details>
              ) : null}

              {m.payload.recall.weak_memories?.length ? (
                <>
                  <div className="assistantHdr">
                    <div className="assistantTitle">Weak familiarity</div>
                    <div className="muted small">{m.payload.recall.weak_memories.length}</div>
                  </div>
                  <div className="assistantList">
                    {m.payload.recall.weak_memories.map((r) => (
                      <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                    ))}
                  </div>
                </>
              ) : null}

              {m.payload.recall.suppressed_memories?.length ? (
                <details className="detailsFold">
                  <summary className="detailsSum">Suppressed ({m.payload.recall.suppressed_memories.length})</summary>
                  <div className="assistantList">
                    {m.payload.recall.suppressed_memories.map((r) => (
                      <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                    ))}
                  </div>
                </details>
              ) : null}

              {m.payload.recall.memories_clipped?.length ? (
                <details className="detailsFold">
                  <summary className="detailsSum">Clipped ({m.payload.recall.memories_clipped.length})</summary>
                  <pre className="pre">{JSON.stringify(m.payload.recall.memories_clipped, null, 2)}</pre>
                </details>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function ResultCard({
  m,
  theme,
  isSelected,
  onSelect,
}: {
  m: MemoryEntry
  theme: 'light' | 'dark'
  isSelected: boolean
  onSelect: (m: MemoryEntry) => void
}) {
  const timeStr = formatTS(m.created_at || m.updated_at)
  const semanticSimilarity = getSemanticSimilarity(m)
  const semanticRelevance = getSemanticRelevance(semanticSimilarity)
  return (
    <article className={isSelected ? 'memCard memCardOn' : 'memCard'} onClick={() => onSelect(m)}>
      <div className="memHdr">
        <div className="memHdrLeft">
          <span className="memDot" aria-hidden="true" />
          <span className="mono memID" title={m.id}>{m.id.slice(0, 16)}...</span>
        </div>
        <div className="memHdrRight">
          {timeStr ? <span className="memTime" style={{ fontSize: '12px', opacity: 0.7 }}>{timeStr}</span> : null}
        </div>
      </div>
      <div className="memBody memBodyCompact">
        <MarkdownView markdown={m.content} clamp={true} theme={theme} />
      </div>
      {typeof semanticSimilarity === 'number' ? (
        <div className="memSignal">
          <div className="memSignalTop">
            <span className="memSignalLabel">semantic similarity</span>
            <span className={`memPill relevancePill relevancePill${toTitle(semanticRelevance.tone)}`}>{semanticRelevance.label}</span>
          </div>
          <div className="memSignalValue">{formatScore(semanticSimilarity, 3)}</div>
          <div className="memSignalHint">Primary relevance signal from `score_breakdown.semantic_similarity`.</div>
        </div>
      ) : null}
      <div className="memFooter">
        <div className="memFooterLeft">
          <span className="memPill memPillAccent">{m.type}</span>
          <span className="memPill">{m.storage_tier}</span>
          {hasDiagram(m) ? <span className="memPill memPillVisual">visual</span> : null}
        </div>
        <div className="memFooterRight">
          {typeof m.score === 'number' ? (
            <span className="memMetric">Blended: <strong>{formatScore(m.score, 3)}</strong></span>
          ) : null}
          <span className="memMetric">Conf: <strong>{Math.round(m.confidence * 100)}%</strong></span>
        </div>
      </div>
    </article>
  )
}
