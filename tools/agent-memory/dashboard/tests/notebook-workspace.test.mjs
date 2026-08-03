import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/App.tsx', import.meta.url), 'utf8')
const notebookSource = await readFile(new URL('../src/ui/NotebookWorkspace.tsx', import.meta.url), 'utf8').catch(() => '')
const librarySource = await readFile(new URL('../src/ui/LibraryWorkspace.tsx', import.meta.url), 'utf8').catch(() => '')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const stylesSource = await readFile(new URL('../src/ui/notebook.css', import.meta.url), 'utf8').catch(() => '')

test('notes are the default destination with a one-release rollback flag and technical surfaces remain reachable', () => {
  assert.match(appSource, /notebookEnabled \? 'notes' : 'overview'/)
  assert.match(appSource, /VITE_NOTEBOOK_ENABLED/)
  assert.match(appSource, /surface === 'notes'/)
  assert.match(appSource, /<NotebookWorkspace/)
  assert.match(appSource, /onOpenSystem=/)
})

test('notebook API exposes document lifecycle operations', () => {
  for (const operation of [
    'listNotes',
    'getNote',
    'createNote',
    'updateNote',
    'trashNote',
    'restoreNote',
    'listNoteRevisions',
    'restoreNoteRevision',
    'listNoteBacklinks',
  ]) {
    assert.match(apiSource, new RegExp(`export function ${operation}`))
  }
})

test('library is a notebook destination backed by the complete study API flow', () => {
  assert.match(notebookSource, /type Destination = [^\n]*'library'/)
  assert.match(notebookSource, /label="Library"/)
  assert.match(notebookSource, /<LibraryWorkspace workspace=\{workspace\}/)

  for (const operation of [
    'importLibraryBook',
    'getLibraryStructure',
    'queryLibrary',
    'reviewLibraryMemory',
  ]) {
    assert.match(apiSource, new RegExp(`export function ${operation}`))
  }
})

test('library workspace separates whole-book indexing source evidence and interpretation review', () => {
  assert.match(librarySource, /Import a whole book/)
  assert.match(librarySource, /accept="\.md,\.markdown,\.txt,text\/markdown,text\/plain"/)
  assert.match(librarySource, /Book contents and index/)
  assert.match(librarySource, /Grounded evidence/)
  assert.match(librarySource, /Reader interpretation/)
  assert.match(librarySource, /No authorized source evidence supports this question yet/)
  assert.match(librarySource, /Accept memory/)
  assert.match(librarySource, /Reject/)
  assert.doesNotMatch(librarySource, /Users\/time/)
})

test('API client reports non-JSON responses with route context', () => {
  assert.match(apiSource, /await res\.text\(\)/)
  assert.match(apiSource, /returned a non-JSON response/)
  assert.match(apiSource, /path/)
  assert.doesNotMatch(apiSource, /await res\.json\(\)/)
})

test('notebook workspace provides explorer editor preview context and ask regions', () => {
  assert.match(notebookSource, /className="notebookExplorer"/)
  assert.match(notebookSource, /className=\{`notebookEditor/)
  assert.match(notebookSource, /<MarkdownView/)
  assert.match(notebookSource, /aria-label="Ask agent-memory"/)
  assert.match(notebookSource, /Backlinks/)
  assert.match(notebookSource, /Outline/)
  assert.match(notebookSource, /Properties/)
})

test('new note naming reserves stored paths after a note title changes', () => {
  const source = /function nextUntitledTitle\(notes: NoteDocument\[\]\) \{([\s\S]*?)\n\}/.exec(notebookSource)
  assert.ok(source, 'nextUntitledTitle source should exist')
  const nextUntitledTitle = Function(`return function nextUntitledTitle(notes) {${source[1]}\n}`)()

  assert.equal(nextUntitledTitle([
    { title: 'NB-15 Browser QA', path: 'Untitled.md' },
  ]), 'Untitled 2')
})

test('the first Markdown line is the note title', () => {
  const source = /function noteTitleFromBody\(body: string, fallback: string\) \{([\s\S]*?)\n\}/.exec(notebookSource)
  assert.ok(source, 'noteTitleFromBody source should exist')
  const noteTitleFromBody = Function(`return function noteTitleFromBody(body, fallback) {${source[1]}\n}`)()

  assert.equal(noteTitleFromBody('# Quarterly plan\n\nDetails', 'Old title'), 'Quarterly plan')
  assert.equal(noteTitleFromBody('Customer interview notes\n\nDetails', 'Old title'), 'Customer interview notes')
  assert.equal(noteTitleFromBody('Status #\n\nDetails', 'Old title'), 'Status #')
  assert.equal(noteTitleFromBody('## Roadmap review ##\n\nDetails', 'Old title'), 'Roadmap review')
  assert.equal(noteTitleFromBody('\nDetails', 'Old title'), 'Old title')
  assert.doesNotMatch(notebookSource, /aria-label="Note title"/)
  assert.match(notebookSource, /patchActiveNote\(\{ body, title: noteTitleFromBody\(body, activeNote\.title\) \}\)/)
})

test('Ask results stay in a bounded document flow without covering the composer', () => {
  assert.match(stylesSource, /\.askWorkspace\s*\{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*overflow-x:\s*hidden;/s)
  assert.match(stylesSource, /\.askAnswer,\s*\.askScope,\s*\.askComposer\s*\{[^}]*width:\s*100%;[^}]*max-width:\s*820px;[^}]*min-width:\s*0;[^}]*box-sizing:\s*border-box;/s)
  assert.match(stylesSource, /\.askAnswer \.md\s*\{[^}]*overflow-wrap:\s*anywhere;/s)
  assert.doesNotMatch(stylesSource, /\.askComposer\s*\{[^}]*position:\s*sticky;/s)
})

test('notebook layout has responsive explorer and context behavior', () => {
  assert.match(stylesSource, /grid-template-columns:/)
  assert.match(stylesSource, /@media \(max-width: 1024px\)/)
  assert.match(stylesSource, /@media \(max-width: 640px\)/)
  assert.match(stylesSource, /prefers-reduced-motion/)
})

test('sidebar destinations use icons when collapsed and add names below icons when expanded', () => {
  assert.match(notebookSource, /className=\{`notebookShell \$\{explorerOpen \? 'railExpanded' : 'railCollapsed'\}`\}/)
  assert.match(notebookSource, /function RailIcon\(/)
  assert.match(notebookSource, /<RailIcon name=\{icon\} \/>/)
  assert.match(notebookSource, /<span className="railLabel">\{label\}<\/span>/)
  assert.doesNotMatch(notebookSource, /glyph="[NLSAR]"/)
  assert.match(stylesSource, /\.railLabel\s*\{[^}]*display:\s*none;/s)
  assert.match(stylesSource, /\.railExpanded \.railLabel\s*\{[^}]*display:\s*block;/s)
  assert.match(stylesSource, /\.railButton svg\s*\{[^}]*width:\s*20px;/s)
})
