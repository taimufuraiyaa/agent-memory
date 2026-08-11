import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useState } from 'react'
import type { DashboardRuntime } from '../lib/runtime'
import {
  acceptHostedProposal,
  acceptHostedRightsAttestation,
  createHostedCredential,
  createHostedProposal,
  deleteHostedAccount,
  deleteHostedSource,
  downloadHostedExport,
  editHostedProposal,
  getHostedBilling,
  getHostedExport,
  getHostedImport,
  getHostedPrivacy,
  getHostedRightsAttestationStatus,
  getLocalSession,
  importHostedBundle,
  listHostedCredentials,
  listHostedSources,
  queryHostedSources,
  rejectHostedProposal,
  requestHostedExport,
  requestHostedPlanChange,
  revokeHostedCredential,
  rotateHostedCredential,
  retryHostedSource,
  searchHostedMemories,
  signupLocalOwner,
  logoutLocalSession,
  uploadHostedSource,
  type HostedConnection,
  type HostedEvidence,
  type HostedImportResult,
  type HostedMemory,
  type HostedRightsBasis,
	type HostedSemanticContext,
  type HostedSource,
} from '../lib/hostedApi'
import { RightsAttestationGate } from './RightsAttestationGate'

type HostedArea = 'home' | 'library' | 'memory' | 'data' | 'settings'

const emptyConnection: HostedConnection = { token: '', tenant: '', workspace: '' }
const terminalSourceStates = new Set(['ready', 'failed', 'rejected', 'disabled', 'deleted'])
const sourcePollIntervalMs = 2_000
const areas: Array<{ id: HostedArea; label: string; hint: string }> = [
  { id: 'home', label: 'Home', hint: 'Workspace overview' },
  { id: 'library', label: 'Library', hint: 'Sources and questions' },
  { id: 'memory', label: 'Memory', hint: 'Search and review' },
  { id: 'data', label: 'Data', hint: 'Privacy and migration' },
  { id: 'settings', label: 'Settings', hint: 'Plan and access' },
]

function formatLabel(value: string): string {
  const spaced = value.replaceAll('_', ' ').replace(/([a-z])([A-Z])/g, '$1 $2')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

function formatFieldValue(value: unknown): ReactNode {
  if (value == null || value === '') return <span className="productMuted">Not set</span>
  if (typeof value === 'boolean') return value ? 'Yes' : 'No'
  if (typeof value === 'number') return <span className="productNumber">{value.toLocaleString()}</span>
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    if (value.length === 0) return <span className="productMuted">None</span>
    const scalarValues = value.filter((item) => ['string', 'number', 'boolean'].includes(typeof item)).slice(0, 4)
    return scalarValues.length === value.length ? scalarValues.map(String).join(', ') : `${value.length} items`
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>).slice(0, 6)
    return <span className="productNestedValue">{entries.map(([key, nested]) => `${formatLabel(key)}: ${typeof nested === 'object' ? 'available' : String(nested)}`).join(' · ')}</span>
  }
  return String(value)
}

function ProductFields({ value, empty }: { value: Record<string, unknown> | null; empty: string }) {
  if (!value) return <div className="productEmpty compact"><p>{empty}</p></div>
  const entries = Object.entries(value).slice(0, 24)
  if (entries.length === 0) return <div className="productEmpty compact"><p>No details are available.</p></div>
  return (
    <dl className="productFields">
      {entries.map(([key, field]) => <div key={key}><dt>{formatLabel(key)}</dt><dd>{formatFieldValue(field)}</dd></div>)}
    </dl>
  )
}

function SectionHeading({ eyebrow, title, description, action }: { eyebrow: string; title: string; description: string; action?: ReactNode }) {
  return (
    <header className="productSectionHeading">
      <div><p className="productEyebrow">{eyebrow}</p><h1>{title}</h1><p>{description}</p></div>
      {action ? <div className="productSectionAction">{action}</div> : null}
    </header>
  )
}

function StateBadge({ value }: { value: string }) {
  const normalized = value.toLowerCase()
  const tone = normalized === 'ready' || normalized === 'active' || normalized === 'completed'
    ? 'positive'
    : normalized === 'failed' || normalized === 'expired' || normalized === 'deleting'
      ? 'negative'
      : 'neutral'
  return <span className={`stateBadge ${tone}`}><span aria-hidden="true" />{formatLabel(value)}</span>
}

