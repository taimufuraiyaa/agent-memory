import { useEffect, useId, useMemo, useRef, useState } from 'react'
import mermaid from 'mermaid'
import type { Diagram } from '../lib/api'

function getMermaidThemeVariables(theme: 'light' | 'dark') {
  if (theme === 'dark') {
    return {
      darkMode: true,
      background: '#090d16',
      mainBkg: '#0f172a',
      nodeBkg: '#0f172a',
      primaryColor: '#0f172a',
      secondaryColor: '#111827',
      tertiaryColor: '#0b1220',
      primaryBorderColor: '#64748b',
      secondaryBorderColor: '#64748b',
      tertiaryBorderColor: '#475569',
      nodeBorder: '#64748b',
      clusterBkg: '#0b1220',
      clusterBorder: '#475569',
      lineColor: '#94a3b8',
      defaultLinkColor: '#94a3b8',
      edgeLabelBackground: '#111827',
      labelTextColor: '#f8fafc',
      textColor: '#f8fafc',
      primaryTextColor: '#f8fafc',
      secondaryTextColor: '#e2e8f0',
      tertiaryTextColor: '#e2e8f0',
    }
  }

  return {
    darkMode: false,
    background: '#f8fafc',
    mainBkg: '#ffffff',
    nodeBkg: '#ffffff',
    primaryColor: '#ffffff',
    secondaryColor: '#f8fafc',
    tertiaryColor: '#e2e8f0',
    primaryBorderColor: '#475569',
    secondaryBorderColor: '#64748b',
    tertiaryBorderColor: '#94a3b8',
    nodeBorder: '#475569',
    clusterBkg: '#e2e8f0',
    clusterBorder: '#94a3b8',
    lineColor: '#334155',
    defaultLinkColor: '#334155',
    edgeLabelBackground: '#f8fafc',
    labelTextColor: '#172033',
    textColor: '#172033',
    primaryTextColor: '#172033',
    secondaryTextColor: '#172033',
    tertiaryTextColor: '#172033',
  }
}

function ensureMermaid(theme: 'light' | 'dark') {
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: 'base',
    htmlLabels: false,
    fontFamily: 'Inter, system-ui, sans-serif',
    flowchart: {
      htmlLabels: false,
      useMaxWidth: true,
    },
    sequence: {
      useMaxWidth: true,
    },
    themeVariables: getMermaidThemeVariables(theme),
  })
}

let mermaidRenderQueue: Promise<void> = Promise.resolve()

function renderMermaidSvg(id: string, code: string, theme: 'light' | 'dark'): Promise<string> {
  const task = async () => {
    ensureMermaid(theme)

    // Mermaid render uses shared global state and temporary DOM work.
    // Serialize renders so theme switches across multiple viewers do not race.
    const container = document.createElement('div')
    container.style.visibility = 'hidden'
    container.style.position = 'absolute'
    document.body.appendChild(container)

    try {
      const result = await mermaid.render(id, code, container)
      return result.svg
    } finally {
      if (container.parentNode === document.body) {
        document.body.removeChild(container)
      }
    }
  }

  const run = mermaidRenderQueue.then(task, task)
  mermaidRenderQueue = run.then(
    () => undefined,
    () => undefined,
  )
  return run
}

export function DiagramViewer({ diagram, theme }: { diagram: Diagram; theme: 'light' | 'dark' }) {
  const [mode, setMode] = useState<'render' | 'code'>(diagram.lang === 'mermaid' ? 'render' : 'code')
  const [svg, setSvg] = useState<string>('')
  const [err, setErr] = useState<string>('')
  const [fullScreen, setFullScreen] = useState<boolean>(false)
  const rid = useId()
  const renderNonceRef = useRef(0)

  const isMermaid = useMemo(() => diagram.lang.trim().toLowerCase() === 'mermaid', [diagram.lang])
  const diagramSvgClassName = useMemo(() => `diagramSvg ${theme === 'dark' ? 'diagramSvgDark' : 'diagramSvgLight'}`, [theme])

  useEffect(() => {
    if (!isMermaid || mode !== 'render') return
    let cancelled = false
    setErr('')
    const id = `m-${rid.replace(/[:]/g, '')}-${theme}-${renderNonceRef.current++}`

    renderMermaidSvg(id, diagram.code, theme)
      .then((nextSvg) => {
        if (cancelled) return
        setSvg(nextSvg)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setErr(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [diagram.code, isMermaid, mode, rid, theme])

  return (
    <div className="diagram">
      <div className="diagramTop">
        <div className="diagramLang">{diagram.lang || 'diagram'}</div>
        {isMermaid ? (
          <div className="seg">
            <button className={mode === 'render' ? 'segBtn segOn' : 'segBtn'} onClick={() => setMode('render')}>
              Render
            </button>
            <button className={mode === 'code' ? 'segBtn segOn' : 'segBtn'} onClick={() => setMode('code')}>
              Code
            </button>
          </div>
        ) : null}
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <button className="btn btnGhost" onClick={() => navigator.clipboard.writeText(diagram.code)} title="Copy code">
            Copy
          </button>
          <button className="btn btnGhost" onClick={() => setFullScreen(true)} title="Full Screen View" aria-label="Open full screen">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7" />
            </svg>
          </button>
        </div>
      </div>

      {mode === 'render' && isMermaid ? (
        <>
          {err ? <div className="callout calloutBad">{err}</div> : null}
          {svg ? <div className={diagramSvgClassName} dangerouslySetInnerHTML={{ __html: svg }} /> : <div className="muted">Rendering…</div>}
        </>
      ) : (
        <pre className="pre preCode">{diagram.code}</pre>
      )}

      {fullScreen ? (
        <div
          className="modalBackdrop"
          style={{ zIndex: 1000 }}
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) setFullScreen(false)
          }}
          role="presentation"
        >
          <div className="modalPanel" style={{ maxWidth: '95vw', width: '95vw', height: '90vh', maxHeight: '90vh' }} role="dialog" aria-modal="true" aria-label="Fullscreen Diagram">
            <div className="modalTop">
              <div className="modalTitle">{(diagram.lang || 'Diagram').toUpperCase()}</div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                {isMermaid ? (
                  <div className="seg">
                    <button className={mode === 'render' ? 'segBtn segOn' : 'segBtn'} onClick={() => setMode('render')}>
                      Render
                    </button>
                    <button className={mode === 'code' ? 'segBtn segOn' : 'segBtn'} onClick={() => setMode('code')}>
                      Code
                    </button>
                  </div>
                ) : null}
                <button className="btn btnGhost" onClick={() => navigator.clipboard.writeText(diagram.code)}>
                  Copy
                </button>
                <button className="btn btnGhost" onClick={() => setFullScreen(false)}>
                  Close
                </button>
              </div>
            </div>
            <div className="modalBody" style={{ flex: 1, overflow: 'auto', display: 'flex', alignItems: 'center', justifyContent: 'center', background: theme === 'dark' ? '#090d16' : '#f8fafc', padding: '32px' }}>
              {mode === 'render' && isMermaid ? (
                <div style={{ width: '100%', height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  {err ? <div className="callout calloutBad">{err}</div> : null}
                  {svg ? <div className={`${diagramSvgClassName} diagramSvgFullscreen`} dangerouslySetInnerHTML={{ __html: svg }} style={{ width: '100%', height: '100%', background: 'transparent', padding: 0 }} /> : <div className="muted">Rendering…</div>}
                </div>
              ) : (
                <pre className="pre preCode" style={{ width: '100%', height: '100%', margin: 0 }}>{diagram.code}</pre>
              )}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
