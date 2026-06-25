import { useEffect, useRef } from 'react'
import type { DashboardStats, ProjectListItem } from '../lib/api'
import {
  formatBytes,
  formatDuration,
  formatNumber,
  formatPercent,
  formatTS,
  getOperationTotals,
  sortCountEntries,
  sumCounts,
  toTitle,
  zeroTokenTotals,
} from './dashboardHelpers'
import {
  BreakdownCard,
  ComparisonSection,
  DiagnosticRow,
  LLMGroupCard,
  MetricCard,
  PieChartBreakdown,
  TokenGroupCard,
} from './components'

export function OverviewPanel({
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
  const scheduler = stats?.scheduler
  const schedulerWorkspace = scheduler?.workspace
  const schedulerState = scheduler?.enabled
    ? schedulerWorkspace?.run_in_progress
      ? 'running'
      : schedulerWorkspace?.last_error
        ? 'failed'
        : schedulerWorkspace?.last_result || 'idle'
    : 'disabled'
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

        <BreakdownCard title="Scheduler" subtitle={scheduler?.enabled ? `Next tick ${formatTS(scheduler?.next_tick_at) || 'n/a'}` : 'Background lifecycle disabled'}>
          <div className="diagnosticsList">
            <DiagnosticRow label="State" value={schedulerState} />
            <DiagnosticRow label="Last Tick" value={formatTS(scheduler?.last_tick_at) || 'n/a'} />
            <DiagnosticRow label="Last Completed" value={formatTS(schedulerWorkspace?.last_completed_at) || 'n/a'} />
            <DiagnosticRow label="Last Skip" value={schedulerWorkspace?.last_skip_reason || schedulerWorkspace?.current_skip_reason || '-'} />
            <DiagnosticRow label="Hygiene Overdue" value={schedulerWorkspace?.hygiene_overdue ? 'yes' : 'no'} />
            <DiagnosticRow label="Last Duration" value={formatDuration(schedulerWorkspace?.last_duration_ms)} />
            <DiagnosticRow label="Last Impacts" value={schedulerWorkspace?.last_impacts != null ? String(schedulerWorkspace.last_impacts) : '-'} />
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
