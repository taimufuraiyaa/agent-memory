import { useEffect, useRef, useState } from 'react'
import type { MemoryEntry, MemoryType, OutcomeResult, ProjectListItem, RecallPreviewResponse, StorageTier } from '../lib/api'
import { DiagramViewer } from './DiagramViewer'
import { MarkdownView } from './MarkdownView'
import {
  ALL_PROJECTS_SCOPE,
  SEARCH_DEFAULT_MIN_SEMANTIC_SCORE,
  allTiers,
  allTypes,
  buildMemoryKey,
  formatNumber,
  formatScore,
  getSemanticRelevance,
  getSemanticSimilarity,
  hasDiagram,
  toTitle,
  wikiSuggestionPresets,
  type WikiMode,
  type WikiViewMode,
} from './dashboardHelpers'

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
          onClick={(e) => { e.stopPropagation(); onToggleSelection(memory) }}
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
          onClick={(e) => { e.stopPropagation(); onTogglePin(memory) }}
        >
          [{pinLabel}]
        </button>
        {memory.diagram ? (
          <button
            className="btn btnGhost wikiDiagramButton"
            type="button"
            onClick={(e) => { e.stopPropagation(); onOpenDiagram(memory) }}
          >
            [diagram]
          </button>
        ) : null}
      </div>
      <div className="wikiFragmentBody">
        <MarkdownView markdown={memory.content} clamp={false} theme={theme} />
      </div>
      {memory.diagram ? (
        <div className="diagramBlock wikiFragmentDiagram" onClick={(e) => { e.stopPropagation() }}>
          <DiagramViewer diagram={memory.diagram} theme={theme} />
        </div>
      ) : null}
    </article>
  )
}

