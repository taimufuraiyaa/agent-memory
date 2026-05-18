import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  getStats,
  listProjects,
  listRecentMemories,
  recallPreview,
  searchMemories,
  type MemoryEntry,
  type MemoryType,
  type OutcomeResult,
  type ProjectListItem,
  type RecallPreviewResponse,
  type StorageTier,
} from '../lib/api'
import { DiagramViewer } from './DiagramViewer'
import { MarkdownView } from './MarkdownView'

type ChatMode = 'search' | 'recall'

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

function pillList(items: string[]): React.ReactNode {
  if (!items.length) return <span className="muted">—</span>
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

export function App() {
  const [mode, setMode] = useState<ChatMode>('search')

  const [projects, setProjects] = useState<ProjectListItem[]>([])
  const [workspace, setWorkspace] = useState<string>('')
  const [stats, setStats] = useState<Record<string, unknown> | null>(null)
  const [statsErr, setStatsErr] = useState<string>('')
  const [infoOpen, setInfoOpen] = useState<boolean>(false)
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
      threadRef.current.scrollTop = threadRef.current.scrollHeight
    }
  }, [messages])

  const projectLabel = useMemo(() => {
    const p = projects.find((x) => x.name === workspace)
    if (!p) return workspace || 'workspace'
    return `${p.name} (${p.memory_count} mem)`
  }, [projects, workspace])

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

  async function showRecentsCapture() {
    if (!workspace) return
    if (recentsBusy) return
    setRecentsBusy(true)
    setSelectedMemory(null)
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode: 'search',
      text: 'Loading recent memories…',
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

  async function runDeepSearch(query: string) {
    const q = query.trim()
    if (!q || !workspace) return
    setDeepSearchPrompt({ open: false, query: '' })
    setBusy(true)
    setSelectedMemory(null)
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode: 'recall',
      text: 'Deep searching (recall preview)…',
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
    if (!workspace) return
    const text = draft.trim()
    if (!text) return
    setDraft('')
    setBusy(true)
    setSelectedMemory(null)
    const userMsg: ChatMessage = {
      id: makeID(),
      role: 'user',
      mode,
      text,
      createdAt: Date.now(),
    }
    const pendingID = makeID()
    const pending: ChatMessage = {
      id: pendingID,
      role: 'assistant',
      mode,
      text: mode === 'search' ? 'Searching…' : 'Recalling…',
      createdAt: Date.now(),
      pending: true,
    }
    setMessages((m) => [...m, userMsg, pending])
    try {
      if (mode === 'search') {
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
      } else {
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
          <button
            className={mode === 'search' ? 'navItem navItemOn' : 'navItem'}
            onClick={() => {
              setMode('search')
              setSelectedMemory(null)
            }}
            type="button"
            aria-label="Search mode"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="11" cy="11" r="7" />
              <path d="M21 21l-4.3-4.3" />
            </svg>
            <span className="navLabel">Search</span>
          </button>
          <button
            className={mode === 'recall' ? 'navItem navItemOn' : 'navItem'}
            onClick={() => {
              setMode('recall')
              setSelectedMemory(null)
            }}
            type="button"
            aria-label="Recall preview mode"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M3 12a9 9 0 101.8-5.4" />
              <path d="M3 4v6h6" />
            </svg>
            <span className="navLabel">Recall Preview</span>
          </button>
          <button
            className="navItem"
            onClick={showRecentsCapture}
            type="button"
            aria-label="Recents capture"
            disabled={recentsBusy || !workspace}
            title="Show recently added memories"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="9" />
              <path d="M12 7v5l3 2" />
            </svg>
            <span className="navLabel">Recents capture</span>
          </button>
        </nav>

        <div className="topbarRight">
          <button
            className="iconBtn iconBtnInfo"
            onClick={() => setInfoOpen(true)}
            aria-label="Info"
            title="System Info"
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
            {diagramMemories.length > 0 ? (
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
                title="Quick access to architecture & diagrams"
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
              {messages.map((m) => (
                <Message
                  key={m.id}
                  m={m}
                  theme={theme}
                  selectedId={selectedMemory?.id}
                  onSelectMemory={setSelectedMemory}
                />
              ))}
            </div>

            <div className="composerDock" ref={composerRef}>
              <div className="composer">
                {advancedOpen ? (
                  <div className="composerAdvanced">
                    <div className="composerRow">
                      <div className="composerRowTitle">Mode</div>
                      <div className="modePills modePillsInline">
                        <button
                          className={mode === 'search' ? 'modePill modePillOn' : 'modePill'}
                          onClick={() => {
                            setMode('search')
                            setSelectedMemory(null)
                          }}
                          type="button"
                        >
                          Search
                        </button>
                        <button
                          className={mode === 'recall' ? 'modePill modePillOn' : 'modePill'}
                          onClick={() => {
                            setMode('recall')
                            setSelectedMemory(null)
                          }}
                          type="button"
                        >
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
                            <input
                              className="input"
                              type="number"
                              min={1}
                              max={200}
                              value={topK}
                              onChange={(e) => setTopK(Number(e.target.value))}
                            />
                          </div>
                          <div>
                            <label className="label">Outcome</label>
                            <select
                              className="input"
                              value={outcome}
                              onChange={(e) => setOutcome(e.target.value as OutcomeResult | '')}
                            >
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
                            <input
                              className="input"
                              inputMode="decimal"
                              value={minConfidence}
                              onChange={(e) => setMinConfidence(e.target.value)}
                              placeholder="0.00 – 1.00"
                            />
                          </div>
                          <div>
                            <label className="label">Min decay</label>
                            <input
                              className="input"
                              inputMode="decimal"
                              value={minDecay}
                              onChange={(e) => setMinDecay(e.target.value)}
                              placeholder="0.00 – 1.00"
                            />
                          </div>
                        </div>

                        <label className="label">Entities (comma-separated)</label>
                        <input
                          className="input"
                          value={entities}
                          onChange={(e) => setEntities(e.target.value)}
                          placeholder="orders, kafka, schema"
                        />

                        <div className="row row2">
                          <div>
                            <label className="label">From</label>
                            <input
                              className="input"
                              type="date"
                              value={fromDate}
                              onChange={(e) => setFromDate(e.target.value)}
                            />
                          </div>
                          <div>
                            <label className="label">To</label>
                            <input
                              className="input"
                              type="date"
                              value={toDate}
                              onChange={(e) => setToDate(e.target.value)}
                            />
                          </div>
                        </div>
                      </>
                    ) : (
                      <div className="row row2">
                        <div>
                          <label className="label">Top K</label>
                          <input
                            className="input"
                            type="number"
                            min={1}
                            max={500}
                            value={recallTopK}
                            onChange={(e) => setRecallTopK(Number(e.target.value))}
                          />
                        </div>
                        <div>
                          <label className="label">Budget</label>
                          <input
                            className="input"
                            type="number"
                            min={1}
                            value={budget}
                            onChange={(e) => setBudget(Number(e.target.value))}
                          />
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
                  placeholder={mode === 'search' ? 'How can I help you today?' : 'Describe the task to recall…'}
                  rows={inputFocused || draft.trim().length > 0 ? 3 : 1}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      submit()
                    }
                  }}
                />
                <div className="composerToolbar">
                  <div className="composerToolbarLeft">
                    <button className="btn btnGhost" title="Add attachment">
                      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M12 5v14M5 12h14"/></svg>
                    </button>
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
              <div className="composerFoot">
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
                <button
                  className="btn btnGhost"
                  onClick={() => setSelectedMemory(null)}
                  aria-label="Close details"
                  style={{ padding: '8px 12px' }}
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                    <path d="M18 6L6 18M6 6l12 12" />
                  </svg>
                </button>
              </div>

              <div className="detailDrawerBody">
                <div className="detailPills">
                  <span className="memPill">{selectedMemory.type}</span>
                  <span className="memPill">{selectedMemory.storage_tier}</span>
                  {typeof selectedMemory.score === 'number' ? (
                    <span className="memPill">score {selectedMemory.score.toFixed(3)}</span>
                  ) : null}
                  <span className="memPill">conf {selectedMemory.confidence.toFixed(2)}</span>
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
                  <div className="detailSectionTitle">Metadata</div>
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

      {infoOpen ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setInfoOpen(false)
          }}
          role="presentation"
        >
          <div className="modalPanel" role="dialog" aria-modal="true" aria-label="Info">
            <div className="modalTop">
              <div className="modalTitle">Info</div>
              <button className="btn btnGhost" onClick={() => setInfoOpen(false)}>
                Close
              </button>
            </div>
            <div className="modalBody">
              <div className="muted small">
                Workspace: <span className="mono">{projectLabel}</span>
              </div>
              {statsErr ? <div className="callout calloutBad">{statsErr}</div> : null}
              <pre className="pre">{stats ? JSON.stringify(stats, null, 2) : 'Loading…'}</pre>
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
              <div className="muted">
                Try a deep search (Recall Preview) for:
              </div>
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
              <div className="muted">
                Tap a card to instantly open its full detail view and interactive architecture canvas:
              </div>
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
                        <span className="diagramPreviewID">{m.id.slice(0, 16)}…</span>
                        <span className="memPill">{m.type}</span>
                      </div>
                      <div className="diagramPreviewSnippet">{snippet.trim().slice(0, 140)}…</div>
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
                  <ResultCard
                    key={r.id}
                    m={r}
                    theme={theme}
                    isSelected={r.id === selectedId}
                    onSelect={onSelectMemory}
                  />
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
                  <button
                    className="btn btnGhost"
                    onClick={() => navigator.clipboard.writeText(m.payload!.recall!.context_block)}
                  >
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
                  <ResultCard
                    key={r.id}
                    m={r}
                    theme={theme}
                    isSelected={r.id === selectedId}
                    onSelect={onSelectMemory}
                  />
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
  return (
    <article className={isSelected ? 'memCard memCardOn' : 'memCard'} onClick={() => onSelect(m)}>
      <div className="memHdr">
        <div className="memHdrLeft">
          <span className="memDot" aria-hidden="true" />
          <span className="mono memID">{m.id.slice(0, 12)}…</span>
        </div>
        <div className="memHdrRight">
          <span className="memPill">{m.type}</span>
          <span className="memPill">{m.storage_tier}</span>
          {typeof m.score === 'number' ? <span className="memPill">score {m.score.toFixed(3)}</span> : null}
          <span className="memPill">conf {m.confidence.toFixed(2)}</span>
        </div>
      </div>
      <div className="memBody memBodyCompact">
        <MarkdownView markdown={m.content} clamp={true} theme={theme} />
      </div>
    </article>
  )
}
