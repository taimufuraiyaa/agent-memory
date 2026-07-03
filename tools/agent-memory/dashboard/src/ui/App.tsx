import { useEffect, useMemo, useRef, useState, useCallback } from 'react'
import {
  deleteMemories,
  getStats,
  listBenchmarkRuns,
  listObservations,
  listSessions,
  listProjects,
  listRecentMemories,
  listSchedulerHistory,
  promoteObservations,
  recallPreview,
  searchMemories,
  setMemoryPinned,
  listFeedback,
  type BenchmarkRun,
  type DashboardStats,
  type MemoryEntry,
  type MemoryType,
  type ObservationEntry,
  type ObservationPromotionResult,
  type OutcomeResult,
  type ProjectListItem,
  type RecallPreviewResponse,
  type SchedulerRunHistory,
  type SessionEntry,
  type StorageTier,
  type RetrievalRequestLog,
} from '../lib/api'
import { DiagramViewer } from './DiagramViewer'
import { MarkdownView } from './MarkdownView'
import {
  ALL_PROJECTS_SCOPE,
  SEARCH_DEFAULT_MIN_SEMANTIC_SCORE,
  buildMemoryKey,
  clampUnitScore,
  compareMemoryRecency,
  compareMemoryRelevance,
  buildConsolidatedExportHTML,
  formatNumber,
  formatBytes,
  formatScore,
  formatTS,
  getHealthState,
  getSemanticRelevance,
  getSemanticSimilarity,
  hasDiagram,
  makeID,
  mergeMemoryResults,
  parseUnitScore,
  pillList,
  toTitle,
  type Surface,
  type WikiMode,
  type WikiSearchState,
  type WikiViewMode,
} from './dashboardHelpers'
import { BenchmarkPanel } from './BenchmarkPanel'
import { DiagnosticsPanel } from './DiagnosticsPanel'
import { LifecyclePanel } from './LifecyclePanel'
import { OverviewPanel } from './OverviewPanel'
import { SessionsPanel } from './SessionsPanel'
import { WikiPanel } from './WikiPanel'
import { FeedbackPanel } from './FeedbackPanel'

