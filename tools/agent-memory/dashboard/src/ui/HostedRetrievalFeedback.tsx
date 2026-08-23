import { useMemo, useState } from 'react'
import { submitHostedRetrievalFeedback, type HostedConnection, type HostedRetrievalRequest } from '../lib/hostedApi'

export function HostedRetrievalFeedback({ connection, workspace, feedback, busy, error, onRefresh }: { connection: HostedConnection; workspace: string; feedback: HostedRetrievalRequest[]; busy: boolean; error: string; onRefresh: () => Promise<void> }) {
  const [query, setQuery] = useState('')
  const [type, setType] = useState<'all' | 'search' | 'recall'>('all')
  const [status, setStatus] = useState<'all' | 'scored' | 'pending'>('all')
  const [selected, setSelected] = useState<HostedRetrievalRequest | null>(null)
  const [score, setScore] = useState(5)
  const [reason, setReason] = useState('')
  const [useful, setUseful] = useState('')
  const [total, setTotal] = useState('')
  const [saveError, setSaveError] = useState('')
  const [saving, setSaving] = useState(false)
  const [loopMinutes, setLoopMinutes] = useState(5)

  const scored = feedback.filter((item) => item.score >= 0)
  const usefulRows = scored.filter((item) => item.total_count > 0 && item.useful_count >= 0)
  const stats = {
    average: scored.length ? scored.reduce((sum, item) => sum + item.score, 0) / scored.length : null,
    avoidance: scored.length ? scored.filter((item) => item.score >= 4).length / scored.length * 100 : null,
    useful: usefulRows.length ? usefulRows.reduce((sum, item) => sum + item.useful_count / item.total_count, 0) / usefulRows.length * 100 : null,
    saved: scored.filter((item) => item.score >= 4).length * loopMinutes / 60,
  }
  const visible = useMemo(() => feedback.filter((item) => {
    const text = `${item.query} ${item.reason} ${item.id}`.toLowerCase()
    return (type === 'all' || item.request_type === type) && (status === 'all' || (status === 'scored' ? item.score >= 0 : item.score < 0)) && text.includes(query.toLowerCase())
  }), [feedback, query, status, type])

  function choose(item: HostedRetrievalRequest) {
    setSelected(item); setScore(item.score >= 0 ? item.score : 5); setReason(item.reason || '')
    setUseful(item.useful_count >= 0 ? String(item.useful_count) : ''); setTotal(item.total_count >= 0 ? String(item.total_count) : ''); setSaveError('')
  }

  async function save() {
    if (!selected) return
    const usefulCount = useful === '' ? undefined : Number(useful)
    const totalCount = total === '' ? undefined : Number(total)
    if (score < 4 && !reason.trim()) return setSaveError('Explain what was missing for scores below 4.')
    if ((usefulCount != null && usefulCount < 0) || (totalCount != null && totalCount < 0) || (usefulCount != null && totalCount != null && usefulCount > totalCount)) return setSaveError('Useful hits must be between 0 and total hits.')
    try {
      setSaving(true); setSaveError('')
      await submitHostedRetrievalFeedback(connection, { workspace, request_id: selected.id, score, reason: reason.trim(), useful_count: usefulCount, total_count: totalCount })
      setSelected(null); await onRefresh()
    } catch (caught) { setSaveError(caught instanceof Error ? caught.message : 'Feedback could not be saved.') } finally { setSaving(false) }
  }

  return <div className="retrievalFeedback">
    <div className="feedbackMetrics">
      <article><strong>{feedback.length}</strong><span>Total queries</span></article>
      <article><strong>{stats.average == null ? '—' : `${stats.average.toFixed(1)}/5`}</strong><span>Average score</span></article>
      <article><strong>{stats.avoidance == null ? '—' : `${stats.avoidance.toFixed(1)}%`}</strong><span>Rework avoidance</span></article>
      <article><strong>{stats.useful == null ? '—' : `${stats.useful.toFixed(1)}%`}</strong><span>Useful-hit ratio</span></article>
      <article className="feedbackWorkloadMetric"><strong>{stats.saved ? `${stats.saved.toFixed(1)} hrs` : '—'}</strong><span>Workload saved</span><label><input className="feedbackLoopInput" aria-label="Minutes per rework loop" type="number" min="1" max="60" value={loopMinutes} onChange={(event) => setLoopMinutes(Math.max(1, Number(event.target.value) || 1))} /><small>min loop</small></label></article>
    </div>
    <div className="feedbackToolbar">
      <input type="search" placeholder="Search query, reason, or request ID…" value={query} onChange={(event) => setQuery(event.target.value)} />
      <label>Type<select value={type} onChange={(event) => setType(event.target.value as typeof type)}><option value="all">All</option><option value="search">Search</option><option value="recall">Recall</option></select></label>
      <label>Status<select value={status} onChange={(event) => setStatus(event.target.value as typeof status)}><option value="all">All</option><option value="scored">Scored</option><option value="pending">Pending</option></select></label>
    </div>
    {error ? <div className="processingError" role="alert">{error}</div> : busy ? <div className="productEmpty compact"><p>Loading retrieval feedback…</p></div> : !feedback.length ? <div className="productEmpty processingEmpty"><h3>No retrieval requests yet</h3><p>Run search or recall in this project and its quality record will appear here.</p></div> : <div className="feedbackTableWrap"><table className="hostedFeedbackTable"><thead><tr><th>Time</th><th>Type</th><th>Query / task</th><th>Score</th><th>Explanation</th></tr></thead><tbody>{visible.map((item) => <tr key={item.id} onClick={() => choose(item)}><td>{new Date(item.created_at).toLocaleString()}</td><td><span className={`requestType ${item.request_type}`}>{item.request_type}</span></td><td>{item.query}</td><td>{item.score >= 0 ? <><strong>{item.score}/5</strong>{item.total_count > 0 ? <small>{item.useful_count}/{item.total_count} hits</small> : null}</> : <span className="pendingScore">Pending</span>}</td><td>{item.reason || '—'}</td></tr>)}</tbody></table></div>}
    {selected ? <div className="feedbackEditor" role="dialog" aria-modal="true" aria-label="Score retrieval request"><button className="feedbackEditorClose" onClick={() => setSelected(null)} aria-label="Close">×</button><p className="productEyebrow">{selected.request_type} · {selected.id}</p><h2>Score this retrieval</h2><p className="feedbackEditorQuery">{selected.query}</p><fieldset><legend>Quality score</legend><div className="scoreOptions">{[0,1,2,3,4,5].map((value) => <button type="button" className={score === value ? 'active' : ''} onClick={() => setScore(value)} key={value}>{value}</button>)}</div></fieldset><div className="hitInputs"><label>Useful hits<input type="number" min="0" value={useful} onChange={(event) => setUseful(event.target.value)} /></label><label>Total hits<input type="number" min="0" value={total} onChange={(event) => setTotal(event.target.value)} /></label></div><label>Explanation<textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder={score < 4 ? 'Required: explain what was missing…' : 'Optional quality notes…'} /></label>{saveError ? <p className="feedbackSaveError" role="alert">{saveError}</p> : null}<button className="productPrimary" disabled={saving} onClick={() => void save()}>{saving ? 'Saving…' : 'Save feedback'}</button></div> : null}
  </div>
}
