import React from 'react'
import type {
  CountMap,
  DashboardStats,
  MemoryEntry,
  MemoryType,
  StorageTier,
  TokenMetricGroupTotals,
  TokenMetricOperationTotals,
  TokenMetricTotals,
} from '../lib/api'
import { renderDiagramMarkupForExport } from './DiagramViewer'

export type Surface = 'overview' | 'sessions' | 'diagnostics' | 'benchmark' | 'wiki' | 'lifecycle'
export type WikiViewMode = 'article' | 'raw'
export type WikiMode = 'search' | 'recall' | 'recents'

export type WikiSearchState = {
  mode: WikiMode
  query: string
  searched: boolean
  results: MemoryEntry[]
  weakResults: MemoryEntry[]
}

export const allTypes: Array<{ key: MemoryType; label: string }> = [
  { key: 'semantic', label: 'semantic' },
  { key: 'procedural', label: 'procedural' },
  { key: 'outcome', label: 'outcome' },
  { key: 'episodic', label: 'episodic' },
]

export const allTiers: Array<{ key: StorageTier; label: string }> = [
  { key: 'vector', label: 'vector' },
  { key: 'markdown', label: 'markdown' },
  { key: 'vector+graph', label: 'vector+graph' },
  { key: 'document', label: 'document' },
]

export function formatTS(s?: string): string {
  if (!s) return ''
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleString()
}

export function formatClock(ts: number): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

export function formatNumber(value?: number): string {
  return typeof value === 'number' ? value.toLocaleString() : '0'
}

export function formatPercent(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '0.0%'
  return `${value.toFixed(1)}%`
}

export function formatUnitPercent(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '0.0%'
  return `${(value * 100).toFixed(1)}%`
}

export function formatMoney(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '$0.00'
  return `$${value.toFixed(4)}`
}

export function formatDuration(ms?: number): string {
  if (typeof ms !== 'number' || Number.isNaN(ms) || ms <= 0) return '0s'
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(seconds >= 10 ? 0 : 1)}s`
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.round(seconds % 60)
  return `${minutes}m ${remainder}s`
}

export const SEARCH_DEFAULT_MIN_SEMANTIC_SCORE = 0.3
export const ALL_PROJECTS_SCOPE = '__all_projects__'

export const wikiSuggestionPresets = [
  { label: 'pinned threads', query: 'show pinned rules and long-lived facts' },
  { label: 'recent research', query: 'what did we recently learn' },
  { label: 'diagrams', query: 'architecture diagram mermaid flow' },
  { label: 'failures', query: 'recent failures regressions incidents' },
] as const

export const semanticFloorPresets = [
  { label: 'diagnose 0.00', value: 0 },
  { label: 'default 0.30', value: 0.3 },
  { label: 'medium 0.40', value: 0.4 },
  { label: 'high 0.55', value: 0.55 },
] as const

export type RelevanceTone = 'high' | 'medium' | 'low' | 'weak'

export function formatScore(value?: number, digits = 3): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return 'n/a'
  return value.toFixed(digits)
}

export function clampUnitScore(value: number): number {
  if (!Number.isFinite(value)) return SEARCH_DEFAULT_MIN_SEMANTIC_SCORE
  return Math.min(1, Math.max(0, value))
}

export function parseUnitScore(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  if (Number.isNaN(parsed)) return undefined
  return clampUnitScore(parsed)
}

export function getSemanticSimilarity(memory: MemoryEntry): number | undefined {
  const value = memory.score_breakdown?.semantic_similarity
  if (typeof value !== 'number' || Number.isNaN(value)) return undefined
  return clampUnitScore(value)
}

export function getSemanticRelevance(value?: number): { label: string; tone: RelevanceTone } {
  if (typeof value !== 'number' || Number.isNaN(value)) return { label: 'Weak', tone: 'weak' }
  if (value >= 0.55) return { label: 'High', tone: 'high' }
  if (value >= 0.4) return { label: 'Medium', tone: 'medium' }
  if (value >= 0.3) return { label: 'Low', tone: 'low' }
  return { label: 'Weak', tone: 'weak' }
}

export function zeroTokenTotals(): TokenMetricTotals {
  return {
    records: 0,
    returned_tokens: 0,
    baseline_tokens: 0,
    saved_tokens: 0,
  }
}

export function getOperationTotals(items: TokenMetricOperationTotals[] | undefined, operation: string): TokenMetricTotals | null {
  return items?.find((item) => item.operation === operation) ?? null
}

export function getGroupOperationTotals(group: TokenMetricGroupTotals, operation: string): TokenMetricTotals | null {
  return group.operations?.find((item) => item.operation === operation) ?? null
}

export function formatBytes(bytes?: number): string {
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

export function toTitle(value: string): string {
  return value
    .split(/[_+\-\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

export function sortCountEntries(counts?: CountMap): Array<[string, number]> {
  return Object.entries(counts ?? {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
}

export function sumCounts(counts?: CountMap): number {
  return Object.values(counts ?? {}).reduce((total, value) => total + value, 0)
}

export function chartColor(index: number): string {
  return `var(--chart-${(index % 6) + 1})`
}

export function formatLegendIndex(index: number): string {
  return `[${String(index + 1).padStart(2, '0')}]`
}

export function buildPieGradient(entries: Array<[string, number]>): string {
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

export function pillList(items: string[]): React.ReactNode {
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

export function makeID(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `${Date.now()}-${Math.random().toString(16).slice(2)}`
  }
}

export function hasDiagram(m: MemoryEntry): boolean {
  if (m.diagram && m.diagram.code) return true
  if (m.content && (m.content.includes('```mermaid') || m.content.includes('```graph') || m.content.includes('```chart'))) return true
  return false
}

export function buildMemoryKey(memory: MemoryEntry): string {
  return `${memory.workspace}:${memory.id}`
}

export function compareMemoryRelevance(a: MemoryEntry, b: MemoryEntry): number {
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

export function compareMemoryRecency(a: MemoryEntry, b: MemoryEntry): number {
  return new Date(b.updated_at || b.created_at).getTime() - new Date(a.updated_at || a.created_at).getTime()
}

export function mergeMemoryResults(items: MemoryEntry[]): MemoryEntry[] {
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

export function escapeHTML(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

export async function buildConsolidatedExportHTML(memories: MemoryEntry[], theme: 'light' | 'dark'): Promise<string> {
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

export function getHealthState(stats: DashboardStats | null, statsErr: string) {
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
