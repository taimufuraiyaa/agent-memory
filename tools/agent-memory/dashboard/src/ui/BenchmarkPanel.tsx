import { useEffect, useState } from 'react'
import type { BenchmarkClusterSummary, BenchmarkRun } from '../lib/api'
import {
  formatDuration,
  formatLegendIndex,
  formatMoney,
  formatNumber,
  formatTS,
  formatUnitPercent,
} from './dashboardHelpers'
import { BreakdownCard, DiagnosticRow, MetricCard } from './components'
import { ListPagination, paginateRecords } from './workspace/ListPagination'

function benchmarkEconomicSummary(run?: BenchmarkRun): Record<string, unknown> | null {
  const manifest = run?.run_manifest
  if (!manifest || typeof manifest !== 'object') return null
  const value = (manifest as Record<string, unknown>).economic_summary
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : null
}

function benchmarkEconomicNumber(run: BenchmarkRun | undefined, key: string, fallback = 0): number {
  const summary = benchmarkEconomicSummary(run)
  const value = summary?.[key]
  return typeof value === 'number' ? value : fallback
}

function BenchmarkClusterCard({ cluster, index }: { cluster: BenchmarkClusterSummary; index: number }) {
  return (
    <article className="groupCard benchmarkClusterCard benchmarkClusterCardReference">
      <div className="groupCardTop">
        <div className="groupHeading">
          <span className="groupIndex">{formatLegendIndex(index)}</span>
          <span className="groupLead">.-</span>
          <div className="groupTitle">{cluster.cluster_title}</div>
        </div>
        <span className="groupBadge groupBadgeOn">{formatNumber(cluster.cases)} cases</span>
      </div>
      <div className="groupMetric">{cluster.continuation_score.toFixed(3)}</div>
      <div className="groupSub">{(cluster.continuation_verdict || cluster.verdict).toLowerCase()} primary score</div>
      <div className="diagnosticsList benchmarkClusterMetrics">
        <DiagnosticRow label="Success" value={formatUnitPercent(cluster.task_success_delta)} />
        <DiagnosticRow label="Facts" value={formatUnitPercent(cluster.answer_fact_coverage_delta)} />
        <DiagnosticRow label="Runtime" value={formatDuration(Math.round(cluster.runtime_delta_ms))} />
      </div>
    </article>
  )
}

function BenchmarkHistoryRow({ run, index }: { run: BenchmarkRun; index: number }) {
  return (
    <div className="benchmarkHistoryRow">
      <div className="benchmarkHistoryMain">
        <span className="benchmarkHistoryIndex">{formatLegendIndex(index)}</span>
        <span className="benchmarkHistoryLead">--</span>
        <span className="benchmarkHistoryRun">{run.run_id}</span>
        <span className="benchmarkHistoryMeta">{formatTS(run.created_at) || 'n/a'}</span>
        <span className="benchmarkHistoryMeta">{formatNumber(run.case_count)} cases</span>
      </div>
      <div className="benchmarkHistorySide">
        <span className="benchmarkHistoryScore">{run.continuation_score.toFixed(3)}</span>
        <span className={`statusBadge ${run.continuation_score >= 0.2 ? 'statusBadgeGood' : run.continuation_score > 0 ? 'statusBadgeWarn' : 'statusBadgeBad'}`}>
          {(run.continuation_verdict || run.verdict).toLowerCase()}
        </span>
      </div>
    </div>
  )
}