export function WikiPanel({
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
    if (weakResults.length === 0) { setWeakTailOpen(false); return }
    if (results.length === 0) setWeakTailOpen(true)
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
            <button className={mode === 'search' ? 'memPill memPillAccent wikiModePill' : 'memPill wikiModePill'} type="button" onClick={() => onModeChange('search')}>search</button>
            <button className={mode === 'recall' ? 'memPill memPillAccent wikiModePill' : 'memPill wikiModePill'} type="button" onClick={() => onModeChange('recall')}>recall</button>
            <button className={mode === 'recents' ? 'memPill memPillAccent wikiModePill' : 'memPill wikiModePill'} type="button" onClick={() => onModeChange('recents')}>recents</button>
          </div>
          <div className="wikiUtilityActions">
            <button className="btn btnGhost" type="button" onClick={onClearView}>[clear]</button>
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
                <button className="wikiSuggestion" type="button" onClick={onSubmit}>[load recents]</button>
              </div>
            ) : (
              <div className="wikiSuggestionRow">
                {wikiSuggestionPresets.map((item) => (
                  <button key={item.label} className="wikiSuggestion" type="button" onClick={() => { onModeChange('search'); onQueryChange(item.query); onSuggestion(item.query) }}>
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
                    <summary className="wikiSelectionSummary"><span className="memPill">consolidate v</span></summary>
                    <div className="wikiSelectionActions">
                      <button className="btn btnGhost" type="button" onClick={onOpenConsolidated}>Open</button>
                      <button className="btn btnGhost" type="button" onClick={onDownloadSelection}>Download</button>
                      <button className="btn btnGhost" type="button" onClick={onPrintSelection}>Print</button>
                      <button className="btn btnGhost wikiDeleteAction" type="button" onClick={onDeleteSelection} disabled={deleteBusy}>{deleteBusy ? 'Deleting...' : 'Delete'}</button>
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
                <div className="wikiLoadingTrack" aria-hidden="true"><span className="wikiLoadingTrackFill" /></div>
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
                    <WikiMemoryFragment key={buildMemoryKey(memory)} memory={memory} theme={theme} raw={viewMode === 'raw'} selected={selectedIds.has(buildMemoryKey(memory))} pinned={isPinned(memory)} pinBusy={isPinBusy(memory)} onToggleSelection={onToggleSelection} onOpenMemory={onOpenMemory} onOpenDiagram={onOpenDiagram} onTogglePin={onTogglePin} />
                  ))}
                </div>
              </section>
            ) : null}

            {results.length > 0 ? (
              <div className={viewMode === 'raw' ? 'wikiArticleList wikiArticleListRaw' : 'wikiArticleList'}>
                {results.map((memory) => (
                  <WikiMemoryFragment key={buildMemoryKey(memory)} memory={memory} theme={theme} raw={viewMode === 'raw'} selected={selectedIds.has(buildMemoryKey(memory))} pinned={isPinned(memory)} pinBusy={isPinBusy(memory)} onToggleSelection={onToggleSelection} onOpenMemory={onOpenMemory} onOpenDiagram={onOpenDiagram} onTogglePin={onTogglePin} />
                ))}
              </div>
            ) : null}

            {weakResults.length > 0 ? (
              <section className="wikiWeakTail">
                <button className="wikiWeakTailToggle" type="button" onClick={() => setWeakTailOpen((c) => !c)} aria-expanded={weakTailOpen}>
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
                      <WikiMemoryFragment key={buildMemoryKey(memory)} memory={memory} theme={theme} raw={viewMode === 'raw'} weak selected={selectedIds.has(buildMemoryKey(memory))} pinned={isPinned(memory)} pinBusy={isPinBusy(memory)} onToggleSelection={onToggleSelection} onOpenMemory={onOpenMemory} onOpenDiagram={onOpenDiagram} onTogglePin={onTogglePin} />
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
              onFocus={() => {
                if (isRecentsMode) {
                  onModeChange('search')
                }
              }}
              rows={dockExpanded || query.trim().length > 0 ? 3 : 1}
              aria-label="Wiki query"
              onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); onSubmit() } }}
            />
          </div>
          <div className="wikiDockRow wikiDockRowActions">
            <div className="wikiDockControlGroup">
              <select className="input wikiInlineSelect" value={scope} onChange={(e) => onScopeChange(e.target.value)} aria-label="Wiki project scope">
                <option value={ALL_PROJECTS_SCOPE}>all projects</option>
                {projects.map((project) => (<option key={project.name} value={project.name}>{project.name}</option>))}
              </select>
              <select className="input wikiInlineSelect" value={viewMode} onChange={(e) => onViewModeChange(e.target.value as WikiViewMode)} aria-label="Wiki result mode">
                <option value="article">wiki article</option>
                <option value="raw">raw</option>
              </select>
            </div>
            <div className="wikiDockButtonGroup">
              <button className="btn btnGhost" type="button" onClick={onToggleOptions}>{optionsOpen ? 'options -' : 'options v'}</button>
              <button className="btn btnGhost" type="button" onClick={onClearView}>clear</button>
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
                    <div className="emptyStateCard">Recall keeps a single-project scope for now and turns the included memories into one stitched article with a context block.</div>
                  </>
                ) : isRecentsMode ? (
                  <>
                    <div>
                      <label className="label">Recent limit</label>
                      <input className="input" type="number" min={1} max={100} value={topK} onChange={(e) => onSetTopK(Number(e.target.value))} />
                    </div>
                    <div className="emptyStateCard">Recents loads the latest captured memories from the selected scope and sorts them by freshest timestamp first.</div>
                  </>
                ) : (
                  <>
                    <div className="semanticFilterCard">
                      <div className="semanticFilterHeader">
                        <div>
                          <label className="label">Semantic score</label>
                          <div className="semanticFilterHint">Primary relevance control reused from dashboard search.</div>
                        </div>
                        <button className="btn btnGhost semanticPresetReset" type="button" onClick={() => onSetMinSemantic(SEARCH_DEFAULT_MIN_SEMANTIC_SCORE)}>reset</button>
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
                <button className="btn btnGhost" type="button" onClick={onCollapseOptions}>simple mode</button>
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
