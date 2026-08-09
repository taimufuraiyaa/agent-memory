import { useEffect, useRef, useState } from 'react'
import {
  getLibraryLocalLLMStatus,
  getLibraryStructure,
  importLibraryBook,
  queryLibrary,
  reviewLibraryMemory,
  saveLibraryLocalLLM,
  testLibraryLocalLLM,
  type BookMemoryProposal,
  type LibraryImportJob,
  type LibraryImportResult,
  type LibraryPassageResult,
  type LibraryStructuralNode,
  type NoteDocument,
  type RightsBasis,
  type LocalLLMConfig,
  type LocalLLMStatus,
} from '../lib/api'

type LibraryKind = 'personal' | 'organization'
type BookFileFormat = 'pdf' | 'epub' | 'markdown' | 'text'
type IndexStatus = 'idle' | 'reading' | 'structuring' | 'ready' | 'failed'

export type ImportedBookNoteInput = {
  title: string
  editionLabel: string
  language: string
  sourceName: string
  result: LibraryImportResult
  nodeCount: number
}

type LibraryWorkspaceProps = {
  workspace: string
  onBookImported: (book: ImportedBookNoteInput) => Promise<NoteDocument>
  onOpenBookNote: (noteID: string) => void
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
const importModeStorageKey = 'agent-memory:library-import-mode'

function savedPreference(key: string, fallback: string) {
  if (typeof window === 'undefined') return fallback
  return window.localStorage.getItem(key) || fallback
}

const rightsBasisOptions: Array<{ value: RightsBasis; label: string }> = [
  { value: 'lawfully_acquired_private_use', label: 'Lawfully acquired copy for private use' },
  { value: 'author_owned', label: 'I am the author or rights holder' },
  { value: 'licensed', label: 'I have a licence or permission' },
  { value: 'public_domain', label: 'Public domain or open licence' },
]

export function LibraryWorkspace({ workspace, onBookImported, onOpenBookNote }: LibraryWorkspaceProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [libraryKind, setLibraryKind] = useState<LibraryKind>('personal')
  const [organizationID, setOrganizationID] = useState('')
  const [title, setTitle] = useState('')
  const [editionLabel, setEditionLabel] = useState('Imported edition')
  const [language, setLanguage] = useState('en')
  const [rightsBasis, setRightsBasis] = useState<RightsBasis>('lawfully_acquired_private_use')
  const [source, setSource] = useState('')
  const [sourceFile, setSourceFile] = useState<File | null>(null)
  const [sourceFormat, setSourceFormat] = useState<BookFileFormat>('markdown')
  const [job, setJob] = useState<LibraryImportJob | null>(null)
  const [importedNote, setImportedNote] = useState<NoteDocument | null>(null)
  const [nodes, setNodes] = useState<LibraryStructuralNode[]>([])
  const [indexStatus, setIndexStatus] = useState<IndexStatus>('idle')
  const [question, setQuestion] = useState('')
  const [lastQuestion, setLastQuestion] = useState('')
  const [interpretation, setInterpretation] = useState('')
  const [evidence, setEvidence] = useState<LibraryPassageResult[]>([])
  const [proposal, setProposal] = useState<BookMemoryProposal | null>(null)
  const [queried, setQueried] = useState(false)
  const [busy, setBusy] = useState<'import' | 'query' | 'review' | 'memory' | 'llm' | ''>('')
  const [error, setError] = useState('')
  const [localLLMStatus, setLocalLLMStatus] = useState<LocalLLMStatus | null>(null)
  const [localLLMNotice, setLocalLLMNotice] = useState('')
  const [importDecisionOpen, setImportDecisionOpen] = useState(false)
  const [setupOpen, setSetupOpen] = useState(false)
  const [rememberParserOnly, setRememberParserOnly] = useState(false)
  const [parserOnlyRemembered, setParserOnlyRemembered] = useState(() => savedPreference(importModeStorageKey, '') === 'parser')
  const [localLLMConfig, setLocalLLMConfig] = useState<LocalLLMConfig>({
    enabled: true,
    base_url: 'http://127.0.0.1:11434/v1',
    text_model: '',
    vision_model: '',
    api_key: '',
    timeout_seconds: 3,
  })

  useEffect(() => {
    let active = true
    void getLibraryLocalLLMStatus().then((status) => {
      if (!active) return
      setLocalLLMStatus(status)
      if (status.configured) {
        setLocalLLMConfig({
          enabled: status.config.enabled,
          base_url: status.config.base_url,
          text_model: status.config.text_model,
          vision_model: status.config.vision_model ?? '',
          api_key: '',
          timeout_seconds: status.config.timeout_seconds,
        })
      }
    }).catch((reason: unknown) => {
      if (active) setLocalLLMNotice(`Local endpoint status unavailable: ${messageOf(reason)}. The built-in parser remains available.`)
    })
    return () => { active = false }
  }, [])

  useEffect(() => {
    resetBook()
  }, [workspace])

  function resetBook() {
    setTitle('')
    setEditionLabel('Imported edition')
    setLanguage('en')
    setRightsBasis('lawfully_acquired_private_use')
    setSource('')
    setSourceFile(null)
    setSourceFormat('markdown')
    setJob(null)
    setImportedNote(null)
    setNodes([])
    setIndexStatus('idle')
    setQuestion('')
    setLastQuestion('')
    setInterpretation('')
    setEvidence([])
    setProposal(null)
    setQueried(false)
    setError('')
    setImportDecisionOpen(false)
    setSetupOpen(false)
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

  function validateImport() {
    const scopeError = validateScope()
    if (scopeError || !title.trim() || !editionLabel.trim() || !language.trim() || (!sourceFile && !source.trim())) {
      return scopeError || 'Check the title, edition, language, and selected book before continuing.'
    }
    return ''
  }

  async function beginImport() {
    const validationError = validateImport()
    if (validationError) {
      setError(validationError)
      return
    }
    const localReady = Boolean(localLLMStatus?.enabled && localLLMStatus.reachable && localLLMStatus.text_model_available)
    if (!localReady && !parserOnlyRemembered) {
      setError('')
      setImportDecisionOpen(true)
      return
    }
    await importBook()
  }

  async function importBook() {
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
        rights_basis: rightsBasis,
      }
      const imported = sourceFile
        ? await importLibraryBook({ ...metadata, format: sourceFormat, source_file: sourceFile })
        : await importLibraryBook({ ...metadata, format: sourceFormat === 'text' ? 'text' : 'markdown', markdown: source })
      setJob(imported)
      setProposal(null)
      setEvidence([])
      setQueried(false)
      setIndexStatus('structuring')
      let importedNodes: LibraryStructuralNode[] = []
      if (imported.result?.edition_id) {
        const structure = await getLibraryStructure({ workspace, edition_id: imported.result.edition_id })
        importedNodes = [...structure.nodes].sort((left, right) => left.ordinal - right.ordinal)
        setNodes(importedNodes)
      }
      setIndexStatus('ready')
      if (imported.result) {
        try {
          const createdNote = await onBookImported({
            title: title.trim(),
            editionLabel: editionLabel.trim(),
            language,
            sourceName: sourceFile?.name ?? 'Pasted manuscript',
            result: imported.result,
            nodeCount: importedNodes.length,
          })
          setImportedNote(createdNote)
        } catch (noteReason) {
          setError(`Book imported, but its note could not be created: ${messageOf(noteReason)}`)
        }
      }
    } catch (reason) {
      setIndexStatus('failed')
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  async function continueWithParser() {
    if (rememberParserOnly) {
      window.localStorage.setItem(importModeStorageKey, 'parser')
      setParserOnlyRemembered(true)
    }
    setImportDecisionOpen(false)
    setSetupOpen(false)
    await importBook()
  }

  async function checkLocalLLM(save: boolean) {
    setBusy('llm')
    setError('')
    setLocalLLMNotice('')
    try {
      const status = save ? await saveLibraryLocalLLM(localLLMConfig) : await testLibraryLocalLLM(localLLMConfig)
      setLocalLLMStatus(status)
      if (status.reachable && status.text_model_available) {
        setLocalLLMNotice(save ? 'Setup saved. Local endpoint is reachable and the text model is available.' : 'Connection succeeded and the text model is available.')
      } else {
        setLocalLLMNotice(status.error || 'The endpoint responded, but the configured text model is unavailable.')
      }
    } catch (reason) {
      setLocalLLMNotice(messageOf(reason))
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
            <label>Rights basis<select value={rightsBasis} onChange={(event) => setRightsBasis(event.target.value as RightsBasis)}>{rightsBasisOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>
            <label>Library<select value={libraryKind} onChange={(event) => setLibraryKind(event.target.value as LibraryKind)}><option value="personal">Personal library</option><option value="organization">Organization library</option></select></label>
            {libraryKind === 'organization' ? <label>Organization ID<input value={organizationID} onChange={(event) => setOrganizationID(event.target.value)} /></label> : null}
          </div>
          <div className="libraryPlanes"><div><strong>Built-in parser</strong><span>Always available · citation baseline</span></div><div><strong>Local endpoint</strong><span>{localLLMStatus?.reachable && localLLMStatus.text_model_available ? 'Connected' : 'Optional · not operational'}</span></div><div><strong>OCR</strong><span>Separate processing stage</span></div></div>
          {parserOnlyRemembered ? <p className="notebookHint">Parser-only choice remembered on this device. <button type="button" onClick={() => {
            window.localStorage.removeItem(importModeStorageKey)
            setParserOnlyRemembered(false)
          }}>Ask again</button></p> : null}
          <footer><p>Indexing happens in the background. The original source stays separate from any memory you choose to keep.</p><button className="primaryNotebookButton" type="button" onClick={() => void beginImport()}>Start reading</button></footer>

          {importDecisionOpen ? <div className="libraryProposal" role="dialog" aria-label="Choose book processing mode">
            <div><span className="sourceBadge agent">Optional local processing</span><strong>Decision required</strong></div>
            <h3>No operational local LLM is configured</h3>
            <p>Set up an OpenAI-compatible local endpoint, or continue with the built-in parser. The deterministic parser still creates citations and remains the source of truth.</p>
            <p>Scanned PDFs still require the OCR processing stage; parser-only import cannot invent text for image-only pages.</p>
            {localLLMStatus?.error ? <p className="notebookHint">Endpoint status: {localLLMStatus.error}</p> : null}
            <label><input type="checkbox" checked={rememberParserOnly} onChange={(event) => setRememberParserOnly(event.target.checked)} /> Remember parser-only choice on this device</label>
            <div>
              <button className="primaryNotebookButton" type="button" onClick={() => setSetupOpen(true)} disabled={busy !== ''}>Set up local LLM</button>
              <button type="button" onClick={() => void continueWithParser()} disabled={busy !== ''}>Continue with built-in parser</button>
              <button type="button" onClick={() => { setImportDecisionOpen(false); setSetupOpen(false) }} disabled={busy !== ''}>Cancel import</button>
            </div>
          </div> : null}

          {setupOpen ? <div className="libraryProposal" aria-label="Local LLM setup">
            <div><span className="sourceBadge agent">Local setup</span><strong>OpenAI-compatible</strong></div>
            <h3>Connect a local model server</h3>
            <p>Use an OpenAI-compatible local endpoint from Ollama, LM Studio, vLLM, or an equivalent server. Initial setup accepts loopback addresses only.</p>
            <div className="libraryScopeGrid">
              <label>Base URL<input value={localLLMConfig.base_url} onChange={(event) => setLocalLLMConfig({ ...localLLMConfig, base_url: event.target.value })} placeholder="http://127.0.0.1:11434/v1" /></label>
              <label>Text model<input value={localLLMConfig.text_model} onChange={(event) => setLocalLLMConfig({ ...localLLMConfig, text_model: event.target.value })} placeholder="Local model ID" /></label>
              <label>Vision model <span className="optionalLabel">optional</span><input value={localLLMConfig.vision_model ?? ''} onChange={(event) => setLocalLLMConfig({ ...localLLMConfig, vision_model: event.target.value })} placeholder="Vision model ID" /></label>
              <label>API key <span className="optionalLabel">write-only</span><input type="password" value={localLLMConfig.api_key ?? ''} onChange={(event) => setLocalLLMConfig({ ...localLLMConfig, api_key: event.target.value })} placeholder={localLLMStatus?.config.api_key_configured ? 'Stored key unchanged when blank' : 'Optional'} /></label>
            </div>
            <p className="notebookHint">A successful test proves connectivity only. Local summary and OCR jobs will report their own processing provenance when those stages run.</p>
            {localLLMNotice ? <p className="notebookHint" role="status">{localLLMNotice}</p> : null}
            <div><button className="primaryNotebookButton" type="button" onClick={() => void checkLocalLLM(false)} disabled={busy !== ''}>{busy === 'llm' ? 'Checking…' : 'Test connection'}</button><button type="button" onClick={() => void checkLocalLLM(true)} disabled={busy !== ''}>Save setup</button><button type="button" onClick={() => setSetupOpen(false)} disabled={busy !== ''}>Back</button></div>
          </div> : null}
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
            <div className="libraryBookActions"><span className="libraryReadyStatus"><i /> {importedNote ? 'Note created' : 'Ready to ask'}</span>{importedNote ? <button type="button" onClick={() => onOpenBookNote(importedNote.id)}>Open note</button> : null}<button type="button" onClick={resetBook}>Import another</button></div>
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