export function BenchmarkPanel({
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
  const clusterCount = latest?.clusters.length ?? 0
  const primaryVerdict = latest?.continuation_verdict || latest?.verdict || ''
  const locatorSuccessDelta = benchmarkEconomicNumber(latest, 'locator_success_delta')
  const locatorSuccessRate = benchmarkEconomicNumber(latest, 'locator_success_rate')
  const offLocatorSuccessRate = benchmarkEconomicNumber(latest, 'off_locator_success_rate')
  const verificationEffortDelta = benchmarkEconomicNumber(latest, 'verification_effort_delta')
  const avgOnVerificationEffort = benchmarkEconomicNumber(latest, 'avg_on_verification_effort')
  const avgOffVerificationEffort = benchmarkEconomicNumber(latest, 'avg_off_verification_effort')
  const avgOnRediscoveryEffort = benchmarkEconomicNumber(latest, 'avg_on_rediscovery_effort')
  const avgOffRediscoveryEffort = benchmarkEconomicNumber(latest, 'avg_off_rediscovery_effort')
  const operationalCostSaved = benchmarkEconomicNumber(latest, 'operational_cost_saved')
  const operationalCostSavedPct = benchmarkEconomicNumber(latest, 'operational_cost_saved_pct')
  const operationalCostWithMemory = benchmarkEconomicNumber(latest, 'operational_cost_with_memory')
  const operationalCostWithoutMemory = benchmarkEconomicNumber(latest, 'operational_cost_without_memory')
  const amortizedAcquisitionCost = benchmarkEconomicNumber(latest, 'amortized_acquisition_cost')
  const memoryROI = benchmarkEconomicNumber(latest, 'memory_roi')

  const [metricExplanationOpen, setMetricExplanationOpen] = useState(false)
  const [runPage, setRunPage] = useState(1)
  const pagedRuns = paginateRecords(runs, runPage)

  useEffect(() => setRunPage(1), [workspace])

  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMetricExplanationOpen(false)
    }
    if (metricExplanationOpen) window.addEventListener('keydown', handleEscape)
    return () => window.removeEventListener('keydown', handleEscape)
  }, [metricExplanationOpen])

  return (
    <section className="surfaceStack">
      <div className="diagnosticsHero">
        <div>
          <div className="overviewEyebrow">Benchmark</div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <h2 className="sectionTitle">{workspace || 'Workspace'} Quality Benchmark</h2>
            <button className="btnInfoCircle" onClick={() => setMetricExplanationOpen(true)} title="How metrics are calculated">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M12 16v-4" />
                <path d="M12 8h.01" />
              </svg>
            </button>
          </div>
          <p className="sectionText">Measure the latest ON/OFF delta first, then use diagnostic signals to understand where memory helps, where it hurts, and what should improve.</p>
        </div>
        {latest ? (
          <div className="diagnosticsHeroSide">
            <span className="statusBadge statusBadgeGood">{primaryVerdict}</span>
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
          <section className="comparisonSection">
            <div className="comparisonHeader">
              <div>
                <div className="breakdownTitle">Benefit</div>
                <div className="breakdownSubtitle">Primary ON/OFF deltas from the latest persisted run</div>
              </div>
            </div>
            <div className="benchmarkStatsGrid">
              <MetricCard title="Verdict" value={primaryVerdict.toLowerCase()} detail={`Latest run: ${formatTS(latest.created_at) || 'n/a'}`} />
              <MetricCard title="Task Success" value={formatUnitPercent(latest.task_success_delta)} detail={`${formatUnitPercent(latest.task_success_rate)} ON vs ${formatUnitPercent(latest.off_task_success_rate)} OFF`} />
              <MetricCard title="Fact Coverage" value={formatUnitPercent(latest.answer_fact_coverage_delta)} detail={`${formatUnitPercent(latest.answer_fact_coverage)} ON vs ${formatUnitPercent(latest.off_answer_fact_coverage)} OFF`} />
              <MetricCard title="Completeness" value={formatUnitPercent(latest.answer_completeness_delta)} detail={`${formatUnitPercent(latest.answer_completeness)} ON vs ${formatUnitPercent(latest.off_answer_completeness)} OFF`} />
              <MetricCard title="Locator Success" value={formatUnitPercent(locatorSuccessDelta)} detail={`${formatUnitPercent(locatorSuccessRate)} ON vs ${formatUnitPercent(offLocatorSuccessRate)} OFF`} />
              <MetricCard title="Verification Effort" value={verificationEffortDelta.toFixed(2)} detail={`${avgOnVerificationEffort.toFixed(2)} ON vs ${avgOffVerificationEffort.toFixed(2)} OFF`} />
              <MetricCard title="Operational Cost" value={formatMoney(operationalCostSaved)} detail={`${formatUnitPercent(operationalCostSavedPct)} vs OFF estimate`} />
              <MetricCard title="Primary Score" value={latest.continuation_score.toFixed(3)} detail="Continuation benefit score" />
            </div>
          </section>

          <div className="benchmarkColumns">
            <BreakdownCard title="Benefit Details" subtitle="Raw ON/OFF values for the primary continuation metrics">
              <div className="benchmarkPanelMeta">
                <span className="overviewMetaItem">run {latest.run_id}</span>
                <span className="overviewMetaItem">cases {formatNumber(latest.case_count)}</span>
                <span className="overviewMetaItem">top_k {formatNumber(latest.top_k)}</span>
                <span className="overviewMetaItem">budget {formatNumber(latest.budget)}</span>
                <span className="overviewMetaItem">clusters {formatNumber(clusterCount)}</span>
              </div>
              <div className="diagnosticsList">
                <DiagnosticRow label="Task Success ON" value={formatUnitPercent(latest.task_success_rate)} />
                <DiagnosticRow label="Task Success OFF" value={formatUnitPercent(latest.off_task_success_rate)} />
                <DiagnosticRow label="Fact Coverage ON" value={formatUnitPercent(latest.answer_fact_coverage)} />
                <DiagnosticRow label="Fact Coverage OFF" value={formatUnitPercent(latest.off_answer_fact_coverage)} />
                <DiagnosticRow label="Completeness ON" value={formatUnitPercent(latest.answer_completeness)} />
                <DiagnosticRow label="Completeness OFF" value={formatUnitPercent(latest.off_answer_completeness)} />
                <DiagnosticRow label="Locator Success ON" value={formatUnitPercent(locatorSuccessRate)} />
                <DiagnosticRow label="Locator Success OFF" value={formatUnitPercent(offLocatorSuccessRate)} />
                <DiagnosticRow label="Avg ON Verification Effort" value={avgOnVerificationEffort.toFixed(2)} />
                <DiagnosticRow label="Avg OFF Verification Effort" value={avgOffVerificationEffort.toFixed(2)} />
                <DiagnosticRow label="Avg ON Rediscovery Effort" value={avgOnRediscoveryEffort.toFixed(2)} />
                <DiagnosticRow label="Avg OFF Rediscovery Effort" value={avgOffRediscoveryEffort.toFixed(2)} />
                <DiagnosticRow label="Avg ON Runtime" value={formatDuration(Math.round(latest.avg_on_runtime_ms))} />
                <DiagnosticRow label="Avg OFF Runtime" value={formatDuration(Math.round(latest.avg_off_runtime_ms))} />
                <DiagnosticRow label="Estimated OFF Operational Cost" value={formatMoney(operationalCostWithoutMemory)} />
                <DiagnosticRow label="Estimated ON Operational Cost" value={formatMoney(operationalCostWithMemory)} />
                <DiagnosticRow label="Amortized Acquisition Cost" value={formatMoney(amortizedAcquisitionCost)} />
                <DiagnosticRow label="Memory ROI" value={formatMoney(memoryROI)} />
              </div>
            </BreakdownCard>

            <BreakdownCard title="Improvement Signals" subtitle="Compact secondary diagnostics and retrieval-context drift">
              <div className="diagnosticsList">
                <DiagnosticRow label="Precision@K" value={latest.precision.toFixed(3)} />
                <DiagnosticRow label="Gold Recall" value={latest.gold_recall.toFixed(3)} />
                <DiagnosticRow label="NDCG@K" value={latest.ndcg.toFixed(3)} />
                <DiagnosticRow label="Keyword Coverage" value={formatUnitPercent(latest.keyword_coverage)} />
                <DiagnosticRow label="ON Retrieval Context Tokens" value={formatNumber(latest.returned_tokens)} />
                <DiagnosticRow label="OFF Retrieval Context Tokens" value={formatNumber(latest.off_returned_tokens)} />
                <DiagnosticRow label="Retrieval Context Delta" value={formatNumber(latest.saved_tokens)} />
                <DiagnosticRow label="Retrieval Context Cost Delta" value={formatMoney(latest.cost_saved)} />
              </div>
            </BreakdownCard>
          </div>

          <section className="comparisonSection">
            <div className="comparisonHeader">
              <div>
                <div className="breakdownTitle">Per-Cluster Breakdown</div>
                <div className="breakdownSubtitle">Diagnostic rollups by topic cluster ({formatNumber(clusterCount)} clusters)</div>
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
                <div className="breakdownSubtitle">Secondary combined-score trend over time ({formatNumber(runs.length)} runs)</div>
              </div>
            </div>
            <div className="benchmarkHistoryList">
              {pagedRuns.items.map((run, index) => (
                <BenchmarkHistoryRow key={run.run_id} run={run} index={pagedRuns.start + index} />
              ))}
            </div>
            <ListPagination page={pagedRuns.page} total={runs.length} onChange={setRunPage} label="Benchmark runs" />
          </section>
        </>
      ) : null}

      {metricExplanationOpen ? (
        <div
          className="modalBackdrop"
          onMouseDown={(e) => { if (e.target === e.currentTarget) setMetricExplanationOpen(false) }}
          role="presentation"
        >
          <div className="modalPanel metricExplanationModal" role="dialog" aria-modal="true" aria-label="Metric Calculations Explanation">
            <div className="modalTop">
              <div className="modalTitle">Metric Calculations Guide</div>
              <button className="btn btnGhost" onClick={() => setMetricExplanationOpen(false)}>Close</button>
            </div>
            <div className="modalBody">
              <div className="metricExplanationGrid">
                <div className="metricExplanationCategory">
                  <div className="metricExplanationCategoryTitle">Economic Model (Savings &amp; ROI)</div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Memory ROI (Return on Investment)</div>
                    <div className="metricExplanationFormula">Memory ROI = Estimated OFF Operational Cost - Estimated ON Operational Cost - Amortized Acquisition Cost</div>
                    <div className="metricExplanationDesc">The net financial savings/value gained by enabling agent memory. Weighs the manual labor savings (from avoided searches, rediscovery, and validation steps) against operational token costs and the amortized cost to acquire/write memories.</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Estimated Operational Cost (ON vs. OFF)</div>
                    <div className="metricExplanationFormula">Operational Cost = token_cost(Returned Tokens + Operational Effort × 200 tokens)</div>
                    <div className="metricExplanationDesc">Combines the actual execution token cost with estimated human labor effort. The labor effort (total steps for lookup, verification, and rediscovery) is converted to a token proxy at a standard rate of 200 tokens per effort unit.</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Amortized Acquisition Cost</div>
                    <div className="metricExplanationDesc">The token-equivalent cost of initial seeding / fixture runs that initially acquired/wrote the memories, amortized (divided) by the number of times those memories are reused.</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Retrieval Context Cost Delta (Context Bloat Penalty)</div>
                    <div className="metricExplanationFormula">Retrieval Cost Delta = token_cost(OFF Tokens) - token_cost(ON Tokens)</div>
                    <div className="metricExplanationDesc">The raw token cost difference between the disabled baseline run and the memory-enabled run. A negative delta represents the "Context Bloat Penalty" from retrieved memories injected into prompts. <i>Note: This penalty is already mathematically accounted for inside the Estimated ON Operational Cost.</i></div>
                  </div>
                </div>
                <div className="metricExplanationCategory">
                  <div className="metricExplanationCategoryTitle">Primary Continuation Signals</div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Task Success Delta</div>
                    <div className="metricExplanationDesc">The change in task success rate (ON % - OFF %). A task is marked successful if the answer achieves high fact coverage (≥ 75%) or full completeness (100%).</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Fact Coverage Delta</div>
                    <div className="metricExplanationDesc">The change in the proportion of expected gold-standard facts successfully identified/addressed in the generated answer (ON % - OFF %).</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Completeness Delta</div>
                    <div className="metricExplanationDesc">The change in the proportion of logical groups of expected facts that were fully covered in the answer (ON % - OFF %).</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Locator Success Delta</div>
                    <div className="metricExplanationDesc">The change in source code files or tool commands correctly referenced in the answers (ON % - OFF %).</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Verification &amp; Rediscovery Effort</div>
                    <div className="metricExplanationDesc"><b>Verification Effort:</b> Remaining incomplete logical groups of facts (representing the manual verification checks needed).<br /><b>Rediscovery Effort:</b> Missing locator targets plus missing gold memory references (representing the manual code search/rediscovery steps needed).</div>
                  </div>
                </div>
                <div className="metricExplanationCategory">
                  <div className="metricExplanationCategoryTitle">Secondary Retrieval Diagnostics</div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Precision@K &amp; Gold Recall</div>
                    <div className="metricExplanationDesc"><b>Precision@K:</b> Proportion of retrieved memories that are relevant.<br /><b>Gold Recall:</b> Proportion of target gold memories retrieved.</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">NDCG@K (Normalized Discounted Cumulative Gain)</div>
                    <div className="metricExplanationDesc">Measures ranking quality, penalizing relevant memories if they are retrieved at lower ranks in the list.</div>
                  </div>
                  <div className="metricExplanationItem">
                    <div className="metricExplanationName">Keyword Coverage</div>
                    <div className="metricExplanationDesc">The proportion of required keywords successfully present in the retrieved memory text context.</div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  )
}
