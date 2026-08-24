import { useEffect, useMemo, useState } from 'react'
import type { ReplayEvent } from '../lib/api'
import { formatTS, toTitle } from './dashboardHelpers'

export function ReplayTimeline({ events, busy, error }: { events: ReplayEvent[]; busy: boolean; error: string }) {
  const [playing, setPlaying] = useState(false)
  const [cursor, setCursor] = useState(0)
  const [speed, setSpeed] = useState(1)
  const [filter, setFilter] = useState('all')
  const kinds = useMemo(() => Array.from(new Set(events.map((event) => event.kind))), [events])
  const visible = useMemo(() => filter === 'all' ? events : events.filter((event) => event.kind === filter), [events, filter])

  useEffect(() => { setCursor(0); setPlaying(false) }, [events, filter])
  useEffect(() => {
    if (!playing || visible.length === 0) return
    const timer = window.setInterval(() => {
      setCursor((current) => {
        if (current >= visible.length - 1) { setPlaying(false); return current }
        return current + 1
      })
    }, 1000 / speed)
    return () => window.clearInterval(timer)
  }, [playing, speed, visible])

  if (busy) return <div className="emptyInline">Loading replay timeline...</div>
  if (error) return <div className="callout calloutBad">{error}</div>
  if (!events.length) return <div className="emptyInline">No sanitized replay events are available for this session.</div>

  const current = visible[Math.min(cursor, Math.max(visible.length - 1, 0))]
  return (
    <section className="replaySurface">
      <div className="replayControls">
        <button className="btn btnPrimary" type="button" onClick={() => setPlaying((value) => !value)}>{playing ? 'Pause' : 'Play'}</button>
        <button className="btn" type="button" disabled={cursor <= 0} onClick={() => setCursor((value) => Math.max(0, value - 1))}>Previous</button>
        <button className="btn" type="button" disabled={cursor >= visible.length - 1} onClick={() => setCursor((value) => Math.min(visible.length - 1, value + 1))}>Next</button>
        <select className="input replaySelect" value={speed} onChange={(event) => setSpeed(Number(event.target.value))} aria-label="Replay speed">
          {[0.5, 1, 2, 4].map((value) => <option key={value} value={value}>{value}×</option>)}
        </select>
        <select className="input replaySelect" value={filter} onChange={(event) => setFilter(event.target.value)} aria-label="Replay event filter">
          <option value="all">All events</option>
          {kinds.map((kind) => <option key={kind} value={kind}>{toTitle(kind)}</option>)}
        </select>
        <span className="mono muted">{visible.length ? cursor + 1 : 0}/{visible.length}</span>
      </div>
      {current ? (
        <article className="replayCurrent">
          <div className="timelineTop">
            <div><div className="timelineTitle"><span className="timelinePrefix">.-</span>{toTitle(current.kind)}</div><div className="timelineMeta">{formatTS(current.occurred_at)}</div></div>
            <span className="memPill">{current.capture_mode || 'captured'}</span>
          </div>
          <div className="timelineSummary">{current.summary}</div>
          <div className="replayProvenance">
            <span>actor:{current.actor || 'unknown'}</span>
            <span>tool:{current.tool_name || 'system'}</span>
            <span>observations:{current.related_observation_ids?.length || 0}</span>
            <span>memories:{current.related_memory_ids?.join(', ') || 'none'}</span>
          </div>
        </article>
      ) : <div className="emptyInline">No events match this filter.</div>}
      <div className="replaySequence" aria-label="Replay event sequence">
        {visible.map((event, index) => (
          <button key={event.event_id} type="button" className={index === cursor ? 'replayStep replayStepOn' : 'replayStep'} onClick={() => { setCursor(index); setPlaying(false) }}>
            <span>{String(index + 1).padStart(2, '0')}</span><span>{toTitle(event.kind)}</span><span>{formatTS(event.occurred_at)}</span>
          </button>
        ))}
      </div>
    </section>
  )
}