export function HostedApp({ runtime }: { runtime: DashboardRuntime }) {
	const localOnboarding = runtime.features.includes('local_onboarding')
  const [draft, setDraft] = useState<HostedConnection>(emptyConnection)
  const [connection, setConnection] = useState<HostedConnection>(emptyConnection)
  const [activeArea, setActiveArea] = useState<HostedArea>('home')
  const [sources, setSources] = useState<HostedSource[]>([])
  const [memories, setMemories] = useState<HostedMemory[]>([])
  const [evidence, setEvidence] = useState<HostedEvidence[]>([])
	const [semanticContext, setSemanticContext] = useState<HostedSemanticContext | null>(null)
  const [status, setStatus] = useState(localOnboarding ? 'Checking this private installation…' : 'Enter your connection details to begin.')
  const [localSessionState, setLocalSessionState] = useState<'loading' | 'signup_required' | 'authenticated'>(localOnboarding ? 'loading' : 'signup_required')
  const [busy, setBusy] = useState(false)
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const [uploadRights, setUploadRights] = useState<HostedRightsBasis>('lawfully_acquired_private_use')
  const [uploadPhase, setUploadPhase] = useState('')
  const [proposal, setProposal] = useState<{ id: string; content: string; status: string } | null>(null)
  const [privacy, setPrivacy] = useState<Record<string, unknown> | null>(null)
  const [billing, setBilling] = useState<Record<string, unknown> | null>(null)
  const [credentials, setCredentials] = useState<Array<Record<string, unknown>>>([])
  const [credentialSecret, setCredentialSecret] = useState('')
  const [exportOperation, setExportOperation] = useState<{ id: string; state: string; expires_at?: string } | null>(null)
  const [migrationFile, setMigrationFile] = useState<File | null>(null)
  const [migrationPassphrase, setMigrationPassphrase] = useState('')
  const [migrationKey, setMigrationKey] = useState(crypto.randomUUID())
  const [migrationResult, setMigrationResult] = useState<HostedImportResult | null>(null)
  const connected = Boolean(connection.tenant && connection.workspace && (connection.token || localOnboarding))
  const readySources = useMemo(() => sources.filter((source) => (source.progress?.state || source.state) === 'ready'), [sources])
  const hasProcessingSources = useMemo(
    () => sources.some((source) => !terminalSourceStates.has((source.progress?.state || source.state).toLowerCase())),
    [sources],
  )
  const getHostedAttestationStatus = useCallback(() => getHostedRightsAttestationStatus(connection), [connection])
  const acceptHostedAttestation = useCallback((input: { policy_version: string; accepted_statement_ids: string[] }) => acceptHostedRightsAttestation(connection, input), [connection])

  useEffect(() => {
    if (!localOnboarding) return
    let current = true
    void getLocalSession().then(async (session) => {
      if (!current) return
      setLocalSessionState(session.state)
      if (session.state === 'authenticated') await openLocalSession(session.tenant_id || '', session.workspace_id || '')
      else setStatus('Create the owner of this private installation to begin.')
    }).catch((error) => {
      if (!current) return
      setLocalSessionState('signup_required')
      setStatus(error instanceof Error ? error.message : 'The local session could not be checked.')
    })
    return () => { current = false }
  }, [localOnboarding])

  useEffect(() => {
    if (!connected || !hasProcessingSources) return

    let active = true
    const refresh = () => {
      void listHostedSources(connection)
        .then((values) => {
          if (active) setSources(values)
        })
        .catch(() => undefined)
    }
    const interval = window.setInterval(refresh, sourcePollIntervalMs)

    return () => {
      active = false
      window.clearInterval(interval)
    }
  }, [connected, connection, hasProcessingSources])

  async function refreshSources(active = connection): Promise<void> {
    const values = await listHostedSources(active)
    setSources(values)
    setStatus(values.length ? `${values.length} private source records are current.` : 'Your library is ready for its first source.')
  }

  async function connect(event: FormEvent): Promise<void> {
    event.preventDefault()
    setBusy(true)
    setStatus('Opening your private workspace…')
    try {
      const values = await listHostedSources(draft)
      setSources(values)
      setConnection(draft)
      setActiveArea('home')
      setStatus('Private workspace connected for this browser session.')
    } catch (error) {
      setStatus(error instanceof Error ? error.message : 'Connection failed. Check the three values and try again.')
    } finally { setBusy(false) }
  }

  async function openLocalSession(tenant: string, workspace: string): Promise<void> {
    if (!tenant || !workspace) throw new Error('The local owner workspace is incomplete.')
    const active = { token: '', tenant, workspace }
    const values = await listHostedSources(active)
    setSources(values)
    setConnection(active)
    setActiveArea('home')
    setLocalSessionState('authenticated')
    setStatus(values.length ? `${values.length} private source records are current.` : 'Your private workspace is ready.')
  }

  async function createLocalOwner(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setStatus('Creating your private workspace…')
    try {
      const session = await signupLocalOwner({
        display_name: String(form.get('display_name') || ''),
        email: String(form.get('email') || ''),
        private_installation_confirmed: form.get('private_installation_confirmed') === 'on',
      })
      await openLocalSession(session.tenant_id || '', session.workspace_id || '')
    } catch (error) {
      setStatus(error instanceof Error ? error.message : 'The local owner could not be created.')
    } finally { setBusy(false) }
  }

  async function disconnect(): Promise<void> {
	if (localOnboarding) await logoutLocalSession()
    setConnection(emptyConnection)
    setDraft(emptyConnection)
    setCredentialSecret('')
    setMigrationPassphrase('')
	setLocalSessionState(localOnboarding ? 'signup_required' : localSessionState)
	setStatus(localOnboarding ? 'Signed out from this browser. Your private data remains on this installation.' : 'Connection cleared from this browser session.')
  }

  async function uploadSource(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    if (!uploadFile) return
    const form = event.currentTarget
    setBusy(true)
    setUploadPhase('Checking file integrity and requesting private custody…')
    try {
      await uploadHostedSource(connection, uploadFile, uploadRights)
      setUploadPhase('Upload received. Indexing will continue in the background.')
      setStatus(`${uploadFile.name} is now in the private processing queue.`)
      setUploadFile(null)
      form.reset()
      await refreshSources()
    } catch (error) {
      setUploadPhase(error instanceof Error ? error.message : 'The source could not be uploaded.')
    } finally { setBusy(false) }
  }

  async function runMemorySearch(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    try {
      const result = await searchHostedMemories(connection, String(form.get('query') || ''))
      setMemories(result.items || [])
      setStatus(result.items?.length ? `${result.items.length} matching memories found.` : 'No durable memories matched this search.')
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Search failed.') }
    finally { setBusy(false) }
  }

  async function runSourceQuery(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    try {
      const result = await queryHostedSources(connection, readySources.map((source) => source.id), String(form.get('query') || ''))
      setEvidence(result.evidence || [])
		setSemanticContext(result.context?.semantic || null)
		setStatus(result.answerable
			? `${result.evidence?.length || 0} reconstructed source contexts are ready.`
			: result.evidence_available
				? 'Related source context was found, but it is not sufficient to answer this question.'
				: 'The ready sources do not contain enough authorized evidence.')
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Source query failed.') }
    finally { setBusy(false) }
  }

  async function createProposal(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    try {
      setProposal(await createHostedProposal(connection, { content: String(form.get('content') || ''), type: String(form.get('type') || 'semantic'), evidence }))
      setStatus('Proposal created. Review it before accepting durable memory.')
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Proposal failed.') }
  }

  async function changeProposal(action: 'edit' | 'accept' | 'reject'): Promise<void> {
    if (!proposal) return
    try {
      const next = action === 'edit'
        ? await editHostedProposal(connection, proposal.id, proposal.content)
        : action === 'accept' ? await acceptHostedProposal(connection, proposal.id) : await rejectHostedProposal(connection, proposal.id)
      setProposal(next)
      setStatus(action === 'accept' ? 'Memory accepted with source lineage.' : action === 'reject' ? 'Proposal rejected; no memory was created.' : 'Proposal edit saved.')
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Proposal update failed.') }
  }

  async function loadOperations(): Promise<void> {
    setBusy(true)
    const results = await Promise.allSettled([getHostedPrivacy(connection), getHostedBilling(connection), listHostedCredentials(connection)])
    if (results[0].status === 'fulfilled') setPrivacy(results[0].value)
    if (results[1].status === 'fulfilled') setBilling(results[1].value)
    if (results[2].status === 'fulfilled') setCredentials(results[2].value || [])
    const loaded = results.filter((result) => result.status === 'fulfilled').length
    setStatus(loaded === 3 ? 'Privacy, plan, and credential controls are current.' : `${loaded} of 3 settings areas loaded. Some permissions may be unavailable.`)
    setBusy(false)
  }

  async function createCredential(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const form = event.currentTarget
    const values = new FormData(form)
    const scopes = String(values.get('scopes') || '').split(',').map((value) => value.trim()).filter(Boolean)
    try {
      const created = await createHostedCredential(connection, String(values.get('label') || ''), scopes, new Date(String(values.get('expires_at') || '')).toISOString())
      setCredentialSecret(String(created.secret || created.token || ''))
      setStatus('Credential created. Its secret is shown once below.')
      setCredentials(await listHostedCredentials(connection))
      form.reset()
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Credential creation failed.') }
  }

  async function beginExport(): Promise<void> {
    try { setExportOperation(await requestHostedExport(connection)); setStatus('Export requested. Refresh until it is ready.') }
    catch (error) { setStatus(error instanceof Error ? error.message : 'Export request failed.') }
  }

  async function refreshExport(): Promise<void> {
    if (!exportOperation) return
    try { setExportOperation(await getHostedExport(connection, exportOperation.id)) }
    catch (error) { setStatus(error instanceof Error ? error.message : 'Export status failed.') }
  }

  async function saveExport(): Promise<void> {
    if (!exportOperation) return
    try {
      const blob = await downloadHostedExport(connection, exportOperation.id)
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `agent-memory-hosted-${connection.workspace}.json`
      anchor.click()
      URL.revokeObjectURL(url)
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Export download failed.') }
  }

  async function runMigrationImport(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    if (!migrationFile) return
    setBusy(true)
    try {
      const result = await importHostedBundle(connection, migrationFile, migrationPassphrase, migrationKey)
      setMigrationResult(result)
      setStatus(`Migration ${result.state}. Imported ${result.report.imported.length}, merged ${result.report.merged.length}, skipped ${result.report.skipped.length}, failed ${result.report.failed.length}.`)
    } catch (error) { setStatus(error instanceof Error ? error.message : 'Migration import failed. Retry uses the same safe request key.') }
    finally { setBusy(false) }
  }

  async function refreshMigration(): Promise<void> {
    if (!migrationResult) return
    try { setMigrationResult(await getHostedImport(connection, migrationResult.id)) }
    catch (error) { setStatus(error instanceof Error ? error.message : 'Migration status failed.') }
  }

  if (!connected) {
    return (
      <div className="hostedProduct productEntry" data-runtime-mode={runtime.mode}>
        <a className="skipLink" href="#hosted-main-content">Skip to onboarding</a>
        <header className="entryHeader"><div className="productBrand"><span aria-hidden="true">am</span><strong>Agent Memory</strong></div><p>Private knowledge, under your control.</p></header>
        <main id="hosted-main-content" className="entryCanvas">
          <section className="entryStory">
            <p className="productEyebrow">Self-managed workspace</p>
            <h1>{localOnboarding ? 'Your knowledge stays yours.' : 'Connect your private workspace.'}</h1>
            <p className="entryLead">Bring books, notes, and durable agent memory into one place you operate yourself.</p>
            <ul className="entryPromises"><li>Source originals stay inside your deployment.</li><li>Derived memory requires your review.</li><li>{localOnboarding ? 'Your browser session never exposes an API token.' : 'Connection details are cleared when you reload.'}</li></ul>
          </section>
          <section className="connectionCard" aria-labelledby="connection-title">
            {localOnboarding ? <>
              <div><p className="productEyebrow">Private local installation</p><h2 id="connection-title">{localSessionState === 'loading' ? 'Opening your workspace' : 'Create local owner'}</h2><p>{localSessionState === 'loading' ? 'Checking for an existing owner on this device.' : 'This first account owns the private tenant and default workspace on this installation.'}</p></div>
              {localSessionState === 'loading' ? <div className="sessionLoading" aria-live="polite"><span aria-hidden="true" /><p>Checking local session…</p></div> : <form className="productForm" onSubmit={createLocalOwner}>
                <label>Your name<input name="display_name" autoComplete="name" maxLength={120} required /></label>
                <label>Email address<input name="email" type="email" autoComplete="email" maxLength={254} required /></label>
                <label className="productChoice localOwnerConfirmation"><input name="private_installation_confirmed" type="checkbox" required /> I confirm this is my private installation and I am creating its local owner.</label>
                <button className="productPrimary" disabled={busy}>{busy ? 'Creating workspace…' : 'Create local owner'}</button>
              </form>}
            </> : <>
              <div><p className="productEyebrow">Managed session</p><h2 id="connection-title">Open workspace</h2><p>Use the values configured by this deployment. Nothing is saved in the browser.</p></div>
              <form className="productForm" onSubmit={connect}>
                <label>Access token<input type="password" autoComplete="off" required value={draft.token} onChange={(event) => setDraft({ ...draft, token: event.target.value })} /></label>
                <label>Tenant ID<input autoComplete="off" required value={draft.tenant} onChange={(event) => setDraft({ ...draft, tenant: event.target.value.trim() })} /></label>
                <label>Workspace ID<input autoComplete="off" required value={draft.workspace} onChange={(event) => setDraft({ ...draft, workspace: event.target.value.trim() })} /></label>
                <button className="productPrimary" disabled={busy}>{busy ? 'Opening workspace…' : 'Open workspace'}</button>
              </form>
            </>}
            <p className="productStatus inline" role="status" aria-live="polite">{status}</p>
          </section>
        </main>
      </div>
    )
  }

  return (
    <RightsAttestationGate getStatus={getHostedAttestationStatus} accept={acceptHostedAttestation}>
    <div className="hostedProduct" data-runtime-mode={runtime.mode}>
      <a className="skipLink" href="#hosted-main-content">Skip to content</a>
      <header className="productHeader">
        <div className="productBrand"><span aria-hidden="true">am</span><strong>Agent Memory</strong></div>
        <div className="workspaceContext"><span className="connectionDot" aria-hidden="true" /><div><small>Hosted Agent Memory</small><strong>{connection.workspace}</strong></div></div>
        <button className="productTextButton" type="button" onClick={() => void disconnect()}>{localOnboarding ? 'Sign out' : 'Disconnect'}</button>
      </header>
      <nav className="productNavigation" aria-label="Product areas">
        {areas.map((area) => <button type="button" key={area.id} className={activeArea === area.id ? 'active' : ''} aria-current={activeArea === area.id ? 'page' : undefined} onClick={() => setActiveArea(area.id)}><strong>{area.label}</strong><small>{area.hint}</small></button>)}
      </nav>

      <main id="hosted-main-content" className="productCanvas">
        {activeArea === 'home' ? <section className="productSection" aria-labelledby="home-title">
          <SectionHeading eyebrow="Workspace overview" title="Good to see your knowledge taking shape." description="A current view of private sources and the work ready for you." action={<button className="productPrimary" type="button" onClick={() => setActiveArea('library')}>Add a source</button>} />
          <div className="metricStrip">
            <article><span>{sources.length}</span><p>Private sources</p><small>in this workspace</small></article>
            <article><span>{readySources.length}</span><p>Ready to ask</p><small>indexed and authorized</small></article>
            <article><span>{memories.length}</span><p>Loaded memories</p><small>from your latest search</small></article>
          </div>
          <div className="homeComposition">
            <article className="homePrimary"><p className="productEyebrow">Next useful step</p><h2>{sources.length ? 'Ask what your library already knows.' : 'Start with a book or note you trust.'}</h2><p>{sources.length ? `${readySources.length} sources are ready for grounded questions and cited evidence.` : 'Upload one private copy. Agent Memory will index it without publishing it outside this deployment.'}</p><button type="button" onClick={() => setActiveArea('library')}>{sources.length ? 'Go to Library' : 'Add first source'}</button></article>
            <aside className="homeAside"><h2>How memory stays trustworthy</h2><ol><li><span>1</span><p><strong>Evidence first</strong>Ask private sources and inspect citations.</p></li><li><span>2</span><p><strong>Review the idea</strong>Edit or reject a proposed memory.</p></li><li><span>3</span><p><strong>Accept deliberately</strong>Only approved ideas become durable.</p></li></ol></aside>
          </div>
        </section> : null}

        {activeArea === 'library' ? <section className="productSection" aria-labelledby="library-title">
          <SectionHeading eyebrow="Private source custody" title="Library" description="Upload lawful copies, follow their indexing state, and ask only ready sources." action={<button type="button" className="productSecondary" disabled={busy} onClick={() => void refreshSources()}>Refresh sources</button>} />
          <div className="libraryLayout">
            <article className="uploadStudio"><p className="productEyebrow">Add to Library</p><h2>Upload a private source</h2><p>PDF, EPUB, Markdown, or text. The browser verifies the file before private custody begins.</p>
              <form className="productForm" onSubmit={uploadSource}>
                <label className="fileDrop">Source file<input type="file" accept=".pdf,.epub,.md,.markdown,.txt" required onChange={(event) => setUploadFile(event.target.files?.[0] || null)} /><span>{uploadFile ? `${uploadFile.name} · ${(uploadFile.size / 1048576).toFixed(1)} MB` : 'Choose one file'}</span></label>
                <label>Rights basis<select value={uploadRights} onChange={(event) => setUploadRights(event.target.value as HostedRightsBasis)}><option value="lawfully_acquired_private_use">Lawfully acquired private copy</option><option value="author_owned">I am the author or rights holder</option><option value="licensed">Licensed for this use</option><option value="public_domain_or_open">Public domain or open license</option></select></label>
                <button className="productPrimary" disabled={busy || !uploadFile}>{busy ? 'Preparing source…' : 'Upload privately'}</button>
              </form>{uploadPhase ? <p className="productStatus inline" role="status">{uploadPhase}</p> : null}
            </article>
            <div className="sourceCollection"><div className="collectionHeading"><h2>Source custody</h2><span>{sources.length} total</span></div>
              {sources.length === 0 ? <div className="productEmpty"><span aria-hidden="true">＋</span><h3>Your library is empty</h3><p>Choose a source on the left. Its processing and retention state will appear here.</p></div> : <div className="sourceList">{sources.map((source) => {
                const state = source.progress?.state || source.state
                return <article className="sourceRow" key={source.id}><div className="sourceType" aria-hidden="true">{source.filename.split('.').pop()?.slice(0, 4) || 'file'}</div><div className="sourceIdentity"><h3>{source.filename}</h3><p>{source.media_type}{source.progress?.stage ? ` · ${formatLabel(source.progress.stage)}` : ''}</p></div><StateBadge value={state} /><div className="sourceActions">{source.failure?.retryable ? <button type="button" onClick={() => void retryHostedSource(connection, source.id).then(() => refreshSources())}>Retry</button> : null}<button type="button" className="danger" onClick={() => window.confirm(`Delete ${source.filename}?`) && void deleteHostedSource(connection, source.id).then(() => refreshSources())}>Delete</button></div></article>
              })}</div>}
            </div>
          </div>
			<article className="askStudio"><div className="askIntro"><p className="productEyebrow">Reconstructive recall</p><h2>Ask ready sources</h2><p>Agent Memory restores the surrounding authorized context before deciding whether the source can answer.</p><span>{readySources.length} ready sources</span></div><form className="askForm" onSubmit={runSourceQuery}><textarea name="query" rows={4} maxLength={4000} placeholder="What do these sources say about…" required /><div><span className="productChoice">Locally reconstructed from cited passages</span><button className="productPrimary" disabled={busy || readySources.length === 0}>Recall source context</button></div></form></article>
			{evidence.length ? <div className="answerLayout"><header className="reconstructionHeading"><p className="productEyebrow">Source recall</p><h2>Reconstructed source context</h2><p>These are cited source windows, not durable memories. Review them before accepting any interpretation into Memory.</p>{semanticContext?.planner_used ? <p className="semanticTrace">Understood as {formatLabel(semanticContext.intent || 'question')} · {semanticContext.language || 'unknown language'} · {semanticContext.subject || 'unspecified subject'}{semanticContext.reranker_used ? ' · Locally reranked' : ''}</p> : semanticContext?.fallbacks?.length ? <p className="semanticTrace fallback">Deterministic recall fallback · local understanding unavailable</p> : null}</header><section className="evidenceList" aria-label="Reconstructed cited source context">{evidence.map((item) => { const citationCount = item.included_citation_ids?.length || 1; return <article key={`${item.source_id}:${item.citation_id}`}><div className="evidenceHeading"><span>{item.locator?.display || 'Source context'}</span><div className="evidenceMeta"><small>{citationCount} supporting citation{citationCount === 1 ? '' : 's'}</small>{item.window_clipped ? <small className="contextWarning">Context clipped at a safe limit</small> : null}</div></div><p>{item.text}</p><details><summary>Source lineage</summary><code>{(item.included_citation_ids || [item.citation_id]).join(' · ')}</code></details></article> })}</section><button type="button" className="productSecondary" onClick={() => setActiveArea('memory')}>Review as memory</button></div> : null}
        </section> : null}

        {activeArea === 'memory' ? <section className="productSection" aria-labelledby="memory-title">
          <SectionHeading eyebrow="Durable knowledge" title="Memory" description="Find what the agent retains and approve new memory only when the evidence is strong." />
          <article className="searchStudio"><h2>Search durable memories</h2><form className="productSearch" onSubmit={runMemorySearch}><input name="query" maxLength={4000} placeholder="Search an idea, decision, or procedure" required /><button className="productPrimary" disabled={busy}>Search</button></form></article>
          {memories.length === 0 ? <div className="productEmpty"><span aria-hidden="true">⌕</span><h3>No memories loaded</h3><p>Search for a concept to inspect durable memory in this workspace.</p></div> : <div className="memoryResults">{memories.map((memory) => <article key={memory.id}><div><StateBadge value={memory.type} /><small>{memory.updated_at ? new Date(memory.updated_at).toLocaleDateString() : 'Durable memory'}</small></div><p>{memory.content}</p></article>)}</div>}
          <article className="proposalStudio"><div><p className="productEyebrow">Human review</p><h2>Review derived memory</h2><p>{evidence.length ? `${evidence.length} cited passages are ready to support a proposal.` : 'Ask your Library first. A proposal needs cited evidence.'}</p></div>{evidence.length ? <form className="productForm" onSubmit={createProposal}><label>Proposed memory<textarea name="content" rows={4} maxLength={2000} required /></label><label>Memory type<select name="type" defaultValue="semantic"><option value="semantic">Semantic</option><option value="procedural">Procedural</option><option value="episodic">Episodic</option><option value="outcome">Outcome</option></select></label><button className="productPrimary">Create review proposal</button></form> : <button type="button" className="productSecondary" onClick={() => setActiveArea('library')}>Ask Library</button>}
            {proposal ? <div className="proposalReview"><div className="collectionHeading"><h3>Review before accepting</h3><StateBadge value={proposal.status} /></div><textarea aria-label="Proposal content" value={proposal.content} onChange={(event) => setProposal({ ...proposal, content: event.target.value })} /><div className="productActions"><button type="button" onClick={() => void changeProposal('edit')}>Save edit</button><button className="productPrimary" type="button" onClick={() => void changeProposal('accept')}>Accept memory</button><button className="danger" type="button" onClick={() => void changeProposal('reject')}>Reject</button></div></div> : null}
          </article>
        </section> : null}

        {activeArea === 'data' ? <section className="productSection" aria-labelledby="data-title">
          <SectionHeading eyebrow="Portability and rights" title="Data" description="Understand retained data, create exports, and move a standalone workspace by explicit copy." action={<button type="button" className="productSecondary" disabled={busy} onClick={() => void loadOperations()}>Refresh privacy details</button>} />
          <article className="productSurface"><div className="surfaceHeading"><div><p className="productEyebrow">Customer controls</p><h2>Privacy &amp; retention</h2></div></div><ProductFields value={privacy} empty="Refresh to inspect the retained data classes and deletion behavior available to this workspace." /></article>
          <div className="dataColumns">
            <article className="productSurface"><p className="productEyebrow">Portable copy</p><h2>Data export</h2><p>Request a hosted export, wait for preparation, then download it during its short availability window.</p><div className="productActions"><button className="productPrimary" type="button" onClick={() => void beginExport()}>Request export</button><button type="button" disabled={!exportOperation} onClick={() => void refreshExport()}>Refresh</button><button type="button" disabled={exportOperation?.state !== 'ready'} onClick={() => void saveExport()}>Download</button></div>{exportOperation ? <div className="operationState"><StateBadge value={exportOperation.state} /><span>{exportOperation.expires_at || 'Expiry pending'}</span></div> : null}</article>
            <article className="productSurface"><p className="productEyebrow">Copy-first move</p><h2>Import standalone migration</h2><p>Import an encrypted AMPB2 copy. Browser-created copies include memories and notes, not uploaded source originals.</p><form className="productForm" onSubmit={runMigrationImport}><label>AMPB2 bundle<input type="file" accept=".ampb2,application/octet-stream" required onChange={(event) => { setMigrationFile(event.target.files?.[0] || null); setMigrationResult(null); setMigrationKey(crypto.randomUUID()) }} /></label><label>Bundle passphrase<input type="password" autoComplete="off" minLength={12} maxLength={1024} required value={migrationPassphrase} onChange={(event) => setMigrationPassphrase(event.target.value)} /></label><button className="productPrimary" disabled={busy || !migrationFile}>Import encrypted copy</button></form>{migrationResult ? <div className="operationReport"><p>Imported <strong>{migrationResult.report.imported.length}</strong> · merged <strong>{migrationResult.report.merged.length}</strong> · skipped <strong>{migrationResult.report.skipped.length}</strong> · failed <strong>{migrationResult.report.failed.length}</strong></p><div><StateBadge value={migrationResult.state} /><button type="button" onClick={() => void refreshMigration()}>Refresh import</button></div></div> : null}</article>
          </div>
        </section> : null}

        {activeArea === 'settings' ? <section className="productSection" aria-labelledby="settings-title">
          <SectionHeading eyebrow="Workspace administration" title="Settings" description="Manage plan context, scoped agent access, and the hosted account lifecycle." action={<button type="button" className="productSecondary" disabled={busy} onClick={() => void loadOperations()}>Refresh settings</button>} />
          <article className="productSurface"><div className="surfaceHeading"><div><p className="productEyebrow">Subscription</p><h2>Plan &amp; usage</h2></div><div className="productActions"><button type="button" onClick={() => void requestHostedPlanChange(connection, 'upgrade', 'individual').then(() => setStatus('Plan change queued.'))}>Request Individual plan</button><button type="button" className="danger" onClick={() => window.confirm('Cancel the paid plan?') && void requestHostedPlanChange(connection, 'cancel', 'trial').then(() => setStatus('Cancellation queued.'))}>Cancel paid plan</button></div></div><ProductFields value={billing} empty="Refresh to inspect plan and reconciled usage available to this workspace." /></article>
          <article className="productSurface"><div className="surfaceHeading"><div><p className="productEyebrow">Scoped machine access</p><h2>Agent credentials</h2><p>Create the narrowest credential each client needs.</p></div></div>{credentialSecret ? <div className="oneTimeSecret"><div><strong>Copy this secret now</strong><p>It will disappear when dismissed or when this page reloads.</p></div><code>{credentialSecret}</code><div className="productActions"><button type="button" onClick={() => void navigator.clipboard.writeText(credentialSecret).then(() => setStatus('Credential secret copied.'))}>Copy secret</button><button type="button" onClick={() => setCredentialSecret('')}>Dismiss</button></div></div> : null}<form className="credentialForm" onSubmit={createCredential}><label>Label<input name="label" maxLength={128} required /></label><label>Scopes<input name="scopes" placeholder="memory:read,memory:write" required /></label><label>Expires at<input name="expires_at" type="datetime-local" required /></label><button className="productPrimary" disabled={!connected}>Create credential</button></form>{credentials.length ? <div className="credentialList">{credentials.map((credential) => { const id = String(credential.id || ''); return <article key={id}><div><h3>{String(credential.label || id)}</h3><p>{String(credential.scopes || 'Scoped access')} · {String(credential.state || '')}</p></div><div className="productActions"><button type="button" onClick={() => void rotateHostedCredential(connection, id, new Date(Date.now() + 30 * 86400000).toISOString()).then(() => loadOperations())}>Rotate</button><button className="danger" type="button" onClick={() => void revokeHostedCredential(connection, id).then(() => loadOperations())}>Revoke</button></div></article> })}</div> : <div className="productEmpty compact"><p>No agent credentials are loaded.</p></div>}</article>
          <article className="dangerZone"><div><p className="productEyebrow">Irreversible account action</p><h2>Delete hosted account</h2><p>Starts the verified hosted deletion lifecycle. It never affects a standalone SQLite workspace.</p></div><button type="button" className="danger" onClick={() => window.confirm('Delete this hosted account and all tenant data?') && void deleteHostedAccount(connection).then((operation) => setStatus(`Account deletion ${operation.state}.`))}>Delete account</button></article>
        </section> : null}
      </main>
      <div className="productStatus" role="status" aria-live="polite"><span aria-hidden="true" />{status}</div>
    </div>
    </RightsAttestationGate>
  )
}
