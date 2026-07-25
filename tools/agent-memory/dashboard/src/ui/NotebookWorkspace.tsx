import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  createNote,
  getNote,
  listNoteBacklinks,
  listNoteRevisions,
  listNotes,
  recallPreview,
  retryNoteIndex,
  restoreNote,
  restoreNoteRevision,
  searchMemories,
  trashNote,
  updateNote,
  type MemoryEntry,
  type NoteDocument,
  type NoteLink,
  type NoteRevision,
  type ProjectListItem,
} from '../lib/api'
import { MarkdownView } from './MarkdownView'
import './notebook.css'

type Destination = 'notes' | 'search' | 'ask' | 'activity'
type EditorMode = 'edit' | 'preview' | 'split'
type ContextTab = 'ask' | 'backlinks' | 'outline' | 'properties' | 'history'
type SaveState = 'idle' | 'dirty' | 'saving' | 'saved' | 'error'
type AskScope = 'active' | 'workspace' | 'all'

type NotebookWorkspaceProps = {
  workspace: string
  projects: ProjectListItem[]
  theme: 'light' | 'dark'
  onWorkspaceChange: (workspace: string) => void
  onOpenSystem: () => void
  onThemeChange: () => void
}

const emptyProperties: Record<string, unknown> = {}

function startsAboveBreakpoint(maxWidth: number) {
  return typeof window === 'undefined' || !window.matchMedia(`(max-width: ${maxWidth}px)`).matches
}

