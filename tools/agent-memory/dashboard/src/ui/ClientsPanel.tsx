import { useEffect, useState, type FormEvent } from 'react'
import {
  createClientProfile,
  deleteClientProfile,
  listClientProfiles,
  updateClientProfile,
  type ClientKind,
  type ClientProfile,
  type ClientToolProfile,
} from '../lib/api'
import './clients.css'

const clientKinds: Array<{ value: ClientKind; label: string }> = [
  { value: 'codex', label: 'Codex' },
  { value: 'claude', label: 'Claude' },
  { value: 'cursor', label: 'Cursor' },
  { value: 'other', label: 'Other' },
]

const emptyForm = {
  id: '',
  display_name: '',
  client_kind: 'codex' as ClientKind,
  tool_profile: 'default' as ClientToolProfile,
}

export function ClientsPanel() {
  const [profiles, setProfiles] = useState<ClientProfile[]>([])
  const [form, setForm] = useState(emptyForm)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function refresh() {
    setBusy(true)
    try {
      const response = await listClientProfiles()
      setProfiles(response.profiles ?? [])
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  async function createProfile(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      const response = await createClientProfile(form)
      setProfiles((current) => [...current, response.profile].sort((a, b) => a.id.localeCompare(b.id)))
      setForm(emptyForm)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  async function saveProfile(profile: ClientProfile) {
    setBusy(true)
    try {
      const response = await updateClientProfile({
        id: profile.id,
        display_name: profile.display_name,
        client_kind: profile.client_kind,
        tool_profile: profile.tool_profile,
        expected_revision: profile.revision,
      })
      setProfiles((current) => current.map((item) => item.id === response.profile.id ? response.profile : item))
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  async function removeProfile(profile: ClientProfile) {
    if (!window.confirm(`Delete client profile “${profile.display_name}”? Its next MCP startup will fail until reconfigured.`)) return
    setBusy(true)
    try {
      await deleteClientProfile({ id: profile.id, expected_revision: profile.revision })
      setProfiles((current) => current.filter((item) => item.id !== profile.id))
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  function patchProfile(id: string, patch: Partial<ClientProfile>) {
    setProfiles((current) => current.map((profile) => profile.id === id ? { ...profile, ...patch } : profile))
  }

  return (
    <div className="surfacePanel clientsPanel">
      <header className="clientsHeader">
        <div>
          <div className="clientsEyebrow">installation settings</div>
          <h2 className="panelTitle">MCP Clients</h2>
          <p className="panelSubtitle">Give each local client its own tool surface. Settings apply across every workspace in this Agent Memory installation.</p>
        </div>
        <button className="clientsRefresh" type="button" onClick={() => void refresh()} disabled={busy}>Refresh</button>
      </header>

      <section className="profileExplainer" aria-label="Tool profile comparison">
        <article>
          <span className="profileLabel">Default</span>
          <strong>5 workflow tools</strong>
          <p>Write, search, recall, feedback, and session finalization. Recommended for normal agent work.</p>
        </article>
        <article>
          <span className="profileLabel profileLabelExpanded">Expanded</span>
          <strong>7 tools</strong>
          <p>Adds health diagnostics and session browsing for operators and troubleshooting clients.</p>
        </article>
        <div className="reconnectNotice"><span aria-hidden="true">↻</span> Saved changes apply after that client reconnects or restarts.</div>
      </section>

      {error ? <div className="errAlert" role="alert">{error}</div> : null}

      <form className="clientCreateForm" onSubmit={createProfile}>
        <div className="clientFormHeading">
          <span>Register a client</span>
          <small>No credentials or commands are stored.</small>
        </div>
        <label>
          <span>Client ID</span>
          <input required pattern="[a-z][a-z0-9_-]{0,63}" placeholder="codex-main" value={form.id} onChange={(event) => setForm({ ...form, id: event.target.value })} />
        </label>
        <label>
          <span>Display name</span>
          <input required maxLength={80} placeholder="Codex Desktop" value={form.display_name} onChange={(event) => setForm({ ...form, display_name: event.target.value })} />
        </label>
        <label>
          <span>Client kind</span>
          <select value={form.client_kind} onChange={(event) => setForm({ ...form, client_kind: event.target.value as ClientKind })}>
            {clientKinds.map((kind) => <option key={kind.value} value={kind.value}>{kind.label}</option>)}
          </select>
        </label>
        <label>
          <span>Tool profile</span>
          <select value={form.tool_profile} onChange={(event) => setForm({ ...form, tool_profile: event.target.value as ClientToolProfile })}>
            <option value="default">Default · 5 tools</option>
            <option value="expanded">Expanded · 7 tools</option>
          </select>
        </label>
        <button className="clientPrimaryAction" type="submit" disabled={busy}>Add client</button>
      </form>

      <section className="clientRegistry" aria-live="polite">
        <div className="clientRegistryHeading">
          <span>Registered clients</span>
          <span>{profiles.length.toString().padStart(2, '0')}</span>
        </div>
        {busy && profiles.length === 0 ? <div className="clientEmpty">Loading client profiles…</div> : null}
        {!busy && profiles.length === 0 ? <div className="clientEmpty">No clients registered yet. Add the first local MCP client above.</div> : null}
        {profiles.map((profile) => (
          <ClientProfileRow
            key={`${profile.id}:${profile.revision}`}
            profile={profile}
            busy={busy}
            onChange={(patch) => patchProfile(profile.id, patch)}
            onSave={() => void saveProfile(profile)}
            onDelete={() => void removeProfile(profile)}
          />
        ))}
      </section>
    </div>
  )
}

function ClientProfileRow({
  profile,
  busy,
  onChange,
  onSave,
  onDelete,
}: {
  profile: ClientProfile
  busy: boolean
  onChange: (patch: Partial<ClientProfile>) => void
  onSave: () => void
  onDelete: () => void
}) {
  const [copied, setCopied] = useState(false)
  const connectionValue = `AGENT_MEMORY_CLIENT_ID=${profile.id}`

  async function copyConnectionValue() {
    await navigator.clipboard.writeText(connectionValue)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  return (
    <article className="clientRow">
      <div className="clientIdentity">
        <span className="clientKindMark">{profile.client_kind.slice(0, 2).toUpperCase()}</span>
        <div>
          <input aria-label={`Display name for ${profile.id}`} maxLength={80} value={profile.display_name} onChange={(event) => onChange({ display_name: event.target.value })} />
          <div className="clientID">{profile.id} · revision {profile.revision}</div>
        </div>
      </div>
      <label className="clientField">
        <span>Kind</span>
        <select value={profile.client_kind} onChange={(event) => onChange({ client_kind: event.target.value as ClientKind })}>
          {clientKinds.map((kind) => <option key={kind.value} value={kind.value}>{kind.label}</option>)}
        </select>
      </label>
      <label className="clientField">
        <span>Tool profile</span>
        <select value={profile.tool_profile} onChange={(event) => onChange({ tool_profile: event.target.value as ClientToolProfile })}>
          <option value="default">Default · 5</option>
          <option value="expanded">Expanded · 7</option>
        </select>
      </label>
      <button className="connectionValue" type="button" onClick={() => void copyConnectionValue()} title="Copy connection value">
        <code>{connectionValue}</code>
        <span>{copied ? 'copied' : 'copy'}</span>
      </button>
      <div className="clientActions">
        <button type="button" onClick={onSave} disabled={busy || profile.display_name.trim() === ''}>Save</button>
        <button className="clientDelete" type="button" onClick={onDelete} disabled={busy}>Delete</button>
      </div>
    </article>
  )
}
