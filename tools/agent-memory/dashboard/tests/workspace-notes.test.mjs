import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/ui/workspace/NotesView.tsx', import.meta.url), 'utf8').catch(() => '')
const appSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')

test('Notes uses the unified gateway for scoped editing', () => {
  assert.match(source, /gateway\.listNotes/)
  assert.match(source, /gateway\.getNote/)
  assert.match(source, /gateway\.createNote/)
  assert.match(source, /gateway\.updateNote/)
  assert.match(source, /Markdown preview/)
  assert.match(appSource, /<NotesView/)
})

test('Notes keeps Trash and destructive confirmation inside Knowledge', () => {
  assert.match(source, /gateway\.trashNote/)
  assert.match(source, /gateway\.restoreNote/)
  assert.match(source, /gateway\.deleteNote/)
  assert.match(source, /Permanently delete/)
  assert.match(source, />Trash</)
})

test('failed note indexing offers an explicit retry', () => {
  assert.match(source, /indexState === 'failed'/)
  assert.match(source, /gateway\.retryNoteIndex/)
  assert.match(source, /Retry indexing/)
})
