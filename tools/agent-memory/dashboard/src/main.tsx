import { StrictMode } from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './ui/App'
import { HostedApp } from './ui/HostedApp'
import { RightsAttestationGate } from './ui/RightsAttestationGate'
import { loadDashboardRuntime } from './lib/runtime'
import './ui/styles.css'
import './ui/hosted.css'

type PreloadRecoveryState = {
  attempted: boolean
  at: number
  message: string
}

type VitePreloadErrorEvent = Event & {
  payload?: unknown
}

const PRELOAD_RECOVERY_KEY = 'agent-memory:vite-preload-recovery'
const PRELOAD_RECOVERY_OVERLAY_ID = 'agent-memory-preload-recovery'
const PRELOAD_RECOVERY_TOAST_ID = 'agent-memory-preload-toast'
const PRELOAD_RECOVERY_TTL_MS = 30 * 60 * 1000
const PRELOAD_RECOVERY_RELOAD_DELAY_MS = 1200

let preloadRecoveryAttemptedInMemory = false

function readPreloadRecoveryState(): PreloadRecoveryState | null {
  try {
    const raw = window.sessionStorage.getItem(PRELOAD_RECOVERY_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<PreloadRecoveryState>
    if (!parsed || typeof parsed.at !== 'number') return null
    return {
      attempted: Boolean(parsed.attempted),
      at: parsed.at,
      message: typeof parsed.message === 'string' ? parsed.message : '',
    }
  } catch {
    return null
  }
}

function writePreloadRecoveryState(state: PreloadRecoveryState): void {
  preloadRecoveryAttemptedInMemory = state.attempted
  try {
    window.sessionStorage.setItem(PRELOAD_RECOVERY_KEY, JSON.stringify(state))
  } catch {
    // Ignore storage failures and keep the in-memory guard.
  }
}

function isRecentPreloadRecovery(state: PreloadRecoveryState | null): boolean {
  if (!state || !state.attempted) return false
  return Date.now() - state.at < PRELOAD_RECOVERY_TTL_MS
}

function getPreloadRecoveryMessage(payload: unknown): string {
  if (payload instanceof Error && payload.message.trim()) return payload.message
  if (typeof payload === 'string' && payload.trim()) return payload
  if (payload && typeof payload === 'object' && 'message' in payload) {
    const message = (payload as { message?: unknown }).message
    if (typeof message === 'string' && message.trim()) return message
  }
  return 'The dashboard failed to load one of its runtime modules.'
}

function renderPreloadRecoveryOverlay(message: string): void {
  const existing = document.getElementById(PRELOAD_RECOVERY_OVERLAY_ID)
  if (existing) existing.remove()

  const overlay = document.createElement('div')
  overlay.id = PRELOAD_RECOVERY_OVERLAY_ID
  Object.assign(overlay.style, {
    position: 'fixed',
    inset: '0',
    zIndex: '99999',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: '24px',
    background: 'rgba(9, 13, 22, 0.92)',
    color: '#f8fafc',
    fontFamily: 'Inter, system-ui, sans-serif',
  })

  const panel = document.createElement('div')
  Object.assign(panel.style, {
    width: 'min(560px, 100%)',
    border: '1px solid rgba(255, 255, 255, 0.16)',
    borderRadius: '18px',
    background: '#111827',
    boxShadow: '0 24px 80px rgba(0, 0, 0, 0.45)',
    padding: '24px',
  })

  const title = document.createElement('h1')
  title.textContent = 'Reload Required'
  Object.assign(title.style, {
    margin: '0 0 12px',
    fontSize: '22px',
    lineHeight: '1.2',
  })

  const body = document.createElement('p')
  body.textContent = 'The dashboard assets changed or a runtime chunk failed to load. Reload the page to reconnect to the current dev-server build.'
  Object.assign(body.style, {
    margin: '0 0 12px',
    color: '#cbd5e1',
    fontSize: '14px',
    lineHeight: '1.6',
  })

  const detail = document.createElement('pre')
  detail.textContent = message
  Object.assign(detail.style, {
    margin: '0 0 16px',
    padding: '12px',
    overflowX: 'auto',
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
    borderRadius: '12px',
    background: '#0f172a',
    border: '1px solid rgba(255, 255, 255, 0.08)',
    color: '#94a3b8',
    fontSize: '12px',
    lineHeight: '1.5',
    fontFamily: 'JetBrains Mono, ui-monospace, monospace',
  })

  const button = document.createElement('button')
  button.type = 'button'
  button.textContent = 'Reload Dashboard'
  Object.assign(button.style, {
    border: 'none',
    borderRadius: '12px',
    background: '#38bdf8',
    color: '#090d16',
    padding: '10px 16px',
    fontSize: '14px',
    fontWeight: '700',
    cursor: 'pointer',
  })
  button.addEventListener('click', () => {
    window.location.reload()
  })

  panel.append(title, body, detail, button)
  overlay.appendChild(panel)
  document.body.appendChild(overlay)
}

function renderPreloadRecoveryToast(message: string): void {
  const existing = document.getElementById(PRELOAD_RECOVERY_TOAST_ID)
  if (existing) existing.remove()

  const toast = document.createElement('div')
  toast.id = PRELOAD_RECOVERY_TOAST_ID
  Object.assign(toast.style, {
    position: 'fixed',
    right: '20px',
    bottom: '20px',
    zIndex: '99998',
    width: 'min(420px, calc(100vw - 40px))',
    padding: '14px 16px',
    borderRadius: '14px',
    border: '1px solid rgba(255, 255, 255, 0.14)',
    background: 'rgba(15, 23, 42, 0.96)',
    color: '#e2e8f0',
    boxShadow: '0 16px 48px rgba(0, 0, 0, 0.35)',
    fontFamily: 'Inter, system-ui, sans-serif',
  })

  const title = document.createElement('div')
  title.textContent = 'Refreshing Dashboard'
  Object.assign(title.style, {
    marginBottom: '6px',
    fontSize: '13px',
    fontWeight: '700',
    letterSpacing: '0.02em',
  })

  const body = document.createElement('div')
  body.textContent = 'A runtime module failed to load, so the dashboard will reload once to reconnect to the current dev-server build.'
  Object.assign(body.style, {
    fontSize: '13px',
    lineHeight: '1.5',
    color: '#cbd5e1',
  })

  const detail = document.createElement('div')
  detail.textContent = message
  Object.assign(detail.style, {
    marginTop: '8px',
    fontSize: '11px',
    lineHeight: '1.4',
    color: '#94a3b8',
    whiteSpace: 'nowrap',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    fontFamily: 'JetBrains Mono, ui-monospace, monospace',
  })

  toast.append(title, body, detail)
  document.body.appendChild(toast)
}

function installPreloadRecovery(): void {
  const existingState = readPreloadRecoveryState()
  if (existingState && !isRecentPreloadRecovery(existingState)) {
    try {
      window.sessionStorage.removeItem(PRELOAD_RECOVERY_KEY)
    } catch {
      // Ignore storage cleanup failures.
    }
    preloadRecoveryAttemptedInMemory = false
  } else {
    preloadRecoveryAttemptedInMemory = Boolean(existingState?.attempted)
  }

  window.addEventListener('vite:preloadError', (event: Event) => {
    const preloadEvent = event as VitePreloadErrorEvent
    const message = getPreloadRecoveryMessage(preloadEvent.payload)
    const state = readPreloadRecoveryState()
    const attempted = preloadRecoveryAttemptedInMemory || isRecentPreloadRecovery(state)

    preloadEvent.preventDefault()

    if (!attempted) {
      writePreloadRecoveryState({
        attempted: true,
        at: Date.now(),
        message,
      })
      renderPreloadRecoveryToast(message)
      window.setTimeout(() => {
        window.location.reload()
      }, PRELOAD_RECOVERY_RELOAD_DELAY_MS)
      return
    }

    renderPreloadRecoveryOverlay(message)
  })
}

installPreloadRecovery()

function RuntimeUnavailable({ message }: { message: string }) {
  return (
    <main className="runtimeUnavailable" role="alert">
      <p className="eyebrow">Safe startup</p>
      <h1>Dashboard runtime unavailable</h1>
      <p>{message}</p>
      <button type="button" onClick={() => window.location.reload()}>Retry discovery</button>
    </main>
  )
}

async function bootstrap(): Promise<void> {
  const root = ReactDOM.createRoot(document.getElementById('root')!)
  try {
    const runtime = await loadDashboardRuntime()
    root.render(
      <StrictMode>
        {runtime.mode === 'hosted' ? (
          <HostedApp runtime={runtime} />
        ) : (
          <RightsAttestationGate>
            <App />
          </RightsAttestationGate>
        )}
      </StrictMode>,
    )
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Runtime discovery failed.'
    root.render(<StrictMode><RuntimeUnavailable message={message} /></StrictMode>)
  }
}

void bootstrap()
