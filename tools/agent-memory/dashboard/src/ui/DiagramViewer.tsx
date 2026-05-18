import React, { useEffect, useId, useMemo, useState } from 'react'
import DOMPurify from 'dompurify'
import mermaid from 'mermaid'
import type { Diagram } from '../lib/api'

function ensureMermaid(theme: 'light' | 'dark') {
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'loose',
    theme: theme === 'dark' ? 'dark' : 'default',
    fontFamily: 'Inter, system-ui, sans-serif',
    flowchart: {
      htmlLabels: false,
      useMaxWidth: true,
    },
    sequence: {
      useMaxWidth: true,
    },
    themeVariables: {
      nodeTextColor: theme === 'dark' ? '#f8fafc' : '#1a1b1e',
      primaryTextColor: theme === 'dark' ? '#f8fafc' : '#1a1b1e',
      textColor: theme === 'dark' ? '#f8fafc' : '#1a1b1e',
      mainBkg: theme === 'dark' ? '#0f172a' : '#ffffff',
    },
  })
}

export function DiagramViewer({ diagram, theme }: { diagram: Diagram; theme: 'light' | 'dark' }) {
  const [mode, setMode] = useState<'render' | 'code'>(diagram.lang === 'mermaid' ? 'render' : 'code')
  const [svg, setSvg] = useState<string>('')
  const [err, setErr] = useState<string>('')
  const [fullScreen, setFullScreen] = useState<boolean>(false)
  const rid = useId()

  const isMermaid = useMemo(() => diagram.lang.trim().toLowerCase() === 'mermaid', [diagram.lang])

  useEffect(() => {
    if (!isMermaid || mode !== 'render') return
    ensureMermaid(theme)
    let cancelled = false
    setErr('')
    setSvg('')
    const id = `m-${rid.replace(/[:]/g, '')}`
    
    // Use a temporary container to help Mermaid calculate dimensions/styles
    const container = document.createElement('div')
    container.style.visibility = 'hidden'
    container.style.position = 'absolute'
    document.body.appendChild(container)

    mermaid
      .render(id, diagram.code, container)
      .then((r: { svg: string }) => {
        if (cancelled) return
        setSvg(r.svg)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setErr(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        document.body.removeChild(container)
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
          {svg ? <div className="diagramSvg" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(svg) }} /> : <div className="muted">Rendering…</div>}
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
                  {svg ? <div className="diagramSvg diagramSvgFullscreen" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(svg) }} style={{ width: '100%', height: '100%', background: 'transparent', padding: 0 }} /> : <div className="muted">Rendering…</div>}
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
