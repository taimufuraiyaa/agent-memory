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
      htmlLabels: false,
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
        <button className="btn btnGhost" onClick={() => navigator.clipboard.writeText(diagram.code)}>
          Copy
        </button>
      </div>

      {mode === 'render' && isMermaid ? (
        <>
          {err ? <div className="callout calloutBad">{err}</div> : null}
          {svg ? <div className="diagramSvg" dangerouslySetInnerHTML={{ __html: svg }} /> : <div className="muted">Rendering…</div>}
        </>
      ) : (
        <pre className="pre preCode">{diagram.code}</pre>
      )}
    </div>
  )
}
