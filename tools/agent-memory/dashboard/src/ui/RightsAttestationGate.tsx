import { useCallback, useEffect, useState, type ReactNode } from 'react'
import {
  acceptRightsAttestation,
  getRightsAttestationStatus,
  type RightsAttestationStatus,
} from '../lib/api'
import './rights-attestation.css'

type AcceptanceInput = { policy_version: string; accepted_statement_ids: string[] }

type RightsAttestationGateProps = {
  children: ReactNode
  getStatus?: () => Promise<RightsAttestationStatus>
  accept?: (input: AcceptanceInput) => Promise<RightsAttestationStatus>
}

export function RightsAttestationGate({ children, getStatus = getRightsAttestationStatus, accept = acceptRightsAttestation }: RightsAttestationGateProps) {
  const [attestation, setAttestation] = useState<RightsAttestationStatus | null>(null)
  const [acceptedStatementIDs, setAcceptedStatementIDs] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const loadStatus = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setAttestation(await getStatus())
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setLoading(false)
    }
  }, [getStatus])

  useEffect(() => {
    void loadStatus()
  }, [loadStatus])

  useEffect(() => {
    setAcceptedStatementIDs(new Set())
  }, [attestation?.policy.version])

  async function acceptCurrentPolicy() {
    if (!attestation) return
    setSubmitting(true)
    setError('')
    try {
      await accept({
        policy_version: attestation.policy.version,
        accepted_statement_ids: [...acceptedStatementIDs],
      })
      setAttestation(await getStatus())
    } catch (reason) {
      setError(messageOf(reason))
      await loadStatus()
    } finally {
      setSubmitting(false)
    }
  }

  const status = attestation?.status
  if (status === 'active') return <>{children}</>

  if (loading && !attestation) {
    return <main className="rightsAttestationLoading" aria-live="polite">Checking your source-upload policy…</main>
  }

  if (!attestation) {
    return (
      <main className="rightsAttestationLoading" role="alert">
        <strong>We couldn’t load the source-upload policy.</strong>
        <p>{error || 'The rights-attestation service is unavailable.'}</p>
        <button type="button" onClick={() => void loadStatus()}>Try again</button>
      </main>
    )
  }

  const { policy } = attestation
  const allAccepted = acceptedStatementIDs.size === policy.statements.length

  return (
    <div className="rightsAttestationBackdrop">
      <section className="rightsAttestationDialog" role="dialog" aria-modal="true" aria-labelledby="rights-attestation-title" aria-describedby="rights-attestation-summary">
        <header>
          <span className="rightsAttestationMark" aria-hidden="true">§</span>
          <div>
            <p>Private source policy · renew every {policy.renewal_days} days</p>
            <h1 id="rights-attestation-title">Confirm your right to use uploaded materials</h1>
          </div>
        </header>

        <p id="rights-attestation-summary" className="rightsAttestationPrimary">{policy.primary_confirmation}</p>
        <div className="rightsAttestationStatements">
          {policy.statements.map((statement, index) => (
            <label key={statement.id}>
              <input
                type="checkbox"
                autoFocus={index === 0}
                checked={acceptedStatementIDs.has(statement.id)}
                onChange={(event) => {
                  const next = new Set(acceptedStatementIDs)
                  if (event.target.checked) next.add(statement.id)
                  else next.delete(statement.id)
                  setAcceptedStatementIDs(next)
                }}
              />
              <span>{statement.text}</span>
            </label>
          ))}
        </div>

        {error ? <p className="rightsAttestationError" role="alert">{error}</p> : null}
        <footer>
          <p>This is your representation, not copyright verification by Agent Memory.</p>
          <button type="button" disabled={submitting || acceptedStatementIDs.size !== policy.statements.length} onClick={() => void acceptCurrentPolicy()}>
            {submitting ? 'Recording confirmation…' : 'Confirm and continue'}
          </button>
        </footer>
      </section>
    </div>
  )
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}
