import React from 'react'
import type { LLMUsageGroupTotals, TokenMetricGroupTotals } from '../lib/api'
import {
  buildPieGradient,
  chartColor,
  formatLegendIndex,
  formatNumber,
  formatPercent,
  getGroupOperationTotals,
  sumCounts,
  toTitle,
} from './dashboardHelpers'

export function MetricCard({ title, value, detail }: { title: string; value: string; detail: string }) {
  return (
    <article className="metricCard">
      <div className="metricLabel">{title}</div>
      <div className="metricValue">{value}</div>
      <div className="metricDetail">{detail}</div>
    </article>
  )
}

export function BreakdownCard({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
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

export function PieChartBreakdown({ entries, emptyLabel }: { entries: Array<[string, number]>; emptyLabel: string }) {
  const total = entries.reduce((sum, [, value]) => sum + value, 0)
  if (entries.length === 0) return <div className="muted">{emptyLabel}</div>
  return (
    <div className="pieChartBlock">
      <div className="pieChartWrap" aria-hidden="true">
        <div className="pieChartVisual" style={{ background: buildPieGradient(entries) }}>
          <div className="pieChartCenter">
            <span className="pieChartTotal">{formatNumber(total)}</span>
            <span className="pieChartTotalCaption">total</span>
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

export function ComparisonSection({
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

export function DiagnosticRow({ label, value }: { label: string; value: string }) {
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

export function TokenGroupCard({ group, index }: { group: TokenMetricGroupTotals; index: number }) {
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
      <div className="groupSub">
        {formatPercent(totals.baseline_tokens > 0 ? (totals.saved_tokens / totals.baseline_tokens) * 100 : 0)} {savingsLabel} across {formatNumber(totals.records)} {recordsLabel}
      </div>
      <div className="groupStats">
        <DiagnosticRow label="Returned" value={formatNumber(totals.returned_tokens)} />
        <DiagnosticRow label="Baseline" value={formatNumber(totals.baseline_tokens)} />
      </div>
    </article>
  )
}

export function LLMGroupCard({ group, index }: { group: LLMUsageGroupTotals; index: number }) {
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
