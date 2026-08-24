import { useEffect, useState } from 'react'
import {
  getDeploymentProfile,
  updateDeploymentProfile,
  type DeploymentDecisionStatus,
  type DeploymentProfile,
} from '../lib/api'
import './deployment.css'

export function DeploymentPanel() {
  const [profile, setProfile] = useState<DeploymentProfile | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState('')

  async function refresh() {
    setBusy(true)
    try {
      const response = await getDeploymentProfile()
      setProfile(response.profile)
      setError('')
      setSaved('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  function patch(patchValue: Partial<DeploymentProfile>) {
    setProfile((current) => current ? { ...current, ...patchValue } : current)
    setSaved('')
  }

  function setBudget(monthlyBudget: number) {
    patch({ monthly_infrastructure_operations_budget_usd: monthlyBudget })
  }

  async function save() {
    if (!profile) return
    setBusy(true)
    try {
      const response = await updateDeploymentProfile({
        monthly_infrastructure_operations_budget_usd: profile.monthly_infrastructure_operations_budget_usd,
        decision_status: profile.decision_status,
        expected_revision: profile.revision,
      })
      setProfile(response.profile)
      setError('')
      setSaved('Infrastructure planning settings saved. No deployment or spending action was performed.')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
      setSaved('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="surfacePanel deploymentPanel">
      <header className="deploymentHeader">
        <div>
          <div className="deploymentEyebrow">internal operator settings</div>
          <h2 className="panelTitle">Self-managed infrastructure</h2>
          <p className="panelSubtitle">One installation-wide planning profile for internal DevOps and operations. It is not available to SaaS tenants, workspaces, or MCP clients.</p>
        </div>
        <button className="deploymentSecondary" type="button" onClick={() => void refresh()} disabled={busy}>Refresh</button>
      </header>

      <section className="deploymentBoundary" aria-label="Configuration safety boundary">
        <div className="deploymentBoundaryMark">INTERNAL</div>
        <div>
          <strong>Planning only — saving never deploys or spends.</strong>
          <p>Development, staging, and production remain self-managed. This record contains no provider choice, credentials, customer billing, or infrastructure command.</p>
        </div>
      </section>

      {error ? <div className="errAlert" role="alert">{error}</div> : null}
      <div className="deploymentFeedback" aria-live="polite">{saved}</div>

      {!profile && busy ? <div className="deploymentLoading">Loading infrastructure settings…</div> : null}
      {profile ? (
        <section className="deploymentEditor">
          <div className="deploymentStatusRow">
            <span className={profile.decision_status === 'assumed' ? 'deploymentStatus assumed' : 'deploymentStatus confirmed'}>
              {profile.decision_status === 'assumed' ? 'ASSUMED' : 'OPERATOR CONFIRMED'}
            </span>
            <span>revision {profile.revision}</span>
          </div>

          <div className="deploymentFields">
            <label>
              <span>Monthly infrastructure operations budget · USD</span>
              <input
                type="number"
                min="0"
                max="1000000"
                step="1"
                value={profile.monthly_infrastructure_operations_budget_usd}
                onChange={(event) => setBudget(Number(event.target.value))}
              />
              <small>Default assumption: $1,000. This is an internal planning ceiling, not a customer price or automated expenditure.</small>
            </label>

            <label>
              <span>Decision status</span>
              <select value={profile.decision_status} onChange={(event) => patch({ decision_status: event.target.value as DeploymentDecisionStatus })}>
                <option value="assumed">Assumed · temporary planning value</option>
                <option value="operator_confirmed">Operator confirmed</option>
              </select>
              <small>Confirmation records an internal planning decision only.</small>
            </label>
          </div>

          <div className="deploymentActions">
            <span>Current monthly ceiling: ${profile.monthly_infrastructure_operations_budget_usd.toLocaleString('en-US')}</span>
            <button type="button" onClick={() => void save()} disabled={busy}>Save infrastructure settings</button>
          </div>
        </section>
      ) : null}
    </div>
  )
}
