import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const librarySource = await readFile(new URL('../src/ui/LibraryWorkspace.tsx', import.meta.url), 'utf8')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')

test('library file picker accepts every locally supported book format', () => {
  assert.match(librarySource, /accept="\.pdf,\.epub,\.md,\.markdown,\.txt/)
  assert.match(librarySource, /PDF · EPUB · Markdown · plain text/)
  assert.match(librarySource, /sourceFile/)
  assert.match(librarySource, /sourceFile\.size/)
  assert.match(librarySource, /PDF and EPUB stay binary/)
})

test('binary book imports use multipart without overriding its boundary', () => {
  assert.match(apiSource, /export type LibraryFileImportRequest/)
  assert.match(apiSource, /source_file: File/)
  assert.match(apiSource, /new FormData\(\)/)
  assert.match(apiSource, /form\.append\('source', input\.source_file\)/)
  assert.match(apiSource, /body: form/)
  assert.match(apiSource, /init\?\.body instanceof FormData/)
})

test('legacy pasted Markdown import remains available', () => {
  assert.match(apiSource, /markdown: string/)
  assert.match(librarySource, /Paste Markdown or plain text/)
  assert.match(librarySource, /markdown: source/)
})