export function NotebookWorkspace({
  workspace,
  projects,
  theme,
  onWorkspaceChange,
  onOpenSystem,
  onThemeChange,
}: NotebookWorkspaceProps) {
  const [destination, setDestination] = useState<Destination>('notes')
  const [notes, setNotes] = useState<NoteDocument[]>([])
  const [trash, setTrash] = useState<NoteDocument[]>([])
  const [activeNote, setActiveNote] = useState<NoteDocument | null>(null)
  const [openTabs, setOpenTabs] = useState<NoteDocument[]>([])
  const [query, setQuery] = useState('')
  const [editorMode, setEditorMode] = useState<EditorMode>('edit')
  const [contextTab, setContextTab] = useState<ContextTab>('ask')
  const [contextOpen, setContextOpen] = useState(() => startsAboveBreakpoint(1240))
  const [explorerOpen, setExplorerOpen] = useState(() => startsAboveBreakpoint(1024))
  const [saveState, setSaveState] = useState<SaveState>('idle')
  const [error, setError] = useState('')
  const [backlinks, setBacklinks] = useState<NoteLink[]>([])
  const [revisions, setRevisions] = useState<NoteRevision[]>([])
  const [searchResults, setSearchResults] = useState<MemoryEntry[]>([])
  const [searchBusy, setSearchBusy] = useState(false)
  const [askText, setAskText] = useState('')
  const [askScope, setAskScope] = useState<AskScope>('workspace')
  const [askAnswer, setAskAnswer] = useState('')
  const [askEvidence, setAskEvidence] = useState<MemoryEntry[]>([])
  const [askBusy, setAskBusy] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const editorRef = useRef<HTMLTextAreaElement>(null)
  const activeNoteRef = useRef<NoteDocument | null>(null)

  const refreshNotes = useCallback(async () => {
    if (!workspace) return
    const response = await listNotes({ workspace, include_deleted: true })
    setNotes(response.notes.filter((note) => !note.deleted_at))
    setTrash(response.notes.filter((note) => Boolean(note.deleted_at)))
  }, [workspace])

  useEffect(() => {
    setActiveNote(null)
    setOpenTabs([])
    setError('')
    void refreshNotes().catch((reason: unknown) => setError(messageOf(reason)))
  }, [refreshNotes])

  useEffect(() => {
    activeNoteRef.current = activeNote
  }, [activeNote])

  useEffect(() => {
    const tablet = window.matchMedia('(max-width: 1024px)')
    const compact = window.matchMedia('(max-width: 1240px)')
    const closeExplorer = (event: MediaQueryListEvent) => {
      if (event.matches) setExplorerOpen(false)
    }
    const closeContext = (event: MediaQueryListEvent) => {
      if (event.matches) setContextOpen(false)
    }
    tablet.addEventListener('change', closeExplorer)
    compact.addEventListener('change', closeContext)
    return () => {
      tablet.removeEventListener('change', closeExplorer)
      compact.removeEventListener('change', closeContext)
    }
  }, [])

  useEffect(() => {
    if (!activeNote || !['pending', 'indexing'].includes(activeNote.index_state)) return
    const timeout = window.setTimeout(async () => {
      try {
        const response = await getNote({ workspace, note_id: activeNote.id })
        activeNoteRef.current = response.note
        setActiveNote(response.note)
        setOpenTabs((current) => current.map((tab) => (tab.id === response.note.id ? response.note : tab)))
      } catch {
        // Save and index errors are surfaced by their explicit actions.
      }
    }, 1000)
    return () => window.clearTimeout(timeout)
  }, [activeNote, workspace])

  const openNote = useCallback(async (noteID: string) => {
    if (!workspace) return
    try {
      const response = await getNote({ workspace, note_id: noteID })
      setActiveNote(response.note)
      setOpenTabs((current) => {
        const existing = current.find((note) => note.id === response.note.id)
        return existing
          ? current.map((note) => (note.id === response.note.id ? response.note : note))
          : [...current, response.note]
      })
      setSaveState('idle')
      setError('')
      const [links, history] = await Promise.all([
        listNoteBacklinks({ workspace, note_id: noteID }),
        listNoteRevisions({ workspace, note_id: noteID }),
      ])
      setBacklinks(links.backlinks)
      setRevisions(history.revisions)
    } catch (reason) {
      setError(messageOf(reason))
    }
  }, [workspace])

  const createNewNote = useCallback(async () => {
    if (!workspace) return
    const title = nextUntitledTitle(notes)
    try {
      const response = await createNote({
        workspace,
        path: `${title}.md`,
        title,
        body: `# ${title}\n\n`,
        properties: {},
      })
      await refreshNotes()
      await openNote(response.note.id)
      requestAnimationFrame(() => editorRef.current?.focus())
    } catch (reason) {
      setError(messageOf(reason))
    }
  }, [notes, openNote, refreshNotes, workspace])

  const saveActiveNote = useCallback(async () => {
    const note = activeNoteRef.current
    if (!note || saveState === 'saving') return
    setSaveState('saving')
    try {
      const response = await updateNote({
        workspace,
        note_id: note.id,
        expected_revision: note.revision,
        path: note.path,
        title: note.title,
        body: note.body,
        properties: note.properties ?? emptyProperties,
      })
      activeNoteRef.current = response.note
      setActiveNote(response.note)
      setOpenTabs((current) => current.map((tab) => (tab.id === response.note.id ? response.note : tab)))
      setSaveState('saved')
      setError('')
      await refreshNotes()
      const [links, history] = await Promise.all([
        listNoteBacklinks({ workspace, note_id: note.id }),
        listNoteRevisions({ workspace, note_id: note.id }),
      ])
      setBacklinks(links.backlinks)
      setRevisions(history.revisions)
    } catch (reason) {
      setSaveState('error')
      setError(messageOf(reason))
    }
  }, [refreshNotes, saveState, workspace])

  useEffect(() => {
    if (saveState !== 'dirty' || !activeNote) return
    const timeout = window.setTimeout(() => void saveActiveNote(), 700)
    return () => window.clearTimeout(timeout)
  }, [activeNote, saveActiveNote, saveState])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const modifier = event.metaKey || event.ctrlKey
      if (modifier && event.key.toLowerCase() === 's') {
        event.preventDefault()
        void saveActiveNote()
      }
      if (modifier && event.key.toLowerCase() === 'n') {
        event.preventDefault()
        void createNewNote()
      }
      if (modifier && event.key.toLowerCase() === 'p') {
        event.preventDefault()
        setPaletteOpen(true)
      }
      if (modifier && event.shiftKey && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        setDestination('search')
      }
      if (modifier && event.shiftKey && event.key.toLowerCase() === 'a') {
        event.preventDefault()
        setDestination('ask')
      }
      if (modifier && event.key.toLowerCase() === 'w' && activeNoteRef.current) {
        event.preventDefault()
        closeTab(activeNoteRef.current.id)
      }
      if (event.ctrlKey && event.key === 'Tab' && openTabs.length > 1) {
        event.preventDefault()
        const currentIndex = openTabs.findIndex((note) => note.id === activeNoteRef.current?.id)
        const direction = event.shiftKey ? -1 : 1
        const nextIndex = (currentIndex + direction + openTabs.length) % openTabs.length
        void openNote(openTabs[nextIndex].id)
      }
      if (event.key === 'Escape') setPaletteOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [createNewNote, openNote, openTabs, saveActiveNote])

  function patchActiveNote(patch: Partial<NoteDocument>) {
    setActiveNote((current) => {
      if (!current) return current
      const next = { ...current, ...patch }
      activeNoteRef.current = next
      return next
    })
    setSaveState('dirty')
  }

  function closeTab(noteID: string) {
    setOpenTabs((current) => {
      const index = current.findIndex((note) => note.id === noteID)
      const next = current.filter((note) => note.id !== noteID)
      if (activeNote?.id === noteID) {
        const fallback = next[Math.max(0, index - 1)] ?? null
        setActiveNote(fallback)
        activeNoteRef.current = fallback
        if (fallback) void openNote(fallback.id)
      }
      return next
    })
  }

  async function moveToTrash() {
    if (!activeNote || !window.confirm(`Move “${activeNote.title}” to trash?`)) return
    try {
      await trashNote({ workspace, note_id: activeNote.id })
      closeTab(activeNote.id)
      setActiveNote(null)
      await refreshNotes()
    } catch (reason) {
      setError(messageOf(reason))
    }
  }

  async function restoreFromTrash(note: NoteDocument) {
    try {
      await restoreNote({ workspace, note_id: note.id })
      await refreshNotes()
      await openNote(note.id)
    } catch (reason) {
      setError(messageOf(reason))
    }
  }

  async function openOrCreateLinkedNote(title: string) {
    const target = notes.find((note) => note.title.toLowerCase() === title.toLowerCase())
    if (target) {
      await openNote(target.id)
      return
    }
    if (!window.confirm(`Create the linked note “${title}”?`)) return
    try {
      const response = await createNote({
        workspace,
        path: `${safePathTitle(title)}.md`,
        title,
        body: `# ${title}\n\n`,
        properties: {},
      })
      await refreshNotes()
      await openNote(response.note.id)
    } catch (reason) {
      setError(messageOf(reason))
    }
  }

  async function restoreRevision(revision: NoteRevision) {
    if (!activeNote || !window.confirm(`Restore revision ${revision.revision} as a new revision?`)) return
    try {
      const response = await restoreNoteRevision({
        workspace,
        note_id: activeNote.id,
        revision: revision.revision,
        expected_revision: activeNote.revision,
      })
      setActiveNote(response.note)
      activeNoteRef.current = response.note
      setSaveState('saved')
      await openNote(response.note.id)
    } catch (reason) {
      setError(messageOf(reason))
    }
  }

  async function retryIndex() {
    if (!activeNote) return
    try {
      await retryNoteIndex({ workspace, note_id: activeNote.id })
      const pending = { ...activeNote, index_state: 'pending' as const, index_error: '' }
      setActiveNote(pending)
      activeNoteRef.current = pending
      setSaveState('saved')
    } catch (reason) {
      setError(messageOf(reason))
    }
  }

  async function runSearch() {
    if (!query.trim()) return
    setSearchBusy(true)
    setError('')
    try {
      const response = await searchMemories({
        workspace,
        query: query.trim(),
        top_k: 30,
        explain: true,
        filters: { types: [], tiers: [] },
      })
      setSearchResults(response.results ?? [])
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setSearchBusy(false)
    }
  }

  async function runAsk() {
    if (!askText.trim()) return
    setAskBusy(true)
    setError('')
    try {
      const targets = askScope === 'all' ? projects.map((project) => project.name) : [workspace]
      const scopePrompt = askScope === 'active' && activeNote
        ? `Answer using the active note "${activeNote.title}" as the primary source. `
        : ''
      const responses = await Promise.all(targets.map((targetWorkspace) => recallPreview({
        workspace: targetWorkspace,
        task_description: `${scopePrompt}${askText.trim()}`,
        top_k: 30,
        token_budget: Math.max(800, Math.floor(4000 / Math.max(1, targets.length))),
        explain: true,
        include_memories: true,
      })))
      const answer = responses.map((response, index) => {
        const label = targets.length > 1 ? `### ${targets[index]}\n\n` : ''
        return response.context_block ? `${label}${response.context_block}` : ''
      }).filter(Boolean).join('\n\n')
      setAskAnswer(answer || 'No grounded answer was found. Try a more specific question or another scope.')
      setAskEvidence(responses.flatMap((response) => response.memories_included_full ?? []))
    } catch (reason) {
      setError(messageOf(reason))
    } finally {
      setAskBusy(false)
    }
  }

  async function saveAnswerAsNote() {
    if (!askAnswer || !workspace) return
    if (!window.confirm('Save this grounded answer and its sources as a new note?')) return
    const title = askText.trim().slice(0, 72) || 'AI research'
    try {
      const response = await createNote({
        workspace,
        path: `AI/${safePathTitle(title)}.md`,
        title,
        body: `# ${title}\n\n${askAnswer}\n\n## Sources\n\n${formatEvidenceLinks(askEvidence)}`,
        properties: { source: 'agent-memory ask' },
        author_kind: 'agent_assisted',
      })
      await refreshNotes()
      await openNote(response.note.id)
      setDestination('notes')
    } catch (reason) {
      setError(messageOf(reason))
    }
  }

  async function appendAnswerToActiveNote() {
    if (!activeNote || !askAnswer) return
    if (!window.confirm(`Append this grounded answer and its sources to “${activeNote.title}”?`)) return
    patchActiveNote({
      body: `${activeNote.body.trimEnd()}\n\n## AI research\n\n${askAnswer}\n\n${formatEvidenceLinks(askEvidence)}\n`,
    })
    setDestination('notes')
  }

  const filteredNotes = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    if (!normalized) return notes
    return notes.filter((note) => `${note.title} ${note.path}`.toLowerCase().includes(normalized))
  }, [notes, query])
  const noteGroups = useMemo(() => groupNotesByFolder(filteredNotes), [filteredNotes])
  const outline = useMemo(() => parseOutline(activeNote?.body ?? ''), [activeNote?.body])
  const outgoingLinks = useMemo(() => parseInternalLinks(activeNote?.body ?? ''), [activeNote?.body])
  const statusText = noteStatusText(activeNote, saveState)

  return (
    <section className="notebookShell">
      <aside className="notebookRail" aria-label="Primary navigation">
        <button className="notebookBrand" type="button" onClick={() => setDestination('notes')} aria-label="Agent Memory notebook">am</button>
        <RailButton label="Notes" active={destination === 'notes'} onClick={() => setDestination('notes')} glyph="N" />
        <RailButton label="Search" active={destination === 'search'} onClick={() => setDestination('search')} glyph="S" />
        <RailButton label="Ask" active={destination === 'ask'} onClick={() => setDestination('ask')} glyph="A" />
        <RailButton label="Activity" active={destination === 'activity'} onClick={() => setDestination('activity')} glyph="R" />
        <div className="notebookRailSpacer" />
        <RailButton label="System" active={false} onClick={onOpenSystem} glyph="SYS" />
        <RailButton label={theme === 'dark' ? 'Light theme' : 'Dark theme'} active={false} onClick={onThemeChange} glyph={theme === 'dark' ? '☼' : '◐'} />
      </aside>

      {explorerOpen ? (
        <aside className="notebookExplorer">
          <div className="notebookWorkspacePicker">
            <select value={workspace} onChange={(event) => onWorkspaceChange(event.target.value)} aria-label="Notebook workspace">
              {projects.map((project) => <option key={project.name} value={project.name}>{project.name}</option>)}
            </select>
            <button type="button" onClick={() => void createNewNote()} title="New note (Cmd/Ctrl+N)">+</button>
          </div>
          <label className="notebookFilter">
            <span>Filter notes</span>
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find a note…" />
          </label>
          <div className="notebookSectionLabel"><span>Notes</span><span>{filteredNotes.length}</span></div>
          <nav className="noteTree" aria-label="Notes">
            {noteGroups.map((group) => (
              <section key={group.folder} className="noteFolder">
                {group.folder ? <div className="noteFolderLabel"><span>⌄</span>{group.folder}</div> : null}
                {group.notes.map((note) => (
                  <button key={note.id} type="button" className={activeNote?.id === note.id ? 'noteTreeItem active' : 'noteTreeItem'} onClick={() => void openNote(note.id)}>
                    <span className="noteTreeIcon">◇</span>
                    <span><strong>{note.title}</strong><small>{note.path}</small></span>
                  </button>
                ))}
              </section>
            ))}
            {filteredNotes.length === 0 ? <p className="notebookHint">Create your first note. It will become available to both you and your agents.</p> : null}
          </nav>
          {trash.length ? (
            <>
              <div className="notebookSectionLabel"><span>Trash</span><span>{trash.length}</span></div>
              <div className="noteTree">
                {trash.map((note) => (
                  <button key={note.id} type="button" className="noteTreeItem muted" onClick={() => void restoreFromTrash(note)}>
                    <span className="noteTreeIcon">×</span><span><strong>{note.title}</strong><small>Restore note</small></span>
                  </button>
                ))}
              </div>
            </>
          ) : null}
        </aside>
      ) : null}

      <main className="notebookMain">
        <header className="notebookTopbar">
          <button type="button" className="notebookIconButton" onClick={() => setExplorerOpen((open) => !open)} aria-label="Toggle note explorer">☰</button>
          <div className="notebookTabs" role="tablist" aria-label="Open notes">
            {openTabs.map((note) => (
              <div key={note.id} className={activeNote?.id === note.id ? 'notebookTab active' : 'notebookTab'}>
                <button type="button" role="tab" aria-selected={activeNote?.id === note.id} onClick={() => void openNote(note.id)}>{note.title}</button>
                <button type="button" onClick={() => closeTab(note.id)} aria-label={`Close ${note.title}`}>×</button>
              </div>
            ))}
          </div>
          <button type="button" className="notebookIconButton" onClick={() => setPaletteOpen(true)} aria-label="Open command palette">⌘</button>
          <button type="button" className="notebookIconButton" onClick={() => setContextOpen((open) => !open)} aria-label="Toggle context panel">◫</button>
        </header>

        {destination === 'notes' ? (
          activeNote ? (
            <article className="notebookDocument">
              <header className="noteHeader">
                <div>
                  <input className="noteTitleInput" value={activeNote.title} onChange={(event) => patchActiveNote({ title: event.target.value })} aria-label="Note title" />
                  <p>{activeNote.path}</p>
                </div>
                <div className="noteHeaderActions">
                  {activeNote.index_state === 'failed'
                    ? <button type="button" className="noteStatus error" onClick={() => void retryIndex()}>Index failed · Retry</button>
                    : <span className={`noteStatus ${saveState}`}>{statusText}</span>}
                  <div className="editorModeSwitcher" aria-label="Editor mode">
                    {(['edit', 'preview', 'split'] as EditorMode[]).map((mode) => (
                      <button key={mode} type="button" className={editorMode === mode ? 'active' : ''} onClick={() => setEditorMode(mode)}>{mode}</button>
                    ))}
                  </div>
                  <button type="button" className="dangerTextButton" onClick={() => void moveToTrash()}>Trash</button>
                </div>
              </header>
              <div className={`notebookEditor mode-${editorMode}`}>
                {editorMode !== 'preview' ? (
                  <textarea
                    ref={editorRef}
                    value={activeNote.body}
                    onChange={(event) => patchActiveNote({ body: event.target.value })}
                    spellCheck
                    aria-label="Markdown editor"
                  />
                ) : null}
                {editorMode !== 'edit' ? (
                  <div className="notebookPreview" aria-label="Markdown preview">
                    <MarkdownView markdown={activeNote.body} clamp={false} theme={theme} />
                  </div>
                ) : null}
              </div>
            </article>
          ) : <NotebookWelcome onCreate={() => void createNewNote()} onAsk={() => setDestination('ask')} />
        ) : null}

        {destination === 'search' ? (
          <section className="notebookUtilityPage">
            <p className="eyebrow">Knowledge search</p>
            <h1>Find a note or memory</h1>
            <div className="notebookSearchBar">
              <input value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') void runSearch() }} placeholder="Search decisions, people, projects…" autoFocus />
              <button type="button" onClick={() => void runSearch()} disabled={searchBusy}>{searchBusy ? 'Searching…' : 'Search'}</button>
            </div>
            <div className="unifiedResults">
              {filteredNotes.map((note) => (
                <button key={note.id} type="button" className="unifiedResult" onClick={() => { void openNote(note.id); setDestination('notes') }}>
                  <span className="sourceBadge human">Human note</span><strong>{note.title}</strong><small>{note.path}</small>
                </button>
              ))}
              {searchResults.map((memory) => (
                <article
                  key={memory.id}
                  className={memory.source?.note_id ? 'unifiedResult humanMemoryResult' : 'unifiedResult'}
                  role={memory.source?.note_id ? 'button' : undefined}
                  tabIndex={memory.source?.note_id ? 0 : undefined}
                  onClick={memory.source?.note_id ? () => {
                    void openNote(memory.source?.note_id ?? '')
                    setDestination('notes')
                  } : undefined}
                  onKeyDown={memory.source?.note_id ? (event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      void openNote(memory.source?.note_id ?? '')
                      setDestination('notes')
                    }
                  } : undefined}
                >
                  <span className={memory.source?.note_id ? 'sourceBadge human' : 'sourceBadge agent'}>
                    {memory.source?.note_id ? 'Human note' : 'Agent memory'}
                  </span>
                  <MarkdownView markdown={memory.content} clamp theme={theme} />
                  {memory.source?.note_path ? <small>{memory.source.note_path}</small> : null}
                </article>
              ))}
            </div>
          </section>
        ) : null}

        {destination === 'ask' ? (
          <AskWorkspace
            value={askText}
            scope={askScope}
            answer={askAnswer}
            evidence={askEvidence}
            busy={askBusy}
            theme={theme}
            onChange={setAskText}
            onScopeChange={setAskScope}
            onSubmit={() => void runAsk()}
            onSaveNew={() => void saveAnswerAsNote()}
            onAppend={() => void appendAnswerToActiveNote()}
            canAppend={Boolean(activeNote)}
            canUseActive={Boolean(activeNote)}
            onOpenEvidence={(noteID) => {
              void openNote(noteID)
              setDestination('notes')
            }}
          />
        ) : null}

        {destination === 'activity' ? (
          <section className="notebookUtilityPage">
            <p className="eyebrow">Activity</p>
            <h1>Recent knowledge</h1>
            <div className="activityList">
              {[...notes].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).slice(0, 20).map((note) => (
                <button key={note.id} type="button" onClick={() => { void openNote(note.id); setDestination('notes') }}>
                  <span>{note.title}</span><small>{new Date(note.updated_at).toLocaleString()} · {note.index_state}</small>
                </button>
              ))}
            </div>
          </section>
        ) : null}

        {error ? <div className="notebookError" role="alert">{error}</div> : null}
        <footer className="notebookStatusbar">
          <span>{workspace || 'No workspace'}</span>
          <span>{activeNote ? `${activeNote.revision} revisions · ${statusText}` : `${notes.length} notes`}</span>
        </footer>
      </main>

      {contextOpen && destination === 'notes' ? (
        <aside className="notebookContext">
          <div className="contextTabs" role="tablist" aria-label="Note context">
            {(['ask', 'backlinks', 'outline', 'properties', 'history'] as ContextTab[]).map((tab) => (
              <button key={tab} type="button" className={contextTab === tab ? 'active' : ''} onClick={() => setContextTab(tab)}>{capitalize(tab)}</button>
            ))}
          </div>
          {contextTab === 'ask' ? (
            <div className="contextBody">
              <h2>Ask about this note</h2>
              <textarea aria-label="Ask agent-memory" value={askText} onChange={(event) => setAskText(event.target.value)} placeholder="What decisions did we make?" />
              <button type="button" className="primaryNotebookButton" onClick={() => { setDestination('ask'); void runAsk() }}>Ask agent-memory</button>
            </div>
          ) : null}
          {contextTab === 'backlinks' ? (
            <div className="contextBody">
              <h2>Backlinks</h2>
              {backlinks.map((link) => <button key={`${link.source_note_id}:${link.line}`} className="contextListItem" type="button" onClick={() => void openNote(link.source_note_id)}>{link.snippet}<small>Line {link.line}</small></button>)}
              {backlinks.length === 0 ? <p className="notebookHint">No notes link here yet.</p> : null}
              <h3>Outgoing links</h3>
              {outgoingLinks.map((link) => <button key={link} className="contextListItem" type="button" onClick={() => void openOrCreateLinkedNote(link)}>{link}</button>)}
            </div>
          ) : null}
          {contextTab === 'outline' ? (
            <div className="contextBody">
              <h2>Outline</h2>
              {outline.map((heading) => <button key={`${heading.line}:${heading.text}`} className="contextListItem" style={{ paddingLeft: `${12 + heading.level * 8}px` }} type="button" onClick={() => focusEditorLine(editorRef.current, heading.line)}>{heading.text}</button>)}
            </div>
          ) : null}
          {contextTab === 'properties' && activeNote ? (
            <div className="contextBody">
              <h2>Properties</h2>
              <label>Path<input value={activeNote.path} onChange={(event) => patchActiveNote({ path: event.target.value })} /></label>
              <label>Status<input value={String(activeNote.properties?.status ?? '')} onChange={(event) => patchActiveNote({ properties: { ...activeNote.properties, status: event.target.value } })} /></label>
              <label>Tags<input value={propertyTags(activeNote.properties).join(', ')} onChange={(event) => patchActiveNote({ properties: { ...activeNote.properties, tags: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) } })} /></label>
              <p className="notebookHint">Properties stay small and machine-readable so both people and agents can use them.</p>
            </div>
          ) : null}
          {contextTab === 'history' ? (
            <div className="contextBody">
              <h2>Revision history</h2>
              {revisions.map((revision) => <button key={revision.revision} type="button" className="contextListItem" onClick={() => void restoreRevision(revision)}><strong>Revision {revision.revision}</strong><small>{new Date(revision.created_at).toLocaleString()} · {revision.author_kind}</small></button>)}
            </div>
          ) : null}
        </aside>
      ) : null}

      {paletteOpen ? (
        <div className="commandPaletteBackdrop" role="presentation" onMouseDown={() => setPaletteOpen(false)}>
          <section className="commandPalette" role="dialog" aria-modal="true" aria-label="Command palette" onMouseDown={(event) => event.stopPropagation()}>
            <p>Command palette</p>
            <button type="button" onClick={() => { setPaletteOpen(false); void createNewNote() }}>New note <kbd>⌘N</kbd></button>
            <button type="button" onClick={() => { setPaletteOpen(false); setDestination('search') }}>Search knowledge <kbd>⌘⇧F</kbd></button>
            <button type="button" onClick={() => { setPaletteOpen(false); setDestination('ask') }}>Ask agent-memory</button>
            <button type="button" onClick={() => { setPaletteOpen(false); onOpenSystem() }}>Open System</button>
          </section>
        </div>
      ) : null}
    </section>
  )
}

