import React, { useMemo } from 'react'
import DOMPurify from 'dompurify'
import { marked } from 'marked'

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
  const html = useMemo(() => {
    const rendered = marked.parse(markdown ?? '', { async: false }) as string
    return sanitize(rendered)
  }, [markdown])

  return (
    <div
      className={clamp ? 'md mdClamp' : 'md'}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
