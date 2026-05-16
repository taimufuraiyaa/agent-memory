import React, { useEffect, useId, useMemo, useState } from 'react'
import DOMPurify from 'dompurify'
import mermaid from 'mermaid'
import type { Diagram } from '../lib/api'

let mermaidReady = false

function ensureMermaid() {
  if (mermaidReady) return
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: 'dark',
  })
  mermaidReady = true
}

export function DiagramViewer({ diagram }: { diagram: Diagram }) {
  const [mode, setMode] = useState<'render' | 'code'>(diagram.lang === 'mermaid' ? 'render' : 'code')
  const [svg, setSvg] = useState<string>('')
  const [err, setErr] = useState<string>('')
  const rid = useId()

  const isMermaid = useMemo(() => diagram.lang.trim().toLowerCase() === 'mermaid', [diagram.lang])

  useEffect(() => {
    if (!isMermaid || mode !== 'render') return
    ensureMermaid()
    let cancelled = false
    setErr('')
    setSvg('')
    const id = `m-${rid.replace(/[:]/g, '')}`
    mermaid
      .render(id, diagram.code)
      .then((r: { svg: string }) => {
        if (cancelled) return
        const clean = DOMPurify.sanitize(r.svg, {
          USE_PROFILES: { svg: true, svgFilters: true },
        })
        setSvg(clean)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setErr(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [diagram.code, isMermaid, mode, rid])

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
