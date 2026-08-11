import { FormEvent, useState } from 'react'
import { downloadPortableMigration } from '../lib/api'

export function MigrationPanel({ workspace }: { workspace: string }) {
  const [passphrase, setPassphrase] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [status, setStatus] = useState('')
  const [busy, setBusy] = useState(false)

  async function createBundle(event: FormEvent): Promise<void> {
    event.preventDefault()
    if (passphrase !== confirmation) {
      setStatus('The passphrases do not match.')
      return
    }
    setBusy(true)
    setStatus('Creating an encrypted copy…')
    try {
      const bundle = await downloadPortableMigration(workspace, passphrase)
      const url = URL.createObjectURL(bundle)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `agent-memory-${workspace || 'workspace'}.ampb2`
      anchor.click()
      URL.revokeObjectURL(url)
      setStatus('Encrypted migration copy downloaded. Local data is not deleted.')
      setPassphrase('')
      setConfirmation('')
    } catch (error) {
      setStatus(error instanceof Error ? error.message : 'The migration copy could not be created.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel migrationPanel">
      <div className="panelHeader">
        <div><p className="eyebrow">Portable AMPB2</p><h2>Move this workspace to hosted Agent Memory</h2></div>
      </div>
      <div className="panelBody">
        <p>This creates an encrypted copy of memories and active notes. Uploaded source originals are excluded from browser migration.</p>
        <p><strong>Copy first:</strong> Local data is not deleted. Keep it until you verify the hosted import, then remove it yourself only if you choose.</p>
        <form className="formStack" onSubmit={createBundle}>
          <label>Workspace<input value={workspace} readOnly /></label>
          <label>Bundle passphrase<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} required value={passphrase} onChange={(event) => setPassphrase(event.target.value)} /></label>
          <label>Confirm passphrase<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} required value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></label>
          <button type="submit" disabled={busy || !workspace}>{busy ? 'Creating encrypted copy…' : 'Download migration copy'}</button>
        </form>
        <p role="status" aria-live="polite">{status}</p>
      </div>
    </section>
  )
}
