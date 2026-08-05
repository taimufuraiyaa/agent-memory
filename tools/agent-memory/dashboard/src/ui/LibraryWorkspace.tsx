import { useEffect, useRef, useState } from 'react'
import {
  getLibraryStructure,
  importLibraryBook,
  queryLibrary,
  reviewLibraryMemory,
  type BookMemoryProposal,
  type LibraryImportJob,
  type LibraryPassageResult,
  type LibraryStructuralNode,
} from '../lib/api'

type LibraryKind = 'personal' | 'organization'
type BookFileFormat = 'pdf' | 'epub' | 'markdown' | 'text'
type IndexStatus = 'idle' | 'reading' | 'structuring' | 'ready' | 'failed'

type LibraryWorkspaceProps = {
  workspace: string
}

const bookLanguageOptions = [
  { value: 'en', label: 'English' },
  { value: 'vi', label: 'Tiếng Việt' },
  { value: 'zh-Hans', label: '中文（简体）' },
  { value: 'zh-Hant', label: '中文（繁體）' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
  { value: 'es', label: 'Español' },
  { value: 'fr', label: 'Français' },
  { value: 'de', label: 'Deutsch' },
  { value: 'pt', label: 'Português' },
  { value: 'ru', label: 'Русский' },
  { value: 'ar', label: 'العربية' },
  { value: 'hi', label: 'हिन्दी' },
  { value: 'th', label: 'ไทย' },
  { value: 'id', label: 'Bahasa Indonesia' },
]

export function LibraryWorkspace({ workspace }: LibraryWorkspaceProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [libraryKind, setLibraryKind] = useState<LibraryKind>('personal')
  const [organizationID, setOrganizationID] = useState('')
  const [title, setTitle] = useState('')
  const [editionLabel, setEditionLabel] = useState('Imported edition')
  const [language, setLanguage] = useState('en')
  const [source, setSource] = useState('')
  const [sourceFile, setSourceFile] = useState<File | null>(null)
  const [sourceFormat, setSourceFormat] = useState<BookFileFormat>('markdown')
  const [job, setJob] = useState<LibraryImportJob | null>(null)
  const [nodes, setNodes] = useState<LibraryStructuralNode[]>([])
  const [indexStatus, setIndexStatus] = useState<IndexStatus>('idle')
  const [question, setQuestion] = useState('')
  const [lastQuestion, setLastQuestion] = useState('')
  const [interpretation, setInterpretation] = useState('')
  const [evidence, setEvidence] = useState<LibraryPassageResult[]>([])
  const [proposal, setProposal] = useState<BookMemoryProposal | null>(null)
  const [queried, setQueried] = useState(false)
  const [busy, setBusy] = useState<'import' | 'query' | 'review' | 'memory' | ''>('')
  const [error, setError] = useState('')

  useEffect(() => {
    resetBook()
  }, [workspace])

  function resetBook() {
    setTitle('')
    setEditionLabel('Imported edition')
    setLanguage('en')
    setSource('')
    setSourceFile(null)
    setSourceFormat('markdown')
    setJob(null)
    setNodes([])
    setIndexStatus('idle')
    setQuestion('')
    setLastQuestion('')
    setInterpretation('')
    setEvidence([])
    setProposal(null)
    setQueried(false)
    setError('')
  }

  function validateScope() {
    if (!workspace) return 'Select a project workspace first.'
    if (libraryKind === 'organization' && !organizationID.trim()) return 'Organization ID is required for an organization library.'
    return ''
  }

  function selectBook(file: File) {
    const format = bookFormatForFile(file.name)
    if (!format) {
      setError('Unsupported file. Choose PDF, EPUB, Markdown, or plain text.')
      return
    }
    setSourceFile(file)
    setSourceFormat(format)
    setTitle(file.name.replace(/\.(pdf|epub|md|markdown|txt)$/i, ''))
    setError('')
    if (format === 'markdown' || format === 'text') {
      void file.text().then(setSource).catch((reason: unknown) => setError(messageOf(reason)))
    } else {
      setSource('')
    }
  }

  async function importBook() {
    const scopeError = validateScope()
    if (scopeError || !title.trim() || !editionLabel.trim() || !language.trim() || (!sourceFile && !source.trim())) {
      setError(scopeError || 'Check the title, edition, language, and selected book before continuing.')
      return
    }
    setBusy('import')
    setIndexStatus('reading')
    setError('')
    try {
      const metadata = {
        workspace,
        library_kind: libraryKind,
        organization_id: libraryKind === 'organization' ? organizationID.trim() : undefined,
        title: title.trim(),
        edition_label: editionLabel.trim(),
        language: language.trim(),
      }
      const imported = sourceFile
        ? await importLibraryBook({ ...metadata, format: sourceFormat, source_file: sourceFile })
        : await importLibraryBook({ ...metadata, format: sourceFormat === 'text' ? 'text' : 'markdown', markdown: source })
      setJob(imported)
      setProposal(null)
      setEvidence([])
      setQueried(false)
      setIndexStatus('structuring')
      if (imported.result?.edition_id) {
        const structure = await getLibraryStructure({ workspace, edition_id: imported.result.edition_id })
        setNodes([...structure.nodes].sort((left, right) => left.ordinal - right.ordinal))
      }
      setIndexStatus('ready')
    } catch (reason) {
      setIndexStatus('failed')
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  async function askBook() {
    const scopeError = validateScope()
    const prompt = question.trim()
    if (scopeError || !prompt) {
      setError(scopeError || 'Write a question for this book.')
      return
    }
    setBusy('query')
    setError('')
    setLastQuestion(prompt)
    setQuestion('')
    try {
      const response = await queryLibrary({
        workspace,
        organization_ids: libraryKind === 'organization' ? [organizationID.trim()] : undefined,
        question: prompt,
        limit: 8,
      })
      setEvidence(response.results ?? [])
      setProposal(null)
      setQueried(true)
    } catch (reason) {
      setEvidence([])
      setQueried(true)
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  async function suggestMemory() {
    if (!lastQuestion || !interpretation.trim()) {
      setError('Write your interpretation before suggesting a memory.')
      return
    }
    setBusy('memory')
    setError('')
    try {
      const response = await queryLibrary({
        workspace,
        organization_ids: libraryKind === 'organization' ? [organizationID.trim()] : undefined,
        question: lastQuestion,
        limit: 8,
        propose_memory: true,
        memory_content: interpretation.trim(),
      })
      setEvidence(response.results ?? evidence)
      setProposal(response.proposal ?? null)
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  async function reviewProposal(decision: 'accept' | 'reject') {
    if (!proposal) return
    setBusy('review')
    setError('')
    try {
      setProposal(await reviewLibraryMemory({ workspace, proposal_id: proposal.id, decision }))
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  const hasSelectedSource = Boolean(sourceFile || source.trim())
  const isIndexing = busy === 'import'
  const isReady = Boolean(job?.result) && indexStatus === 'ready'
  const backgroundIndexStatus = indexStatus === 'structuring' ? 'Building the book index' : 'Reading and indexing your book'

  return (
    <section className={`libraryWorkspace ${isReady ? 'isReading' : ''}`}>
      <input ref={fileInputRef} className="libraryHiddenFile" type="file" accept=".pdf,.epub,.md,.markdown,.txt,application/pdf,application/epub+zip,text/markdown,text/plain" onChange={(event) => {
        const file = event.target.files?.[0]
        if (file) selectBook(file)
      }} />

      {!hasSelectedSource && !job ? (
        <section className="libraryEmptyState">
          <span className="libraryMonogram" aria-hidden="true">Aa</span>
          <p className="eyebrow">Your reading room</p>
          <h1>Bring a book. Ask better questions.</h1>
          <p>Import a complete book and agent-memory will make its chapters and cited passages available for conversation.</p>
          <button className="primaryNotebookButton libraryImportButton" type="button" onClick={() => fileInputRef.current?.click()}>Import a book</button>
          <small>PDF · EPUB · Markdown · plain text</small>
          <details className="libraryPasteOption">
            <summary>Paste Markdown or plain text</summary>
            <textarea value={source} onChange={(event) => {
              setSource(event.target.value)
              setSourceFile(null)
              setSourceFormat('markdown')
              if (!title) setTitle('Untitled book')
            }} placeholder="# Chapter 1\n\nPaste the complete book here…" />
          </details>
        </section>
      ) : null}

      {hasSelectedSource && !job && !isIndexing ? (
        <section className="libraryImportSheet">
          <header>
            <button className="libraryBackButton" type="button" onClick={resetBook}>← Library</button>
            <p className="eyebrow">Review before importing</p>
            <h1>Make sure this book looks right.</h1>
          </header>
          <div className="librarySelectedBook">
            <div className="libraryBookGlyph" aria-hidden="true">{sourceFormat === 'markdown' ? 'MD' : sourceFormat.toUpperCase()}</div>
            <div><strong>{sourceFile?.name ?? 'Pasted manuscript'}</strong><span>{sourceFormat.toUpperCase()} · {sourceFile ? formatBytes(sourceFile.size) : `${source.length.toLocaleString()} characters`}</span></div>
            <button type="button" onClick={() => fileInputRef.current?.click()}>Replace</button>
          </div>
          <div className="libraryMetadataGrid">
            <label>Book title<input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="Book title" /></label>
            <label>Edition<input value={editionLabel} onChange={(event) => setEditionLabel(event.target.value)} /></label>
            <label>Language<select value={language} onChange={(event) => setLanguage(event.target.value)}>{bookLanguageOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>
            <label>Library<select value={libraryKind} onChange={(event) => setLibraryKind(event.target.value as LibraryKind)}><option value="personal">Personal library</option><option value="organization">Organization library</option></select></label>
            {libraryKind === 'organization' ? <label>Organization ID<input value={organizationID} onChange={(event) => setOrganizationID(event.target.value)} /></label> : null}
          </div>
          <footer><p>Indexing happens in the background. The original source stays separate from any memory you choose to keep.</p><button className="primaryNotebookButton" type="button" onClick={() => void importBook()}>Start reading</button></footer>
        </section>
      ) : null}

      {isIndexing ? (
        <section className="libraryIndexingState" aria-live="polite">
          <div className="libraryIndexingBook" aria-hidden="true"><span /><span /><span /></div>
          <div><span className="libraryStatusDot" /><p className="eyebrow">Working in the background</p><h1>{backgroundIndexStatus}</h1><p>{title} will open here as soon as its chapters and citable passages are ready.</p></div>
        </section>
      ) : null}

      {isReady && job?.result ? (
        <section className="libraryReadingRoom">
          <header className="libraryBookHeader">
            <div><p className="eyebrow">Now reading</p><h1>{title}</h1><p>{editionLabel} · {languageLabel(language)} · {job.result.format.toUpperCase()}</p></div>
            <div className="libraryBookActions"><span className="libraryReadyStatus"><i /> Ready to ask</span><button type="button" onClick={resetBook}>Import another</button></div>
          </header>

          <div className="libraryReaderGrid">
            <main className="libraryBookBody">
              <section className="libraryConversation" aria-live="polite">
                {!queried ? <div className="libraryReaderWelcome"><span aria-hidden="true">“</span><h2>What do you want to understand?</h2><p>Ask about an argument, a chapter, or a quote you remember. Answers stay attached to passages from this book.</p></div> : null}
                {lastQuestion ? <article className="libraryMessage reader"><small>You</small><p>{lastQuestion}</p></article> : null}
                {queried ? <article className="libraryMessage book"><header><span className="libraryBookAvatar">Aa</span><div><strong>{title}</strong><small>Grounded passages</small></div></header>{evidence.length ? evidence.map((result) => <blockquote key={result.passage.id}><p>{result.passage.text}</p><footer>{result.passage.locator.display}<span>Relevance {formatScore(result.score)}</span></footer></blockquote>) : <p className="libraryEmpty">I couldn’t find a passage in this book that supports an answer. Try asking with different words or name a chapter.</p>}</article> : null}
                {queried && evidence.length ? <details className="libraryInsight"><summary>Keep your interpretation as memory</summary><label>Your interpretation<textarea value={interpretation} onChange={(event) => setInterpretation(event.target.value)} placeholder="What does this mean to you?" /></label><button type="button" onClick={() => void suggestMemory()} disabled={busy !== ''}>{busy === 'memory' ? 'Preparing…' : 'Suggest memory'}</button></details> : null}
                {proposal ? <div className="libraryProposal"><div><span className="sourceBadge agent">Suggested memory</span><strong>{proposal.status}</strong></div><p>{proposal.content}</p>{proposal.citations?.map((citation) => <small key={citation.id}>{citation.locator.display}</small>)}{proposal.status === 'suggested' ? <div><button className="primaryNotebookButton" type="button" onClick={() => void reviewProposal('accept')} disabled={busy !== ''}>Accept</button><button type="button" onClick={() => void reviewProposal('reject')} disabled={busy !== ''}>Reject</button></div> : <p className="libraryReviewStatus">Review complete · {proposal.status}</p>}</div> : null}
              </section>

              <form className="libraryChatComposer" onSubmit={(event) => { event.preventDefault(); void askBook() }}>
                <textarea value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="Ask this book anything…" aria-label="Ask this book" onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault()
                    void askBook()
                  }
                }} />
                <button type="submit" disabled={busy !== '' || !question.trim()} aria-label="Send question">↑</button>
                <small>Enter to send · Shift + Enter for a new line</small>
              </form>
            </main>

            <aside className="libraryBookIndex" aria-label="Book contents">
              <header><span>Contents</span><small>{nodes.length} sections</small></header>
              {nodes.length ? <ol>{nodes.map((node) => <li key={node.id}><button type="button"><span>{String(node.ordinal + 1).padStart(2, '0')}</span><div><strong>{node.title}</strong><small>{friendlyNodeKind(node.kind)}</small></div></button></li>)}</ol> : <p>The book has no visible chapter structure yet.</p>}
              <details className="libraryTechnicalDetails"><summary>Book details</summary><Fact label="Work" value={job.result.work_id} /><Fact label="Edition" value={job.result.edition_id} /><Fact label="Source" value={job.result.asset_id} /><Fact label="Indexed" value={`${job.result.node_count} sections · ${job.result.passage_count ?? '—'} passages`} /></details>
            </aside>
          </div>
        </section>
      ) : null}

      {indexStatus === 'failed' && hasSelectedSource ? <section className="libraryImportError"><strong>We couldn’t prepare this book.</strong><p>{error}</p><div><button className="primaryNotebookButton" type="button" onClick={() => void importBook()}>Try again</button><button type="button" onClick={resetBook}>Choose another book</button></div></section> : null}
      {error && indexStatus !== 'failed' ? <div className="notebookError libraryError" role="alert">{error}</div> : null}
    </section>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong title={value}>{value}</strong></div>
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}

function bookFormatForFile(filename: string): BookFileFormat | null {
  const extension = filename.toLowerCase().split('.').pop()
  if (extension === 'pdf' || extension === 'epub') return extension
  if (extension === 'md' || extension === 'markdown') return 'markdown'
  if (extension === 'txt') return 'text'
  return null
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

function formatScore(score: number) {
  return Number.isFinite(score) ? score.toFixed(2) : '—'
}

function friendlyNodeKind(kind: string) {
  return kind.replace(/[_-]+/g, ' ').replace(/^./, (letter) => letter.toUpperCase())
}

function languageLabel(value: string) {
  return bookLanguageOptions.find((option) => option.value === value)?.label ?? value
}
