import { useEffect, useMemo, useState, type FormEvent } from 'react'
import {
  acceptHostedRightsAttestation,
  getHostedRightsAttestationStatus,
  getLocalSession,
  signupLocalOwner,
  type HostedConnection,
} from '../lib/hostedApi'
import { createHostedKnowledgeGateway } from '../lib/adapters/hostedKnowledgeGateway'
import type { DashboardRuntime } from '../lib/runtime'
import { RightsAttestationGate } from './RightsAttestationGate'
import { WorkspaceApp } from './WorkspaceApp'

const emptyConnection: HostedConnection = { token: '', tenant: '', workspace: '' }

export function HostedWorkspaceBootstrap({ runtime }: { runtime: DashboardRuntime }) {
  const localOnboarding = runtime.features.includes('local_onboarding')
  const [connection, setConnection] = useState<HostedConnection>(emptyConnection)
  const [draft, setDraft] = useState<HostedConnection>(emptyConnection)
  const [sessionState, setSessionState] = useState<'loading' | 'signup_required' | 'authenticated'>(localOnboarding ? 'loading' : 'signup_required')
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState(localOnboarding ? 'Checking this private installation…' : 'Enter your connection details to begin.')

  useEffect(() => {
    if (!localOnboarding) return
    let current = true
    getLocalSession().then((session) => {
      if (!current) return
      setSessionState(session.state)
      if (session.state === 'authenticated') setConnection({ token: '', tenant: session.tenant_id || '', workspace: session.workspace_id || '' })
      else setStatus('Create the owner of this private installation to begin.')
    }).catch((reason: unknown) => {
      if (!current) return
      setSessionState('signup_required')
      setStatus(reason instanceof Error ? reason.message : 'The local session could not be checked.')
    })
    return () => { current = false }
  }, [localOnboarding])

  const connected = Boolean(connection.tenant && connection.workspace && (connection.token || localOnboarding))
  const gateway = useMemo(() => connected ? createHostedKnowledgeGateway(connection) : null, [connected, connection])

  async function createOwner(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    const form = new FormData(event.currentTarget)
    try {
      const session = await signupLocalOwner({ display_name: String(form.get('display_name') || ''), email: String(form.get('email') || ''), private_installation_confirmed: form.get('private_installation_confirmed') === 'on' })
      setSessionState(session.state)
      setConnection({ token: '', tenant: session.tenant_id || '', workspace: session.workspace_id || '' })
    } catch (reason) {
      setStatus(reason instanceof Error ? reason.message : 'The local owner could not be created.')
    } finally {
      setBusy(false)
    }
  }

  function connect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setConnection({ token: draft.token.trim(), tenant: draft.tenant.trim(), workspace: draft.workspace.trim() })
  }

  if (!connected || !gateway) return <div className="hostedProduct productEntry" data-runtime-mode={runtime.mode}>
    <header className="entryHeader"><div className="productBrand"><span aria-hidden="true">am</span><strong>Agent Memory</strong></div><p>One private workspace experience.</p></header>
    <main className="entryCanvas">
      <section className="entryStory"><p className="productEyebrow">Unified workspace</p><h1>{localOnboarding ? 'Your knowledge stays yours.' : 'Connect your private workspace.'}</h1><p className="entryLead">Ask, search, browse, study projects, and import documents without switching applications.</p></section>
      <section className="connectionCard" aria-labelledby="unified-connection-title">
        {localOnboarding ? <><div><p className="productEyebrow">Private local installation</p><h2 id="unified-connection-title">{sessionState === 'loading' ? 'Opening your workspace' : 'Create local owner'}</h2></div>{sessionState === 'loading' ? <p aria-live="polite">Checking local session…</p> : <form className="productForm" onSubmit={createOwner}><label>Your name<input name="display_name" autoComplete="name" required /></label><label>Email address<input name="email" type="email" autoComplete="email" required /></label><label className="productChoice localOwnerConfirmation"><input name="private_installation_confirmed" type="checkbox" required /> I confirm this is my private installation.</label><button className="productPrimary" disabled={busy}>{busy ? 'Creating workspace…' : 'Create local owner'}</button></form>}</> : <><div><p className="productEyebrow">Managed session</p><h2 id="unified-connection-title">Open workspace</h2></div><form className="productForm" onSubmit={connect}><label>Access token<input type="password" autoComplete="off" required value={draft.token} onChange={(event) => setDraft({ ...draft, token: event.target.value })} /></label><label>Tenant ID<input required value={draft.tenant} onChange={(event) => setDraft({ ...draft, tenant: event.target.value })} /></label><label>Workspace ID<input required value={draft.workspace} onChange={(event) => setDraft({ ...draft, workspace: event.target.value })} /></label><button className="productPrimary">Open workspace</button></form></>}
        <p className="productStatus inline" role="status" aria-live="polite">{status}</p>
      </section>
    </main>
  </div>

  return <RightsAttestationGate getStatus={() => getHostedRightsAttestationStatus(connection)} accept={(input) => acceptHostedRightsAttestation(connection, input)}>
    <WorkspaceApp runtime={runtime} gateway={gateway} />
  </RightsAttestationGate>
}