function RailButton({ label, glyph, active, onClick }: { label: string; glyph: string; active: boolean; onClick: () => void }) {
  return <button type="button" className={active ? 'railButton active' : 'railButton'} onClick={onClick} title={label} aria-label={label}><span>{glyph}</span></button>
}

function NotebookWelcome({ onCreate, onAsk }: { onCreate: () => void; onAsk: () => void }) {
  return (
    <section className="notebookWelcome">
      <p className="eyebrow">Your shared second brain</p>
      <h1>Write what matters.<br />Let your agents remember it.</h1>
      <p>Notes you create here become grounded knowledge for search, recall, and future work.</p>
      <div><button type="button" className="primaryNotebookButton" onClick={onCreate}>Create a note</button><button type="button" onClick={onAsk}>Ask this workspace</button></div>
      <dl><div><dt>Markdown</dt><dd>Write naturally with headings, tasks, tables, code, and links.</dd></div><div><dt>Mermaid</dt><dd>Turn fenced diagrams into readable system and process maps.</dd></div><div><dt>Shared recall</dt><dd>Human notes and agent memories contribute with visible provenance.</dd></div></dl>
    </section>
  )
}

function AskWorkspace(props: {
  value: string
  scope: AskScope
  answer: string
  evidence: MemoryEntry[]
  busy: boolean
  theme: 'light' | 'dark'
  canAppend: boolean
  canUseActive: boolean
  onChange: (value: string) => void
  onScopeChange: (scope: AskScope) => void
  onSubmit: () => void
  onSaveNew: () => void
  onAppend: () => void
  onOpenEvidence: (noteID: string) => void
}) {
  return (
    <section className="askWorkspace">
      <div className="askIntro"><p className="eyebrow">Ask your second brain</p><h1>What do you need to know?</h1><p>Answers are assembled from your notes and agent memories. Sources stay attached.</p></div>
      {props.answer ? (
        <article className="askAnswer">
          <MarkdownView markdown={props.answer} clamp={false} theme={props.theme} />
          <div className="askEvidence"><h2>Sources</h2>{props.evidence.map((memory) => (
            <div
              key={memory.id}
              className={memory.source?.note_id ? 'askEvidenceItem navigable' : 'askEvidenceItem'}
              role={memory.source?.note_id ? 'button' : undefined}
              tabIndex={memory.source?.note_id ? 0 : undefined}
              onClick={memory.source?.note_id ? () => props.onOpenEvidence(memory.source?.note_id ?? '') : undefined}
              onKeyDown={memory.source?.note_id ? (event) => {
                if (event.key === 'Enter' || event.key === ' ') props.onOpenEvidence(memory.source?.note_id ?? '')
              } : undefined}
            >
              <span className={memory.source?.note_id ? 'sourceBadge human' : 'sourceBadge agent'}>{memory.source?.note_id ? 'Human note' : 'Agent memory'}</span>
              <p>{memory.content.slice(0, 180)}</p>
            </div>
          ))}</div>
          <div className="askAnswerActions"><button type="button" onClick={props.onSaveNew}>Save as new note</button><button type="button" onClick={props.onAppend} disabled={!props.canAppend}>Append to active note</button></div>
        </article>
      ) : null}
      <div className="askScope">
        <label>Search scope
          <select value={props.scope} onChange={(event) => props.onScopeChange(event.target.value as AskScope)}>
            <option value="active" disabled={!props.canUseActive}>Active note</option>
            <option value="workspace">Current workspace</option>
            <option value="all">All workspaces</option>
          </select>
        </label>
      </div>
      <div className="askComposer"><textarea aria-label="Ask agent-memory" value={props.value} onChange={(event) => props.onChange(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); props.onSubmit() } }} placeholder="Ask about a decision, project, person, or past session…" /><button type="button" disabled={props.busy || !props.value.trim()} onClick={props.onSubmit}>{props.busy ? 'Thinking…' : 'Ask'}</button></div>
    </section>
  )
}

