import type { DashboardStats, ProjectListItem } from '../lib/api'
import {
  formatBytes,
  formatNumber,
  formatPercent,
  formatTS,
  getOperationTotals,
  toTitle,
  zeroTokenTotals,
} from './dashboardHelpers'
import { BreakdownCard, DiagnosticRow, MetricCard } from './components'

export function DiagnosticsPanel({
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
