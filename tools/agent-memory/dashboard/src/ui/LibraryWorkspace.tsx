import { useEffect, useState } from 'react'
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

type LibraryWorkspaceProps = {
  workspace: string
}

const principalStorageKey = 'agent-memory:library-principal'
const libraryStorageKey = 'agent-memory:library-id'

function savedPreference(key: string, fallback: string) {
  if (typeof window === 'undefined') return fallback
  return window.localStorage.getItem(key) || fallback
}

export function LibraryWorkspace({ workspace }: LibraryWorkspaceProps) {
  const [principalID, setPrincipalID] = useState(() => savedPreference(principalStorageKey, 'local-reader'))
  const [libraryID, setLibraryID] = useState(() => savedPreference(libraryStorageKey, 'personal-library'))
  const [libraryKind, setLibraryKind] = useState<LibraryKind>('personal')
  const [organizationID, setOrganizationID] = useState('')
  const [title, setTitle] = useState('')
  const [editionLabel, setEditionLabel] = useState('Imported edition')
  const [language, setLanguage] = useState('en')
  const [source, setSource] = useState('')
  const [job, setJob] = useState<LibraryImportJob | null>(null)
  const [nodes, setNodes] = useState<LibraryStructuralNode[]>([])
  const [rememberedStatement, setRememberedStatement] = useState('')
  const [question, setQuestion] = useState('')
  const [interpretation, setInterpretation] = useState('')
  const [evidence, setEvidence] = useState<LibraryPassageResult[]>([])
  const [proposal, setProposal] = useState<BookMemoryProposal | null>(null)
  const [queried, setQueried] = useState(false)
  const [busy, setBusy] = useState<'import' | 'query' | 'review' | ''>('')
  const [error, setError] = useState('')

  useEffect(() => {
    setJob(null)
    setNodes([])
    setEvidence([])
    setProposal(null)
    setQueried(false)
    setError('')
  }, [workspace])

  function persistScope() {
    window.localStorage.setItem(principalStorageKey, principalID.trim())
    window.localStorage.setItem(libraryStorageKey, libraryID.trim())
  }

  function validateScope() {
    if (!workspace) return 'Select a project workspace first.'
    if (!principalID.trim() || !libraryID.trim()) return 'Reader ID and library ID are required.'
    if (libraryKind === 'organization' && !organizationID.trim()) return 'Organization ID is required for an organization library.'
    return ''
  }

  async function importBook() {
    const scopeError = validateScope()
    if (scopeError || !title.trim() || !editionLabel.trim() || !language.trim() || !source.trim()) {
      setError(scopeError || 'Title, edition, language, and complete source text are required.')
      return
    }
    setBusy('import')
    setError('')
    try {
      persistScope()
      const imported = await importLibraryBook({
        workspace,
        library_id: libraryID.trim(),
        library_kind: libraryKind,
        organization_id: libraryKind === 'organization' ? organizationID.trim() : undefined,
        principal_id: principalID.trim(),
        title: title.trim(),
        edition_label: editionLabel.trim(),
        language: language.trim(),
        markdown: source,
      })
      setJob(imported)
      setProposal(null)
      setEvidence([])
      setQueried(false)
      if (imported.result?.edition_id) {
        const structure = await getLibraryStructure({
          workspace,
          principal_id: principalID.trim(),
          edition_id: imported.result.edition_id,
        })
        setNodes([...structure.nodes].sort((left, right) => left.ordinal - right.ordinal))
      }
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  async function askBook() {
    const scopeError = validateScope()
    const prompt = [rememberedStatement.trim(), question.trim()].filter(Boolean).join('\n\n')
    if (scopeError || !prompt) {
      setError(scopeError || 'Write a remembered statement, a question, or both.')
      return
    }
    setBusy('query')
    setError('')
    try {
      persistScope()
      const response = await queryLibrary({
        workspace,
        principal_id: principalID.trim(),
        organization_ids: libraryKind === 'organization' ? [organizationID.trim()] : undefined,
        question: prompt,
        limit: 8,
        propose_memory: Boolean(interpretation.trim()),
        memory_content: interpretation.trim() || undefined,
      })
      setEvidence(response.results ?? [])
      setProposal(response.proposal ?? null)
      setQueried(true)
    } catch (reason) {
      setEvidence([])
      setProposal(null)
      setQueried(true)
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
      const reviewed = await reviewLibraryMemory({
        workspace,
        proposal_id: proposal.id,
        principal_id: principalID.trim(),
        decision,
      })
      setProposal(reviewed)
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setBusy('')
    }
  }

  return (
    <section className="libraryWorkspace">
      <header className="libraryHero">
        <p className="eyebrow">Living knowledge library</p>
        <h1>Read the source. Keep the lineage.</h1>
        <p>Import the complete book for retrieval, then retain only cited quotes, summaries, and your reviewed interpretations.</p>
      </header>

      <section className="libraryCard">
        <div className="libraryCardHeader"><div><span>1</span><h2>Import a whole book</h2></div><small>Markdown / plain text</small></div>
        <div className="libraryScopeGrid">
          <label>Reader ID<input value={principalID} onChange={(event) => setPrincipalID(event.target.value)} /></label>
          <label>Library ID<input value={libraryID} onChange={(event) => setLibraryID(event.target.value)} /></label>
          <label>Library type<select value={libraryKind} onChange={(event) => setLibraryKind(event.target.value as LibraryKind)}><option value="personal">Personal</option><option value="organization">Organization</option></select></label>
          {libraryKind === 'organization' ? <label>Organization ID<input value={organizationID} onChange={(event) => setOrganizationID(event.target.value)} /></label> : null}
        </div>
        <div className="libraryScopeGrid">
          <label>Title<input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="Book title" /></label>
          <label>Edition<input value={editionLabel} onChange={(event) => setEditionLabel(event.target.value)} /></label>
          <label>Language<input value={language} onChange={(event) => setLanguage(event.target.value)} /></label>
          <label className="libraryFile">Source file<input type="file" accept=".md,.markdown,.txt,text/markdown,text/plain" onChange={(event) => {
            const file = event.target.files?.[0]
            if (!file) return
            void file.text().then((text) => {
              setSource(text)
              if (!title.trim()) setTitle(file.name.replace(/\.(md|markdown|txt)$/i, ''))
            }).catch((reason: unknown) => setError(messageOf(reason)))
          }} /></label>
        </div>
        <label>Complete source<textarea className="librarySource" value={source} onChange={(event) => setSource(event.target.value)} placeholder="# Chapter 1\n\nPaste the complete Markdown or plain-text book here…" /></label>
        <button className="primaryNotebookButton" type="button" onClick={() => void importBook()} disabled={busy !== ''}>{busy === 'import' ? 'Reading and indexing…' : 'Import and index book'}</button>
      </section>

      <section className="libraryCard">
        <div className="libraryCardHeader"><div><span>2</span><h2>Book contents and index</h2></div><small>{job?.result?.existing ? 'Existing edition reused' : job ? 'New edition indexed' : 'Waiting for import'}</small></div>
        {job?.result ? <div className="libraryFacts"><Fact label="Work" value={job.result.work_id} /><Fact label="Edition" value={job.result.edition_id} /><Fact label="Source asset" value={job.result.asset_id} /><Fact label="Contents" value={`${job.result.node_count} nodes · ${job.result.passage_count ?? '—'} passages`} /></div> : <p className="notebookHint">The agent reads hierarchically: source → structure → passages → sections → complete work.</p>}
        {nodes.length ? <ol className="libraryContents">{nodes.map((node) => <li key={node.id}><span>{node.ordinal + 1}</span><div><strong>{node.title}</strong><small>{node.kind} · offsets {node.start_offset ?? 0}–{node.end_offset ?? 0}</small></div></li>)}</ol> : null}
        <div className="libraryPlanes"><div><strong>Source</strong><span>Policy-controlled original</span></div><div><strong>Index</strong><span>Rebuildable passages and locators</span></div><div><strong>Memory</strong><span>Reviewed knowledge with lineage</span></div></div>
      </section>

      <section className="libraryCard">
        <div className="libraryCardHeader"><div><span>3</span><h2>Talk with the book</h2></div><small>Evidence before interpretation</small></div>
        <label>Remembered quote or statement<textarea value={rememberedStatement} onChange={(event) => setRememberedStatement(event.target.value)} placeholder={'“All roads lead to Rome.”'} /></label>
        <label>Your question<textarea value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="Does this mean that regardless of the university, the Earth still revolves around the Sun?" /></label>
        <label>Reader interpretation <span className="optionalLabel">optional · creates a suggested memory</span><textarea value={interpretation} onChange={(event) => setInterpretation(event.target.value)} placeholder="Write what this means to you. It will stay attributed to you/the agent, not to the author." /></label>
        <button className="primaryNotebookButton" type="button" onClick={() => void askBook()} disabled={busy !== ''}>{busy === 'query' ? 'Finding evidence…' : 'Ask across imported books'}</button>

        {queried ? <div className="libraryEvidence"><h3>Grounded evidence</h3>{evidence.length ? evidence.map((result) => <article key={result.passage.id}><div><span className="sourceBadge human">Source evidence</span><small>score {result.score}</small></div><p>{result.passage.text}</p><footer>{result.passage.locator.display} · {result.passage.structural_node_id}</footer></article>) : <p className="libraryEmpty">No authorized source evidence supports this question yet. The agent will not invent an answer.</p>}</div> : null}

        {proposal ? <div className="libraryProposal"><div><span className="sourceBadge agent">Agent interpretation</span><strong>{proposal.status}</strong></div><h3>Suggested memory</h3><p>{proposal.content}</p>{proposal.citations?.map((citation) => <small key={citation.id}>{citation.id} · {citation.locator.display}</small>)}{proposal.status === 'suggested' ? <div><button className="primaryNotebookButton" type="button" onClick={() => void reviewProposal('accept')} disabled={busy !== ''}>Accept memory</button><button type="button" onClick={() => void reviewProposal('reject')} disabled={busy !== ''}>Reject</button></div> : <p className="libraryReviewStatus">Review complete · {proposal.status}{proposal.memory_id ? ` · memory ${proposal.memory_id}` : ''}</p>}</div> : null}
      </section>

      {error ? <div className="notebookError" role="alert">{error}</div> : null}
    </section>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong title={value}>{value}</strong></div>
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}
