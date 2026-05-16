import React, { useMemo } from 'react'
import DOMPurify from 'dompurify'
import { marked, type Token } from 'marked'
import { DiagramViewer } from './DiagramViewer'

marked.setOptions({
  gfm: true,
  breaks: false,
})

function sanitize(html: string): string {
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    ADD_ATTR: ['target', 'rel'],
  })
}

export function MarkdownView({ markdown, clamp }: { markdown: string; clamp: boolean }) {
  const tokens = useMemo(() => {
    return marked.lexer(markdown ?? '')
  }, [markdown])

  return (
    <div className={clamp ? 'md mdClamp' : 'md'}>
      {tokens.map((token, i) => {
        if (token.type === 'code' && token.lang === 'mermaid') {
          return <DiagramViewer key={i} diagram={{ lang: 'mermaid', code: token.text }} />
        }
        
        // For other tokens, we render them as HTML
        // Note: marked tokens can be deeply nested, but simple top-level render usually works for typical memory content.
        // If nested rendering is needed, we'd need a recursive component.
        // For now, we use a simple approach: render the token's raw text as markdown.
        const html = sanitize(marked.parser([token]))
        return (
          <div
            key={i}
            className="mdPart"
            dangerouslySetInnerHTML={{ __html: html }}
          />
        )
      })}
    </div>
  )
}
