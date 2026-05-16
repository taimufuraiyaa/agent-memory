import React, { useEffect, useMemo, useState } from 'react'
import {
  getStats,
  listProjects,
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
  const [messages, setMessages] = useState<ChatMessage[]>([])

  const projectLabel = useMemo(() => {
    const p = projects.find((x) => x.name === workspace)
    if (!p) return workspace || 'workspace'
    return `${p.name} (${p.memory_count} mem)`
  }, [projects, workspace])

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

  async function runDeepSearch(query: string) {
    const q = query.trim()
    if (!q || !workspace) return
    setDeepSearchPrompt({ open: false, query: '' })
    setBusy(true)
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
        <div className="brand">
          <div className="brandMark" aria-hidden="true" />
          <div className="brandText">
            <div className="brandTitle">Agent Memory</div>
            <div className="brandSub">Chat-style human inspection (English-only)</div>
          </div>
        </div>
        <div className="topbarRight">
          <select
            className="headerSelect"
            value={workspace}
            onChange={(e) => setWorkspace(e.target.value)}
            aria-label="Workspace"
          >
            {projects.length === 0 ? <option value="">(no workspaces)</option> : null}
            {projects.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name} ({p.memory_count})
              </option>
            ))}
          </select>
          <button
            className="iconBtn iconBtnInfo"
            onClick={() => setInfoOpen(true)}
            aria-label="Info"
            title="Info"
          >
            i
          </button>
          <a className="topLink" href="/health" target="_blank" rel="noreferrer noopener">
            health
          </a>
        </div>
      </header>

      <div className="chatLayout chatLayoutNoSidebar">
        <main className="chatMain">
          <div className="thread">
            {messages.map((m) => (
              <Message key={m.id} m={m} />
            ))}
          </div>

          <div className="composerDock">
            <div className="composer">
              <div className="composerTop">
                <div className="composerMeta">
                  <span className="composerMode">{mode === 'search' ? 'Search' : 'Recall Preview'}</span>
                  <span className="composerHint">
                    {mode === 'search' ? 'Ask a question to find memories.' : 'Describe a task to preview recall output.'}
                  </span>
                </div>
                <div className="composerActions">
                  <button className="btn" onClick={() => setAdvancedOpen((v) => !v)}>
                    {advancedOpen ? 'Hide Advanced' : 'Advanced'}
                  </button>
                </div>
              </div>

              {advancedOpen ? (
                <div className="composerAdvanced">
                  <div className="composerRow">
                    <div className="composerRowTitle">Mode</div>
                    <div className="modePills modePillsInline">
                      <button
                        className={mode === 'search' ? 'modePill modePillOn' : 'modePill'}
                        onClick={() => setMode('search')}
                        type="button"
                      >
                        Search
                      </button>
                      <button
                        className={mode === 'recall' ? 'modePill modePillOn' : 'modePill'}
                        onClick={() => setMode('recall')}
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

              <div className="composerInputRow">
                <textarea
                  className="composerInput"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder={mode === 'search' ? 'Ask a question in English. Use Search to find relevant memories; use Recall Preview to see the exact context block an agent would receive.' : 'Describe the task to recall…'}
                  rows={1}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      submit()
                    }
                  }}
                />
                <button className="sendBtn" onClick={submit} disabled={!workspace || busy || !draft.trim()}>
                  Send
                </button>
              </div>
              <div className="composerFoot">
                <span className="muted small">
                  Served locally by <span className="mono">agent-memory serve</span>. Markdown is sanitized; Mermaid renders when present.
                </span>
              </div>
            </div>
          </div>
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
    </div>
  )
}

function Message({ m }: { m: ChatMessage }) {
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
          <MarkdownView markdown={m.text} clamp={false} />
          {m.error ? <div className="callout calloutBad">{m.error}</div> : null}

          {m.payload?.results ? (
            <div className="assistantBlock">
              <div className="assistantHdr">
                <div className="assistantTitle">Results</div>
                <div className="muted small">{m.payload.results.length}</div>
              </div>
              <div className="assistantList">
                {m.payload.results.map((r) => (
                  <ResultCard key={r.id} m={r} />
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
                  <ResultCard key={r.id} m={r} />
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

function ResultCard({ m }: { m: MemoryEntry }) {
  const [open, setOpen] = useState(false)
  return (
    <article className="memCard">
      <button className="memHdr" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <div className="memHdrLeft">
          <span className="memDot" aria-hidden="true" />
          <span className="mono memID">{m.id}</span>
        </div>
        <div className="memHdrRight">
          <span className="memPill">{m.type}</span>
          <span className="memPill">{m.storage_tier}</span>
          {typeof m.score === 'number' ? <span className="memPill">score {m.score.toFixed(3)}</span> : null}
          <span className="memPill">conf {m.confidence.toFixed(2)}</span>
        </div>
      </button>
      <div className="memBody">
        <MarkdownView markdown={m.content} clamp={!open} />
        {open ? (
          <>
            <div className="memMetaGrid">
              <div className="memMeta">
                <div className="memMetaLabel">Updated</div>
                <div className="memMetaValue">{formatTS(m.updated_at)}</div>
              </div>
              <div className="memMeta">
                <div className="memMetaLabel">Created</div>
                <div className="memMetaValue">{formatTS(m.created_at)}</div>
              </div>
              <div className="memMeta">
                <div className="memMetaLabel">Entities</div>
                <div className="memMetaValue">{pillList(m.entities ?? [])}</div>
              </div>
              <div className="memMeta">
                <div className="memMetaLabel">Tags</div>
                <div className="memMetaValue">{pillList(m.tags ?? [])}</div>
              </div>
            </div>

            {m.diagram ? (
              <div className="diagramBlock">
                <div className="memMetaLabel">Diagram</div>
                <DiagramViewer diagram={m.diagram} />
              </div>
            ) : null}

            {m.score_breakdown ? (
              <details className="detailsFold">
                <summary className="detailsSum">Score breakdown</summary>
                <pre className="pre">{JSON.stringify(m.score_breakdown, null, 2)}</pre>
              </details>
            ) : null}
          </>
        ) : null}
      </div>
    </article>
  )
}