function nextUntitledTitle(notes: NoteDocument[]) {
  let index = 1
  const titles = new Set(notes.map((note) => note.title.toLowerCase()))
  const paths = new Set(notes.map((note) => note.path.toLowerCase()))
  while (true) {
    const candidate = index === 1 ? 'untitled' : `untitled ${index}`
    if (!titles.has(candidate) && !paths.has(`${candidate}.md`)) break
    index += 1
  }
  return index === 1 ? 'Untitled' : `Untitled ${index}`
}

function groupNotesByFolder(notes: NoteDocument[]) {
  const groups = new Map<string, NoteDocument[]>()
  for (const note of notes) {
    const slash = note.path.lastIndexOf('/')
    const folder = slash >= 0 ? note.path.slice(0, slash) : ''
    groups.set(folder, [...(groups.get(folder) ?? []), note])
  }
  return [...groups.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([folder, groupNotes]) => ({
      folder,
      notes: [...groupNotes].sort((left, right) => left.title.localeCompare(right.title)),
    }))
}

function parseOutline(markdown: string) {
  return markdown.split('\n').flatMap((line, index) => {
    const match = /^(#{1,6})\s+(.+)$/.exec(line)
    return match ? [{ level: match[1].length, text: match[2].trim(), line: index + 1 }] : []
  })
}

function parseInternalLinks(markdown: string) {
  const links = new Set<string>()
  for (const match of markdown.matchAll(/\[\[([^\]|#]+)(?:#[^\]|]+)?(?:\|[^\]]+)?\]\]/g)) links.add(match[1].trim())
  return [...links]
}

function focusEditorLine(editor: HTMLTextAreaElement | null, line: number) {
  if (!editor) return
  const offset = editor.value.split('\n').slice(0, Math.max(0, line - 1)).reduce((total, value) => total + value.length + 1, 0)
  editor.focus()
  editor.setSelectionRange(offset, offset)
}

function noteStatusText(note: NoteDocument | null, state: SaveState) {
  if (!note) return ''
  if (state === 'dirty') return 'Unsaved changes'
  if (state === 'saving') return 'Saving'
  if (state === 'error') return 'Save failed'
  if (note.index_state === 'failed') return 'Saved, indexing failed'
  if (note.index_state === 'pending' || note.index_state === 'indexing') return 'Saved · indexing'
  if (note.index_state === 'ready') return 'Saved · ready for AI'
  return state === 'saved' ? 'Saved' : 'Ready'
}

function propertyTags(properties: Record<string, unknown>) {
  const value = properties?.tags
  return Array.isArray(value) ? value.map(String) : []
}

function formatEvidenceLinks(evidence: MemoryEntry[]) {
  if (!evidence.length) return '_No sources were returned._'
  return evidence.map((memory) => `- ${memory.source?.note_path ? `[[${memory.source.note_path.replace(/\.md$/i, '')}]]` : `Agent memory \`${memory.id}\``}`).join('\n')
}

function safePathTitle(value: string) {
  return value.replace(/[\\/:*?"<>|]/g, '-').replace(/\s+/g, ' ').trim() || 'AI research'
}

function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}
