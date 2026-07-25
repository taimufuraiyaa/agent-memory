import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/App.tsx', import.meta.url), 'utf8')
const notebookSource = await readFile(new URL('../src/ui/NotebookWorkspace.tsx', import.meta.url), 'utf8').catch(() => '')
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
