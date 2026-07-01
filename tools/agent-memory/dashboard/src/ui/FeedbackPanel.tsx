import React, { useState, useMemo } from 'react'
import type { RetrievalRequestLog } from '../lib/api'
import { formatTS } from './dashboardHelpers'

export function FeedbackPanel({
  workspace,
  feedback,
  busy,
  error,
}: {
  workspace: string
  feedback: RetrievalRequestLog[]
  busy: boolean
  error: string
}) {
  const [statusFilter, setStatusFilter] = useState<'all' | 'scored' | 'pending'>('all')
  const [typeFilter, setTypeFilter] = useState<'all' | 'search' | 'recall'>('all')
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedLog, setSelectedLog] = useState<RetrievalRequestLog | null>(null)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  // 1. Calculate stats aggregates
  const stats = useMemo(() => {
    const total = feedback.length
    const scoredList = feedback.filter((f) => f.score >= 0)
    const scored = scoredList.length
    const pending = total - scored
    const average =
      scored > 0
        ? (scoredList.reduce((acc, f) => acc + f.score, 0) / scored).toFixed(1)
        : '-'
    return { total, scored, pending, average }
  }, [feedback])

  // 2. Filter list
  const filteredFeedback = useMemo(() => {
    return feedback.filter((item) => {
      const matchesStatus =
        statusFilter === 'all' ||
        (statusFilter === 'scored' && item.score >= 0) ||
        (statusFilter === 'pending' && item.score < 0)

      const matchesType =
        typeFilter === 'all' ||
        item.request_type === typeFilter

      const queryLower = searchQuery.toLowerCase()
      const matchesSearch =
        !searchQuery ||
        item.query.toLowerCase().includes(queryLower) ||
        item.reason.toLowerCase().includes(queryLower) ||
        item.id.toLowerCase().includes(queryLower)

      return matchesStatus && matchesType && matchesSearch
    })
  }, [feedback, statusFilter, typeFilter, searchQuery])

  const handleCopy = (id: string) => {
    void navigator.clipboard.writeText(id)
    setCopiedId(id)
    setTimeout(() => setCopiedId(null), 2000)
  }

  if (!workspace) {
    return (
      <div className="surfacePanel">
        <div className="emptyState">
          <div className="emptyTitle">No Workspace Selected</div>
          <div className="emptyBody">Select a workspace to view its retrieval feedback logs.</div>
        </div>
      </div>
    )
  }

  return (
    <div className="surfacePanel">
      <div className="panelHeader">
        <h2 className="panelTitle">Retrieval Feedback</h2>
        <p className="panelSubtitle">Logged search and recall requests with AI Agent quality scoring and comments.</p>
      </div>

      {error ? (
        <div className="errAlert">Failed to load feedback logs: {error}</div>
      ) : busy && feedback.length === 0 ? (
        <div className="emptyState">
          <div className="emptyBody">Loading feedback logs...</div>
        </div>
      ) : feedback.length === 0 ? (
        <div className="emptyState">
          <div className="emptyBody">No retrieval requests or feedback logged yet for this workspace.</div>
        </div>
      ) : (
        <div className="feedbackContainer">
          {/* Summary Cards */}
          <div className="feedbackSummaryGrid">
            <div className="feedbackSummaryCard">
              <div className="feedbackSummaryLabel">Total Queries</div>
              <div className="feedbackSummaryValue">{stats.total}</div>
            </div>
            <div className="feedbackSummaryCard">
              <div className="feedbackSummaryLabel">Average Score</div>
              <div className="feedbackSummaryValue" style={{ color: stats.average !== '-' && Number(stats.average) >= 4 ? '#2ecc71' : 'inherit' }}>
                {stats.average !== '-' ? `${stats.average}/5` : '-'}
              </div>
            </div>
            <div className="feedbackSummaryCard">
              <div className="feedbackSummaryLabel">Scored Requests</div>
              <div className="feedbackSummaryValue" style={{ color: '#2ecc71' }}>{stats.scored}</div>
            </div>
            <div className="feedbackSummaryCard">
              <div className="feedbackSummaryLabel">Pending Scoring</div>
              <div className="feedbackSummaryValue" style={{ color: stats.pending > 0 ? '#e67e22' : 'inherit' }}>{stats.pending}</div>
            </div>
          </div>

          {/* Controls Bar */}
          <div className="feedbackControls">
            <div className="feedbackSearchWrapper">
              <input
                type="text"
                className="feedbackSearchInput"
                placeholder="Search query, reason, or request ID..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
            </div>

            <div style={{ display: 'flex', gap: '16px', flexWrap: 'wrap' }}>
              {/* Type Filter */}
              <div className="feedbackFilterGroup">
                <span className="feedbackFilterLabel">Type:</span>
                <div className="feedbackFilterOptions">
                  {(['all', 'search', 'recall'] as const).map((t) => (
                    <button
                      key={t}
                      type="button"
                      className={`feedbackFilterBtn ${typeFilter === t ? 'feedbackFilterBtnActive' : ''}`}
                      onClick={() => setTypeFilter(t)}
                    >
                      {t}
                    </button>
                  ))}
                </div>
              </div>

              {/* Status Filter */}
              <div className="feedbackFilterGroup">
                <span className="feedbackFilterLabel">Status:</span>
                <div className="feedbackFilterOptions">
                  {(['all', 'scored', 'pending'] as const).map((s) => (
                    <button
                      key={s}
                      type="button"
                      className={`feedbackFilterBtn ${statusFilter === s ? 'feedbackFilterBtnActive' : ''}`}
                      onClick={() => setStatusFilter(s)}
                    >
                      {s}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </div>

          {/* Main Layout containing list and drawer */}
          <div style={{ display: 'flex', gap: '20px', width: '100%', position: 'relative', alignItems: 'flex-start' }}>
            {/* Table Area */}
            <div style={{ flex: 1, minWidth: 0, border: '1px dashed var(--border)', background: 'var(--bg-surface)' }}>
              {filteredFeedback.length === 0 ? (
                <div className="emptyState" style={{ padding: '40px' }}>
                  <div className="emptyBody">No matching feedback logs found for this selection.</div>
                </div>
              ) : (
                <table className="feedbackTable">
                  <thead>
                    <tr style={{ borderBottom: '1px dashed var(--border)', background: 'var(--bg-input)' }}>
                      <th className="feedbackTableCell" style={{ width: '180px', fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-muted)' }}>Time</th>
                      <th className="feedbackTableCell" style={{ width: '90px', fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-muted)' }}>Type</th>
                      <th className="feedbackTableCell" style={{ width: '45%', fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-muted)' }}>Query / Task</th>
                      <th className="feedbackTableCell" style={{ width: '90px', textAlign: 'center', fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-muted)' }}>Score</th>
                      <th className="feedbackTableCell" style={{ width: '35%', fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-muted)' }}>Explanation Reason</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredFeedback.map((req) => (
                      <tr
                        key={req.id}
                        className={`feedbackTableRow ${selectedLog?.id === req.id ? 'feedbackTableRowActive' : ''}`}
                        onClick={() => setSelectedLog(req)}
                      >
                        <td className="feedbackTableCell mono" style={{ fontSize: '11px', color: 'var(--text-muted)' }}>
                          {formatTS(req.created_at)}
                        </td>
                        <td className="feedbackTableCell">
                          <span className={req.request_type === 'search' ? 'feedbackBadgeSearch' : 'feedbackBadgeRecall'}>
                            {req.request_type}
                          </span>
                        </td>
                        <td className="feedbackTableCell" style={{ whiteSpace: 'normal', wordBreak: 'break-word', fontSize: '13px' }}>
                          {req.query}
                        </td>
                        <td className="feedbackTableCell" style={{ textAlign: 'center' }}>
                          {req.score >= 0 ? (
                            <span className={`feedbackScoreBadge ${req.score >= 4 ? 'feedbackScoreHigh' : 'feedbackScoreLow'}`}>
                              {req.score}/5
                            </span>
                          ) : (
                            <span className="feedbackScoreBadge feedbackScorePending">Pending</span>
                          )}
                        </td>
                        <td className="feedbackTableCell">
                          <div className="feedbackReasonText" title={req.reason}>
                            {req.reason || <span className="muted">-</span>}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>

            {/* Sidebar Details Drawer */}
            {selectedLog && (
              <aside className="detailDrawer" style={{ width: '380px', flexShrink: 0, position: 'sticky', top: '20px', minHeight: '300px', display: 'flex', flexDirection: 'column' }}>
                <div className="detailDrawerTop" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <div className="detailDrawerTitle" style={{ fontFamily: 'var(--font-mono)', fontSize: '12px', fontWeight: 'bold', textTransform: 'uppercase', color: 'var(--accent-primary)' }}>
                    ..query details
                  </div>
                  <button
                    type="button"
                    className="closeBtn"
                    style={{ background: 'transparent', border: 'none', color: 'var(--text-muted)', fontSize: '18px', cursor: 'pointer', padding: '0 4px' }}
                    onClick={() => setSelectedLog(null)}
                    aria-label="Close details"
                  >
                    ×
                  </button>
                </div>

                <div className="detailDrawerBody" style={{ overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: '16px', padding: '18px' }}>
                  {/* Score block */}
                  <div className="detailSection" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', background: 'var(--bg-input)', padding: '12px', border: '1px dotted var(--border)' }}>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: '11px', color: 'var(--text-muted)' }}>QUALITY SCORE</span>
                    {selectedLog.score >= 0 ? (
                      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: '4px' }}>
                        <span className={`feedbackScoreBadge ${selectedLog.score >= 4 ? 'feedbackScoreHigh' : 'feedbackScoreLow'}`} style={{ fontSize: '14px', padding: '6px 12px' }}>
                          {selectedLog.score} / 5
                        </span>
                      </div>
                    ) : (
                      <span className="feedbackScoreBadge feedbackScorePending" style={{ padding: '6px 12px' }}>PENDING FEEDBACK</span>
                    )}
                  </div>

                  {/* Request ID block */}
                  <div className="detailSection">
                    <div className="memMetaLabel" style={{ marginBottom: '6px' }}>Request ID</div>
                    <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                      <span className="mono" style={{ fontSize: '11px', wordBreak: 'break-all', flex: 1, background: 'var(--bg-input)', padding: '6px 8px', border: '1px solid var(--border)' }}>
                        {selectedLog.id}
                      </span>
                      <button
                        type="button"
                        className="btn"
                        style={{ padding: '6px 10px', fontSize: '11px', fontFamily: 'var(--font-mono)', cursor: 'pointer' }}
                        onClick={() => handleCopy(selectedLog.id)}
                      >
                        {copiedId === selectedLog.id ? 'Copied' : 'Copy'}
                      </button>
                    </div>
                  </div>

                  {/* Timestamp & Type */}
                  <div style={{ display: 'flex', gap: '16px' }}>
                    <div style={{ flex: 1 }}>
                      <div className="memMetaLabel" style={{ marginBottom: '4px' }}>Logged Time</div>
                      <div style={{ fontFamily: 'var(--font-mono)', fontSize: '12px' }}>{formatTS(selectedLog.created_at)}</div>
                    </div>
                    <div style={{ flex: 1 }}>
                      <div className="memMetaLabel" style={{ marginBottom: '4px' }}>Action Type</div>
                      <div>
                        <span className={selectedLog.request_type === 'search' ? 'feedbackBadgeSearch' : 'feedbackBadgeRecall'}>
                          {selectedLog.request_type}
                        </span>
                      </div>
                    </div>
                  </div>

                  {/* Query text */}
                  <div className="detailSection">
                    <div className="memMetaLabel" style={{ marginBottom: '6px' }}>Query / Task Description</div>
                    <div
                      className="pre"
                      style={{
                        padding: '12px',
                        background: 'var(--bg-input)',
                        border: '1px dotted var(--border)',
                        fontFamily: 'var(--font-mono)',
                        fontSize: '12px',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-word',
                        maxHeight: '150px',
                        overflowY: 'auto'
                      }}
                    >
                      {selectedLog.query}
                    </div>
                  </div>

                  {/* Reason explanation text */}
                  <div className="detailSection">
                    <div className="memMetaLabel" style={{ marginBottom: '6px' }}>Feedback Reason / Explanation</div>
                    <div
                      className="pre"
                      style={{
                        padding: '12px',
                        background: 'var(--bg-input)',
                        border: '1px dotted var(--border)',
                        fontFamily: 'var(--font-mono)',
                        fontSize: '12px',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-word',
                        minHeight: '80px',
                        maxHeight: '180px',
                        overflowY: 'auto',
                        color: selectedLog.reason ? 'var(--text-main)' : 'var(--text-muted)'
                      }}
                    >
                      {selectedLog.reason || 'No explanation comments provided for this request.'}
                    </div>
                  </div>
                </div>
              </aside>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