export function App() {
  const [surface, setSurface] = useState<Surface>('overview')
  const [viewingJSON, setViewingJSON] = useState<any | null>(null)

  const [projects, setProjects] = useState<ProjectListItem[]>([])
  const [workspace, setWorkspace] = useState<string>('')
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [statsErr, setStatsErr] = useState<string>('')
  const [benchmarkRuns, setBenchmarkRuns] = useState<BenchmarkRun[]>([])
  const [benchmarkBusy, setBenchmarkBusy] = useState<boolean>(false)
  const [benchmarkErr, setBenchmarkErr] = useState<string>('')
  const [schedulerHistory, setSchedulerHistory] = useState<SchedulerRunHistory[]>([])
  const [schedulerBusy, setSchedulerBusy] = useState<boolean>(false)
  const [schedulerErr, setSchedulerErr] = useState<string>('')
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
  const [feedbackLogs, setFeedbackLogs] = useState<RetrievalRequestLog[]>([])
  const [feedbackBusy, setFeedbackBusy] = useState<boolean>(false)
  const [feedbackErr, setFeedbackErr] = useState<string>('')

  const [topK, setTopK] = useState<number>(10)
  const [explain, setExplain] = useState<boolean>(true)

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
  const [theme, setTheme] = useState<'light' | 'dark'>('dark')
  const [selectedMemory, setSelectedMemory] = useState<MemoryEntry | null>(null)
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
  }, [rawStatsOpen, selectedMemory, wikiConsolidatedOpen, wikiDiagramMemory])

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
    if (!workspace || surface !== 'lifecycle') return
    setSchedulerBusy(true)
    listSchedulerHistory({ workspace, limit: 100 })
      .then((res) => {
        if (cancelled) return
        setSchedulerHistory(res.history || [])
        setSchedulerErr('')
      })
      .catch((err) => {
        if (cancelled) return
        setSchedulerErr((err as Error).message)
      })
      .finally(() => {
        if (cancelled) return
        setSchedulerBusy(false)
      })
    return () => {
      cancelled = true
    }
  }, [workspace, surface])

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

  const refreshFeedbackAndStats = useCallback(() => {
    if (!workspace) return
    setFeedbackBusy(true)
    setFeedbackErr('')
    listFeedback({ workspace })
      .then((data) => {
        setFeedbackLogs(data)
      })
      .catch((e) => {
        setFeedbackLogs([])
        setFeedbackErr(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        setFeedbackBusy(false)
      })

    getStats(workspace)
      .then((s) => {
        setStats(s)
      })
      .catch((e) => {
        setStats(null)
        setStatsErr(e instanceof Error ? e.message : String(e))
      })
  }, [workspace])

  useEffect(() => {
    if (workspace && surface === 'feedback') {
      refreshFeedbackAndStats()
    }
  }, [workspace, surface, refreshFeedbackAndStats])

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
    setWikiMode('search')
    setSurface('wiki')
    setSelectedMemory(null)
  }

  function openRecall() {
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
    }
    setSurface('wiki')
    setSelectedMemory(null)
  }

  function triggerWikiSearch(query: string) {
    setWikiMode('search')
    setWikiQuery(query)
    setSurface('wiki')
    setSelectedMemory(null)
    void runWikiSearch(query)
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

  function focusExperimentComparisons() {
    setSurface('overview')
    setOverviewExperimentFocusKey((current) => current + 1)
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
    <div className={surface === 'wiki' ? 'shell chatShell shellWikiMode' : surface === 'feedback' ? 'shell chatShell shellFeedbackMode' : 'shell chatShell'}>
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
              const val = e.target.value
              setWorkspace(val)
              setWikiScope(val)
              setSelectedMemory(null)
            }}
            aria-label="Switch workspace"
          >
            {projects.length === 0 ? <option value="">(no workspaces)</option> : null}
            {projects.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name} ({p.memory_count} mem, {formatBytes(p.size_bytes)})
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
          <button className={surface === 'lifecycle' ? 'navItem navItemOn' : 'navItem'} onClick={() => setSurface('lifecycle')} type="button" aria-label="Lifecycle">
            <span className="navKey">[05]</span>
            <span className="navLabel">Lifecycle</span>
          </button>
          <button className={surface === 'wiki' ? 'navItem navItemOn' : 'navItem'} onClick={() => openWiki()} type="button" aria-label="Wiki">
            <span className="navKey">[06]</span>
            <span className="navLabel">Wiki</span>
          </button>
          <button className={surface === 'feedback' ? 'navItem navItemOn' : 'navItem'} onClick={() => { setSurface('feedback'); setSelectedMemory(null); }} type="button" aria-label="Feedback">
            <span className="navKey">[07]</span>
            <span className="navLabel">Feedback</span>
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

      <div className="chatLayout">
        <main className="chatMain">
          <div className="chatFeed">
            <div className="thread" ref={threadRef}>
              {surface === 'overview' ? (
                <OverviewPanel
                  workspace={workspace}
                  project={selectedProject}
                  stats={stats}
                  statsErr={statsErr}
                  healthState={healthState}
                  diagramCount={stats?.diagram_count || 0}
                  experimentFocusKey={overviewExperimentFocusKey}
                  onCompareRuns={focusExperimentComparisons}
                  onInspectFailures={() => triggerWikiSearch('recent failures errors regressions')}
                  onReviewLastSession={openSessions}
                  onRunDiagramAction={() => triggerWikiSearch('architecture diagram mermaid flow')}
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

              {surface === 'lifecycle' ? (
                <LifecyclePanel
                  workspace={workspace}
                  scheduler={stats?.scheduler}
                  history={schedulerHistory}
                  busy={schedulerBusy}
                  error={schedulerErr}
                />
              ) : null}

              {surface === 'feedback' ? (
                <FeedbackPanel
                  workspace={workspace}
                  feedback={feedbackLogs}
                  busy={feedbackBusy}
                  error={feedbackErr}
                  onFeedbackUpdated={refreshFeedbackAndStats}
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
                    setWikiSearch((current) => {
                      if (current.mode === nextMode) return current
                      if (current.mode === 'recents' && nextMode === 'search') {
                        return { ...current, mode: nextMode }
                      }
                      return { ...current, mode: nextMode, searched: false }
                    })
                    if (nextMode !== 'recall') setWikiRecall(null)
                    if (nextMode === 'recents') void showRecentsCapture()
                  }}
                  onScopeChange={setWikiScope}
                  onViewModeChange={setWikiViewMode}
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
                  {selectedMemory.superseded_by ? (
                    <span className="memPill relevancePill relevancePillLow" style={{ background: '#3b1c1c', border: '1px solid #7d2a2a', color: '#ff8585' }}>Superseded</span>
                  ) : null}
                  {selectedMemory.relations?.some(r => r.type === 'supersedes') ? (
                    <span className="memPill relevancePill relevancePillHigh" style={{ background: '#1c3b24', border: '1px solid #2a7d43', color: '#85ff9d' }}>Correction</span>
                  ) : null}
                </div>

                {(() => {
                  const parsedJSON = tryParseJSON(selectedMemory.content)
                  return (
                    <div className="detailSection">
                      <div className="detailSectionTitle">Content</div>
                      <div
                        className={`detailContentCard ${parsedJSON ? 'clickableJSONCard' : ''}`}
                        onClick={() => {
                          if (parsedJSON) setViewingJSON({ id: selectedMemory.id, data: parsedJSON })
                        }}
                        style={parsedJSON ? { cursor: 'pointer', border: '1px dashed var(--accent-primary)', position: 'relative' } : undefined}
                        title={parsedJSON ? "Click to view beautiful JSON" : undefined}
                      >
                        {parsedJSON ? (
                          <div style={{ position: 'absolute', right: '12px', top: '8px', fontSize: '10px', color: 'var(--accent-primary)', fontWeight: 'bold', background: 'var(--bg-input)', padding: '2px 6px', borderRadius: '4px' }}>
                            JSON 🔍
                          </div>
                        ) : null}
                        <MarkdownView markdown={selectedMemory.content} clamp={false} theme={theme} />
                        {selectedMemory.diagram ? (
                          <div className="diagramBlock">
                            <DiagramViewer diagram={selectedMemory.diagram} theme={theme} />
                          </div>
                        ) : null}
                      </div>
                    </div>
                  )
                })()}

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
                    {selectedMemory.superseded_by ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Superseded By</div>
                        <div className="memMetaValue mono" style={{ color: '#ff8585', wordBreak: 'break-all' }}>{selectedMemory.superseded_by}</div>
                      </div>
                    ) : null}
                    {selectedMemory.relations?.some(r => r.type === 'supersedes') ? (
                      <div className="memMeta">
                        <div className="memMetaLabel">Supersedes</div>
                        <div className="memMetaValue mono" style={{ color: '#85ff9d', wordBreak: 'break-all' }}>
                          {selectedMemory.relations.find(r => r.type === 'supersedes')?.target_id}
                        </div>
                      </div>
                    ) : null}
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

      {viewingJSON ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setViewingJSON(null)
          }}
          role="presentation"
        >
          <div className="modalPanel" role="dialog" aria-modal="true" aria-label="Beautified JSON content" style={{ maxWidth: '800px', width: '90%' }}>
            <div className="modalTop">
              <div className="modalTitle">Details</div>
              <button className="btn btnGhost" onClick={() => setViewingJSON(null)}>
                Close
              </button>
            </div>
            <div className="modalBody" style={{ maxHeight: '70vh', overflowY: 'auto' }}>
              <div className="muted small" style={{ marginBottom: '8px' }}>
                Memory ID: <span className="mono">{viewingJSON.id}</span>
              </div>
              <pre className="pre" style={{ margin: 0, padding: '12px', background: 'var(--bg-input)', borderRadius: '6px', fontSize: '12px', lineHeight: '1.5', whiteSpace: 'pre-wrap', overflowWrap: 'break-word' }}>
                <code>{JSON.stringify(viewingJSON.data, null, 2)}</code>
              </pre>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}

function tryParseJSON(str: string): any {
  try {
    return JSON.parse(str)
  } catch (e) {
    return null
  }
}
