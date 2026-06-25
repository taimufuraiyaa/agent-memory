import type { SchedulerRunHistory, SchedulerSummary } from '../lib/api'
import { formatDuration, formatNumber, formatTS } from './dashboardHelpers'

export function LifecyclePanel({
  workspace,
  scheduler,
  history,
  busy,
  error,
}: {
  workspace: string
  scheduler?: SchedulerSummary
  history: SchedulerRunHistory[]
  busy: boolean
  error: string
}) {
  if (!workspace) {
    return (
      <div className="surfacePanel">
        <div className="emptyState">
          <div className="emptyTitle">No Workspace Selected</div>
          <div className="emptyBody">Select a workspace to view its memory lifecycle history.</div>
        </div>
      </div>
    )
  }
  return (
    <div className="surfacePanel">
      <div className="panelHeader">
        <h2 className="panelTitle">Memory Lifecycle</h2>
        <p className="panelSubtitle">Background scheduler state and run history.</p>
      </div>

      {scheduler?.enabled ? (
        <div className="panelSection">
          <h3 className="sectionTitle">Scheduler State</h3>
          <div className="metricGrid">
            <div className="metricCard">
              <div className="metricLabel">Status</div>
              <div className="metricValue">
                {scheduler.workspace?.run_in_progress ? (
                  <span className="tone-good">Running</span>
                ) : (
                  <span className="tone-neutral">Idle</span>
                )}
              </div>
            </div>
            <div className="metricCard">
              <div className="metricLabel">Next Tick</div>
              <div className="metricValue">{formatTS(scheduler.next_tick_at)}</div>
            </div>
            <div className="metricCard">
              <div className="metricLabel">Last Scheduled</div>
              <div className="metricValue">{formatTS(scheduler.workspace?.last_scheduled_at)}</div>
            </div>
            <div className="metricCard">
              <div className="metricLabel">Last Completed</div>
              <div className="metricValue">{formatTS(scheduler.workspace?.last_completed_at)}</div>
            </div>
          </div>
        </div>
      ) : (
        <div className="panelSection">
          <div className="emptyState">
            <div className="emptyBody">Scheduler is disabled. Enable it from the agent-memory menubar.</div>
          </div>
        </div>
      )}

      {error ? (
        <div className="errAlert">Failed to load lifecycle history: {error}</div>
      ) : busy && history.length === 0 ? (
        <div className="emptyState">
          <div className="emptyBody">Loading history...</div>
        </div>
      ) : history.length === 0 ? (
        <div className="emptyState">
          <div className="emptyBody">No lifecycle runs recorded yet for this workspace.</div>
        </div>
      ) : (
        <div className="panelSection">
          <h3 className="sectionTitle">Recent Runs</h3>
          <table className="dataTable">
            <thead>
              <tr>
                <th>Started</th>
                <th>Result</th>
                <th>Duration</th>
                <th>Decay Updated</th>
                <th>Promoted</th>
                <th>Evicted</th>
              </tr>
            </thead>
            <tbody>
              {history.map((run) => (
                <tr key={run.id}>
                  <td>{formatTS(run.started_at)}</td>
                  <td>
                    {run.result === 'success' ? (
                      <span className="tone-good">Success</span>
                    ) : run.result === 'skipped' ? (
                      <span className="tone-neutral">Skipped ({run.skip_reason})</span>
                    ) : run.result === 'failed' ? (
                      <span className="tone-bad">Failed</span>
                    ) : (
                      <span className="tone-warn">{run.result}</span>
                    )}
                  </td>
                  <td>{formatDuration(run.duration_ms)}</td>
                  <td>{run.decay_updated > 0 ? formatNumber(run.decay_updated) : <span className="muted">-</span>}</td>
                  <td>{run.promoted > 0 ? <span className="tone-good">+{formatNumber(run.promoted)}</span> : <span className="muted">-</span>}</td>
                  <td>{run.evicted > 0 ? <span className="tone-bad">-{formatNumber(run.evicted)}</span> : <span className="muted">-</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
