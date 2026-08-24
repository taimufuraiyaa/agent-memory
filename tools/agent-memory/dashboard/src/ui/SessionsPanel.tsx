import type { MemoryType, ObservationEntry, ObservationPromotionResult, ReplayEvent, SessionEntry } from '../lib/api'
import { formatLegendIndex, formatNumber, formatTS, toTitle } from './dashboardHelpers'
import { BreakdownCard, DiagnosticRow } from './components'
import { ReplayTimeline } from './ReplayTimeline'

export function SessionsPanel({
  workspace,
  sessions,
  sessionsBusy,
  sessionsErr,
  sessionsUnavailable,
  selectedSessionID,
  observations,
  observationsBusy,
  observationsErr,
  promotionResult,
  promotionBusy,
  onSelectSession,
  onPromote,
  replayEvents,
  replayBusy,
  replayErr,
}: {
  workspace: string
  sessions: SessionEntry[]
  sessionsBusy: boolean
  sessionsErr: string
  sessionsUnavailable: boolean
  selectedSessionID: string
  observations: ObservationEntry[]
  observationsBusy: boolean
  observationsErr: string
  promotionResult?: ObservationPromotionResult
  promotionBusy: boolean
  onSelectSession: (sessionID: string) => void
  onPromote: (type?: MemoryType) => void
  replayEvents: ReplayEvent[]
  replayBusy: boolean
  replayErr: string
}) {
  const selectedSession = sessions.find((session) => session.session_id === selectedSessionID) ?? sessions[0]

  return (
    <section className="surfaceStack">
      <div className="diagnosticsHero">
        <div>
          <div className="overviewEyebrow">Session Explorer</div>
          <h2 className="sectionTitle">{workspace || 'Workspace'} Sessions</h2>
          <p className="sectionText">Inspect one session at a time and promote it when the signal is worth keeping.</p>
        </div>
      </div>

      {sessionsUnavailable ? (
        <div className="emptyStateCard">
          <div className="overviewEyebrow">Auto-Capture Disabled</div>
          <div className="sectionTitle">Sessions Are Not Available Yet</div>
          <p className="sectionText">
            The session routes are feature-gated. Enable observation capture to populate this explorer, then reload the dashboard.
          </p>
        </div>
      ) : null}

      {!sessionsUnavailable && sessionsErr ? <div className="callout calloutBad">{sessionsErr}</div> : null}

      {!sessionsUnavailable && !sessionsBusy && sessions.length === 0 && !sessionsErr ? (
        <div className="emptyStateCard">
          <div className="overviewEyebrow">No Sessions Yet</div>
          <div className="sectionTitle">This Workspace Has No Captured Sessions</div>
          <p className="sectionText">
            Auto-capture is enabled, but there are no recent sessions for this workspace yet. Once observations are ingested, they will appear here ordered by recent activity.
          </p>
        </div>
      ) : null}

      {!sessionsUnavailable && sessions.length > 0 ? (
        <section className="comparisonSection">
          <div className="comparisonHeader">
            <div>
              <div className="breakdownTitle">Session Timeline</div>
              <div className="breakdownSubtitle">Pick a session to answer basic "what happened?" questions and optionally promote it into memory.</div>
            </div>
          </div>

          <div className="sessionExplorerLayout">
            <div className="sessionRail">
              {sessions.map((session, index) => {
                const isSelected = session.session_id === selectedSession?.session_id
                const promoted = promotionResult && session.session_id === promotionResult.session_id
                return (
                  <button
                    key={session.session_id}
                    type="button"
                    className={isSelected ? 'sessionRailCard sessionRailCardOn' : 'sessionRailCard'}
                    onClick={() => onSelectSession(session.session_id)}
                  >
                    <div className="sessionRailTop">
                      <div className="sessionRailHeading">
                        <span className="sessionRailIndex">{formatLegendIndex(index)}</span>
                        <span className="sessionRailLead">.-</span>
                        <div className="sessionRailTitle">{session.session_id}</div>
                      </div>
                      <span className="groupBadge groupBadgeOn">{formatNumber(session.observation_count)} obs</span>
                    </div>
                    <div className="sessionRailMeta">
                      <span className="sessionRailStamp">last_seen:</span> {formatTS(session.last_seen_at) || 'n/a'}
                    </div>
                    {promoted ? (
                      <div className="sessionRailMeta">
                        <span className="sessionRailStamp">promotion:</span>{' '}
                        {promotionResult.deduplicated ? 'Already promoted' : promotionResult.rejected ? 'Promotion rejected' : 'Promotion recorded'}
                      </div>
                    ) : null}
                  </button>
                )
              })}
            </div>

            <div className="sessionDetailPanel">
              {selectedSession ? (
                <>
                  <div className="sessionDetailHeader">
                    <div>
                      <div className="sessionCardTitle">{selectedSession.session_id}</div>
                      <div className="sessionCardMeta">Observation timeline for the selected session</div>
                      <div className="sessionDetailSummary asciiChipRow">
                        <span className="asciiChipRowItem">
                          <span className="asciiChipIndex">[01]</span>
                          <span className="asciiChipLead">.-</span>
                          <span className="overviewMetaItem">{formatNumber(selectedSession.observation_count)} observations</span>
                        </span>
                        <span className="asciiChipRowItem">
                          <span className="asciiChipIndex">[02]</span>
                          <span className="asciiChipLead">.-</span>
                          <span className="overviewMetaItem">last seen {formatTS(selectedSession.last_seen_at) || 'n/a'}</span>
                        </span>
                      </div>
                    </div>
                    <div className="sessionDetailActions">
                      <button className="btn btnPrimary" type="button" disabled={promotionBusy} onClick={() => onPromote('episodic')}>
                        {promotionBusy ? 'Promoting...' : 'Promote Episodic'}
                      </button>
                      <button className="btn" type="button" disabled={promotionBusy} onClick={() => onPromote('procedural')}>
                        Promote Procedural
                      </button>
                      <button className="btn" type="button" disabled={promotionBusy} onClick={() => onPromote('semantic')}>
                        Promote Semantic
                      </button>
                    </div>
                  </div>

                  <div className="diagnosticsGrid">
                    <BreakdownCard title="Session Facts" subtitle="High-level session metadata">
                      <div className="diagnosticsList">
                        <DiagnosticRow label="Last Seen" value={formatTS(selectedSession.last_seen_at) || 'n/a'} />
                        <DiagnosticRow label="Started" value={formatTS(selectedSession.started_at) || 'n/a'} />
                        <DiagnosticRow label="Ended" value={formatTS(selectedSession.ended_at) || 'open'} />
                        <DiagnosticRow label="Project Root" value={selectedSession.project_root || 'n/a'} />
                        <DiagnosticRow label="CWD" value={selectedSession.cwd || 'n/a'} />
                      </div>
                    </BreakdownCard>

                    <BreakdownCard title="Promotion Status" subtitle="Visible once a promotion attempt has been made">
                      {promotionResult ? (
                        <div className="diagnosticsList">
                          <DiagnosticRow label="Requested Type" value={promotionResult.requested_type} />
                          <DiagnosticRow
                            label="Result"
                            value={
                              promotionResult.rejected
                                ? 'rejected'
                                : promotionResult.deduplicated
                                  ? 'deduplicated'
                                  : promotionResult.created_id
                                    ? 'created'
                                    : 'empty'
                            }
                          />
                          <DiagnosticRow label="Created ID" value={promotionResult.created_id || 'n/a'} />
                          <DiagnosticRow label="Observations" value={formatNumber(promotionResult.observations)} />
                          <DiagnosticRow label="Storage Tier" value={promotionResult.storage_tier || 'n/a'} />
                          <DiagnosticRow label="Confidence" value={typeof promotionResult.confidence === 'number' ? promotionResult.confidence.toFixed(2) : 'n/a'} />
                          {promotionResult.reject_reason ? <DiagnosticRow label="Reason" value={promotionResult.reject_reason} /> : null}
                        </div>
                      ) : (
                        <div className="muted">No promotion has been run for this session in the current dashboard view yet.</div>
                      )}
                    </BreakdownCard>
                  </div>

                  {observationsErr ? <div className="callout calloutBad">{observationsErr}</div> : null}

                  <section className="timelineSection">
                    <div className="comparisonHeader"><div><div className="breakdownTitle">Sanitized Session Replay</div><div className="breakdownSubtitle">Play, step, and filter captured or imported events while tracing promoted memory provenance.</div></div></div>
                    <ReplayTimeline events={replayEvents} busy={replayBusy} error={replayErr} />
                  </section>

                  <section className="timelineSection">
                    <div className="comparisonHeader">
                      <div>
                        <div className="breakdownTitle">Observation Timeline</div>
                        <div className="breakdownSubtitle">Newest first. Each observation is already privacy-scrubbed and summarized.</div>
                      </div>
                    </div>

                    {observationsBusy ? <div className="emptyInline">Loading observations...</div> : null}
                    {!observationsBusy && observations.length === 0 ? <div className="emptyInline">No observations for this session yet.</div> : null}
                    {!observationsBusy && observations.length > 0 ? (
                      <div className="timelineList">
                        {observations.map((observation) => (
                          <article key={observation.id} className="timelineCard">
                            <div className="timelineRail" aria-hidden="true">
                              <span className="timelineRailDot" />
                              <span className="timelineRailLine" />
                            </div>
                            <div className="timelineCardBody">
                              <div className="timelineTop">
                                <div>
                                  <div className="timelineTitle">
                                    <span className="timelinePrefix">.-</span>
                                    <span>{toTitle(observation.kind)}</span>
                                  </div>
                                  <div className="timelineMeta">{formatTS(observation.occurred_at) || 'n/a'}</div>
                                </div>
                                <span className="memPill timelineToolPill">{observation.tool_name || 'system'}</span>
                              </div>
                              <div className="timelineSummary">{observation.summary}</div>
                              <div className="timelineFooter">
                                <span className="mono timelineStamp">id:{observation.id.slice(0, 16)}...</span>
                                <span className="muted small timelineStamp">created:{formatTS(observation.created_at) || 'n/a'}</span>
                              </div>
                            </div>
                          </article>
                        ))}
                      </div>
                    ) : null}
                  </section>
                </>
              ) : null}
            </div>
          </div>
        </section>
      ) : null}
    </section>
  )
}
