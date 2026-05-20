import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  getStats,
  listObservations,
  listSessions,
  listProjects,
  listRecentMemories,
  promoteObservations,
  recallPreview,
  searchMemories,
  type CountMap,
  type DashboardStats,
  type LLMUsageGroupTotals,
  type MemoryEntry,
  type MemoryType,
  type ObservationEntry,
  type ObservationPromotionResult,
  type OutcomeResult,
  type ProjectListItem,
  type RecallPreviewResponse,
  type SessionEntry,
  type StorageTier,
  type TokenMetricGroupTotals,
} from '../lib/api'
import { DiagramViewer } from './DiagramViewer'
import { MarkdownView } from './MarkdownView'

type ChatMode = 'search' | 'recall'
type Surface = 'overview' | 'search' | 'recall' | 'diagnostics' | 'sessions'

type ChatMessage = {
  id: string
  role: 'user' | 'assistant' | 'system'
  mode: ChatMode
  text: string
  createdAt: number
  payload?: {
    results?: MemoryEntry[]
    recall?: RecallPreviewResponse
  }
  pending?: boolean
  error?: string
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
  const [surface, setSurface] = useState<Surface>('overview')
  const [mode, setMode] = useState<ChatMode>('search')

  const [projects, setProjects] = useState<ProjectListItem[]>([])
  const [workspace, setWorkspace] = useState<string>('')
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [statsErr, setStatsErr] = useState<string>('')
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
  const composerExpanded = composerFocused || advancedOpen || draft.trim().length > 0

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
      entities: parsedEntities.length ? parsedEntities : undefined,
      date_from: fromDate || undefined,
      date_to: toDate || undefined,
    }
  }, [entities, fromDate, minConfidence, minDecay, outcome, tiers, toDate, types])

  function openSearch() {
    setMode('search')
    setSurface('search')
    setSelectedMemory(null)
  }

  function openRecall() {
    setMode('recall')
    setSurface('recall')
    setSelectedMemory(null)
  }

  function openSessions() {
    setSurface('sessions')
    setSelectedMemory(null)
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
    if (!workspace) return
    if (recentsBusy) return
    openSearch()
    setRecentsBusy(true)
    setSelectedMemory(null)
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode: 'search',
      text: 'Loading recent memories...',
      createdAt: Date.now(),
      pending: true,
    }
    setMessages((m) => [...m, pending])
    try {
      const r = await listRecentMemories({ workspace, limit: topK })
      const hitCount = r.results?.length ?? 0
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: hitCount > 0 ? `Recent memories (${hitCount}).` : 'No recent memories found.',
                payload: { results: r.results ?? [] },
              }
            : x,
        ),
      )
    } catch (e) {
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: 'Request failed.',
                error: e instanceof Error ? e.message : String(e),
              }
            : x,
        ),
      )
    } finally {
      setRecentsBusy(false)
    }
  }

  async function runSearchFlow(query: string) {
    const text = query.trim()
    if (!workspace || !text) return
    openSearch()
    setBusy(true)
    setSelectedMemory(null)
    const userMsg: ChatMessage = {
      id: makeID(),
      role: 'user',
      mode: 'search',
      text,
      createdAt: Date.now(),
    }
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode: 'search',
      text: 'Searching...',
      createdAt: Date.now(),
      pending: true,
    }
    setMessages((m) => [...m, userMsg, pending])
    try {
      const r = await searchMemories({
        workspace,
        query: text,
        top_k: topK,
        explain,
        filters,
      })
      const hitCount = r.results?.length ?? 0
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: hitCount > 0 ? `Found ${hitCount} memories.` : 'No results found.',
                payload: { results: r.results ?? [] },
              }
            : x,
        ),
      )
      if (hitCount === 0) {
        setDeepSearchPrompt({ open: true, query: text })
      }
    } catch (e) {
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: 'Request failed.',
                error: e instanceof Error ? e.message : String(e),
              }
            : x,
        ),
      )
    } finally {
      setBusy(false)
    }
  }

  async function runRecallFlow(task: string) {
    const text = task.trim()
    if (!workspace || !text) return
    openRecall()
    setBusy(true)
    setSelectedMemory(null)
    const userMsg: ChatMessage = {
      id: makeID(),
      role: 'user',
      mode: 'recall',
      text,
      createdAt: Date.now(),
    }
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode: 'recall',
      text: 'Recalling...',
      createdAt: Date.now(),
      pending: true,
    }
    setMessages((m) => [...m, userMsg, pending])
    try {
      const r = await recallPreview({
        workspace,
        task_description: text,
        top_k: recallTopK,
        token_budget: budget,
        explain,
        include_memories: true,
      })
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: `Recall preview: ${r.tokens_used}/${r.tokens_budget} tokens.`,
                payload: { recall: r },
              }
            : x,
        ),
      )
    } catch (e) {
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: 'Request failed.',
                error: e instanceof Error ? e.message : String(e),
              }
            : x,
        ),
      )
    } finally {
      setBusy(false)
    }
  }

  async function runDeepSearch(query: string) {
    const q = query.trim()
    if (!q || !workspace) return
    openRecall()
    setDeepSearchPrompt({ open: false, query: '' })
    setBusy(true)
    setSelectedMemory(null)
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode: 'recall',
      text: 'Deep searching (recall preview)...',
      createdAt: Date.now(),
      pending: true,
    }
    setMessages((m) => [...m, pending])
    try {
      const r = await recallPreview({
        workspace,
        task_description: q,
        top_k: recallTopK,
        token_budget: budget,
        explain,
        include_memories: true,
      })
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: `Deep search: ${r.tokens_used}/${r.tokens_budget} tokens.`,
                payload: { recall: r },
              }
            : x,
        ),
      )
    } catch (e) {
      setMessages((m) =>
        m.map((x) =>
          x.id === pendingID
            ? {
                ...x,
                pending: false,
                text: 'Deep search failed.',
                error: e instanceof Error ? e.message : String(e),
              }
            : x,
        ),
      )
    } finally {
      setBusy(false)
    }
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

  return (
    <div className="shell chatShell">
      <header className="topbar chatTopbar">
        <div className="topbarLeft">
          <div className="brand">
            <div className="brandMark" aria-hidden="true">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ color: '#090d16' }}>
                <path d="M12 2l2 6 6 2-6 2-2 6-2-6-6-2 6-2 2-6z" />
              </svg>
            </div>
            <div className="brandText">
              <div className="brandTitle">
                Agent Memory <span className="mono" style={{ fontSize: '11px', opacity: 0.6, fontWeight: 500 }}>v1.0.12</span>
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
            <span className="navLabel">Overview</span>
          </button>
          <button className={surface === 'search' ? 'navItem navItemOn' : 'navItem'} onClick={openSearch} type="button" aria-label="Search mode">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="11" cy="11" r="7" />
              <path d="M21 21l-4.3-4.3" />
            </svg>
            <span className="navLabel">Search</span>
          </button>
          <button className={surface === 'recall' ? 'navItem navItemOn' : 'navItem'} onClick={openRecall} type="button" aria-label="Recall preview mode">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M3 12a9 9 0 101.8-5.4" />
              <path d="M3 4v6h6" />
            </svg>
            <span className="navLabel">Recall Preview</span>
          </button>
          <button className="navItem" onClick={showRecentsCapture} type="button" aria-label="Recents capture" disabled={recentsBusy || !workspace} title="Show recently added memories">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="9" />
              <path d="M12 7v5l3 2" />
            </svg>
            <span className="navLabel">Recents</span>
          </button>
          <button className={surface === 'sessions' ? 'navItem navItemOn' : 'navItem'} onClick={openSessions} type="button" aria-label="Sessions">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <path d="M8 2v4M16 2v4M3 10h18" />
            </svg>
            <span className="navLabel">Sessions</span>
          </button>
          <button className={surface === 'diagnostics' ? 'navItem navItemOn' : 'navItem'} onClick={() => setSurface('diagnostics')} type="button" aria-label="Diagnostics">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M12 20v-6M6 20V10M18 20V4" />
            </svg>
            <span className="navLabel">Diagnostics</span>
          </button>
        </nav>

        <div className="topbarRight">
          <button
            className="iconBtn iconBtnInfo"
            onClick={() => setRawStatsOpen(true)}
            aria-label="Raw stats payload"
            title="Raw stats payload"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="10" />
              <path d="M12 16v-4M12 8h.01" />
            </svg>
          </button>
          <button
            className="iconBtn"
            onClick={() => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))}
            title={theme === 'dark' ? 'Switch to Light Mode' : 'Switch to Dark Mode'}
            aria-label={theme === 'dark' ? 'Switch to Light Mode' : 'Switch to Dark Mode'}
          >
            {theme === 'dark' ? (
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="5" />
                <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
              </svg>
            ) : (
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z" />
              </svg>
            )}
          </button>
        </div>
      </header>

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
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                  <rect x="10" y="3" width="4" height="4" rx="1" />
                  <rect x="3" y="17" width="4" height="4" rx="1" />
                  <rect x="17" y="17" width="4" height="4" rx="1" />
                  <path d="M12 7v5M12 12H5v5M12 12h7v5" />
                </svg>
                <span>Diagrams</span>
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

            <div
              className={composerExpanded ? 'composerDock composerDockExpanded' : 'composerDock composerDockCollapsed'}
              ref={composerRef}
              onFocusCapture={() => setComposerFocused(true)}
              onBlurCapture={(e) => {
                const nextTarget = e.relatedTarget
                if (nextTarget instanceof Node && composerRef.current?.contains(nextTarget)) return
                setComposerFocused(false)
                if (!draft.trim()) setAdvancedOpen(false)
              }}
            >
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
                  placeholder={mode === 'search' ? 'Search your memory system...' : 'Describe the task to recall...'}
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
                      {surface === 'overview' ? 'Ready to query.' : surface === 'diagnostics' ? 'Diagnostics open.' : surface === 'sessions' ? 'Sessions open.' : 'Searching this workspace.'}
                    </span>
                  </div>
                  <div className="composerToolbarRight">
                    <button className="btn btnGhost" onClick={() => setAdvancedOpen((v) => !v)}>
                      {advancedOpen ? 'Hide Advanced' : 'Advanced Settings'} ⌄
                    </button>
                    <button className="sendBtn" onClick={submit} disabled={!workspace || busy || !draft.trim()} title="Send query">
                      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M12 19V5M5 12l7-7 7 7"/></svg>
                    </button>
                  </div>
                </div>
              </div>
              <div className={composerExpanded ? 'composerFoot' : 'composerFoot composerFootCollapsed'}>
                <span className="muted small" style={{ display: 'block', textAlign: 'center' }}>
                  Served locally by <span className="mono">agent-memory serve</span>. Markdown is sanitized; Mermaid renders when present.
                </span>
              </div>
            </div>
          </div>

          {selectedMemory ? (
            <aside className="detailDrawer" aria-label="Memory details">
              <div className="detailDrawerTop">
                <div className="detailDrawerHeader">
                  <div className="detailDrawerTitle">Memory Details</div>
                  <div className="mono detailDrawerID">{selectedMemory.id}</div>
                </div>
                <button className="btn btnGhost" onClick={() => setSelectedMemory(null)} aria-label="Close details" style={{ padding: '8px 12px' }}>
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                    <path d="M18 6L6 18M6 6l12 12" />
                  </svg>
                </button>
              </div>

              <div className="detailDrawerBody">
                <div className="detailPills">
                  <span className="memPill">{selectedMemory.type}</span>
                  <span className="memPill">{selectedMemory.storage_tier}</span>
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
                        <div className="memMetaLabel">Score</div>
                        <div className="memMetaValue">{selectedMemory.score.toFixed(3)}</div>
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
          <div className="overviewMetaRow">
            <span className={`statusBadge statusBadge${toTitle(healthState.tone)}`}>{healthState.label}</span>
            <span className="overviewMetaItem">Last activity {formatTS(stats?.last_activity || project?.last_activity) || 'n/a'}</span>
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
        <MetricCard title="Memories" value={formatNumber(stats?.memory_count ?? project?.memory_count ?? 0)} detail="Current workspace volume" />
        <MetricCard title="Storage" value={formatBytes(stats?.db_size_bytes ?? project?.size_bytes)} detail="SQLite footprint on disk" />
        <MetricCard title="Token Savings" value={formatPercent(stats?.token_savings_percent)} detail={`${formatNumber(stats?.token_metrics.records)} measured operations`} />
        <MetricCard title="LLM Usage" value={formatNumber(stats?.llm_usage_totals.total_tokens)} detail={`${formatNumber(stats?.llm_usage_totals.records)} provider reports`} />
        <MetricCard title="Pinned" value={formatNumber(stats?.pinned_count)} detail="Pinned memories retained" />
        <MetricCard title="Diagrams" value={formatNumber(stats?.diagram_count)} detail="Memories with visual payloads" />
      </div>

      <div className="overviewColumns">
        <BreakdownCard title="Memory Types" subtitle={`${formatNumber(sumCounts(stats?.memory_type_counts))} total`}>
          <BreakdownList entries={typeEntries} emptyLabel="No type distribution yet." />
        </BreakdownCard>

        <BreakdownCard title="Storage Tiers" subtitle={`${formatNumber(sumCounts(stats?.storage_tier_counts))} classified`}>
          <BreakdownList entries={tierEntries} emptyLabel="No tier distribution yet." />
        </BreakdownCard>
      </div>

      <section ref={experimentsRef}>
        <ComparisonSection title="Token Experiment Comparison" description="Grouped by run label and memory enabled state." emptyLabel="No grouped token metrics yet. Run labeled ON/OFF experiments to populate this view.">
          {tokenGroups.map((group) => (
            <TokenGroupCard key={`token-${group.run_label}-${group.memory_enabled ? 'on' : 'off'}`} group={group} />
          ))}
        </ComparisonSection>

        <ComparisonSection title="LLM Usage Comparison" description="Provider-reported usage grouped by run label and memory enabled state." emptyLabel="No grouped LLM usage yet. Ingest provider usage metrics to populate this view.">
          {llmGroups.map((group) => (
            <LLMGroupCard key={`llm-${group.run_label}-${group.memory_enabled ? 'on' : 'off'}`} group={group} />
          ))}
        </ComparisonSection>
      </section>
    </section>
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
        <MetricCard title="Memories" value={formatNumber(stats?.memory_count ?? project?.memory_count ?? 0)} detail="Current workspace volume" />
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
            <DiagnosticRow label="Token Records" value={formatNumber(stats?.token_metrics.records)} />
            <DiagnosticRow label="Tokens Saved" value={formatNumber(stats?.token_metrics.saved_tokens)} />
            <DiagnosticRow label="Savings Rate" value={formatPercent(stats?.token_savings_percent)} />
            <DiagnosticRow label="LLM Records" value={formatNumber(stats?.llm_usage_totals.records)} />
          </div>
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
              {sessions.map((session) => {
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
                      <div className="sessionRailTitle">{session.session_id}</div>
                      <span className="groupBadge groupBadgeOn">{formatNumber(session.observation_count)} obs</span>
                    </div>
                    <div className="sessionRailMeta">Last seen {formatTS(session.last_seen_at) || 'n/a'}</div>
                    {promoted ? (
                      <div className="sessionRailMeta">
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
                      <div className="sessionDetailSummary">
                        <span className="overviewMetaItem">{formatNumber(selectedSession.observation_count)} observations</span>
                        <span className="overviewMetaItem">last seen {formatTS(selectedSession.last_seen_at) || 'n/a'}</span>
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
                            <div className="timelineTop">
                              <div>
                                <div className="timelineTitle">{toTitle(observation.kind)}</div>
                                <div className="timelineMeta">{formatTS(observation.occurred_at) || 'n/a'}</div>
                              </div>
                              <span className="memPill">{observation.tool_name || 'system'}</span>
                            </div>
                            <div className="timelineSummary">{observation.summary}</div>
                            <div className="timelineFooter">
                              <span className="mono">{observation.id.slice(0, 16)}...</span>
                              <span className="muted small">Created {formatTS(observation.created_at) || 'n/a'}</span>
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

function BreakdownList({ entries, emptyLabel }: { entries: Array<[string, number]>; emptyLabel: string }) {
  const max = entries[0]?.[1] ?? 0
  if (entries.length === 0) return <div className="muted">{emptyLabel}</div>
  return (
    <div className="breakdownList">
      {entries.map(([label, value]) => (
        <div key={label} className="breakdownRow">
          <div className="breakdownRowTop">
            <span>{toTitle(label)}</span>
            <span className="mono">{formatNumber(value)}</span>
          </div>
          <div className="breakdownBarTrack">
            <div className="breakdownBarFill" style={{ width: `${max > 0 ? (value / max) * 100 : 0}%` }} />
          </div>
        </div>
      ))}
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

function TokenGroupCard({ group }: { group: TokenMetricGroupTotals }) {
  return (
    <article className="groupCard">
      <div className="groupCardTop">
        <span className="groupTitle">{group.run_label || 'default'}</span>
        <span className={group.memory_enabled ? 'groupBadge groupBadgeOn' : 'groupBadge groupBadgeOff'}>{group.memory_enabled ? 'memory on' : 'memory off'}</span>
      </div>
      <div className="groupMetric">{formatNumber(group.saved_tokens)} saved</div>
      <div className="groupSub">{formatPercent(group.baseline_tokens > 0 ? (group.saved_tokens / group.baseline_tokens) * 100 : 0)} of baseline across {formatNumber(group.records)} records</div>
      <div className="groupStats">
        <DiagnosticRow label="Returned" value={formatNumber(group.returned_tokens)} />
        <DiagnosticRow label="Baseline" value={formatNumber(group.baseline_tokens)} />
      </div>
    </article>
  )
}

function LLMGroupCard({ group }: { group: LLMUsageGroupTotals }) {
  return (
    <article className="groupCard">
      <div className="groupCardTop">
        <span className="groupTitle">{group.run_label || 'default'}</span>
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
      <span className="diagnosticLabel">{label}</span>
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
              <div className="assistantList">
                {m.payload.results.map((r) => (
                  <ResultCard key={r.id} m={r} theme={theme} isSelected={r.id === selectedId} onSelect={onSelectMemory} />
                ))}
              </div>
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
      <div className="memFooter">
        <div className="memFooterLeft">
          <span className="memPill memPillAccent">{m.type}</span>
          <span className="memPill">{m.storage_tier}</span>
          {hasDiagram(m) ? <span className="memPill memPillVisual">visual</span> : null}
        </div>
        <div className="memFooterRight">
          {typeof m.score === 'number' ? (
            <span className="memMetric">Score: <strong>{m.score.toFixed(3)}</strong></span>
          ) : null}
          <span className="memMetric">Conf: <strong>{Math.round(m.confidence * 100)}%</strong></span>
        </div>
      </div>
    </article>
  )
}
