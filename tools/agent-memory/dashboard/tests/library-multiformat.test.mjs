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

test('reader and library identities are system managed and hidden from the dashboard', () => {
  assert.doesNotMatch(librarySource, /Reader ID/)
  assert.doesNotMatch(librarySource, /Library ID/)
  assert.doesNotMatch(librarySource, /principalStorageKey|libraryStorageKey/)
  assert.doesNotMatch(librarySource, /principal_id:|library_id:/)
  assert.match(apiSource, /library_id\?: string/)
  assert.match(apiSource, /principal_id\?: string/)
  assert.match(apiSource, /if \(input\.library_id\) form\.append\('library_id', input\.library_id\)/)
  assert.match(apiSource, /if \(input\.principal_id\) form\.append\('principal_id', input\.principal_id\)/)
})

test('book language is selected from Unicode-safe language tags', () => {
  assert.match(librarySource, /const bookLanguageOptions =/)
  assert.match(librarySource, /\{ value: 'en', label: 'English' \}/)
  assert.match(librarySource, /\{ value: 'vi', label: 'Tiếng Việt' \}/)
  assert.match(librarySource, /\{ value: 'zh-Hans', label: '中文（简体）' \}/)
  assert.match(librarySource, /\{ value: 'ar', label: 'العربية' \}/)
  assert.match(librarySource, /\{ value: 'hi', label: 'हिन्दी' \}/)
  assert.match(librarySource, /Language[\s\S]*<select value=\{language\}/)
  assert.match(librarySource, /bookLanguageOptions\.map/)
  assert.match(librarySource, /language: language\.trim\(\)/)
  assert.doesNotMatch(librarySource, /<label>Language<input/)
})

test('Library starts with one customer-facing import action instead of numbered progress cards', () => {
  assert.match(librarySource, /className="libraryEmptyState"/)
  assert.match(librarySource, />Import a book</)
  assert.match(librarySource, /fileInputRef\.current\?\.click\(\)/)
  assert.doesNotMatch(librarySource, />1<\/span><h2>Import a whole book/)
  assert.doesNotMatch(librarySource, />2<\/span><h2>Book contents and index/)
  assert.doesNotMatch(librarySource, />3<\/span><h2>Talk with the book/)
})

test('selecting a book reveals editable metadata before explicit indexing confirmation', () => {
  assert.match(librarySource, /className="libraryImportSheet"/)
  assert.match(librarySource, /className="librarySelectedBook"/)
  assert.match(librarySource, /value=\{title\}/)
  assert.match(librarySource, /value=\{editionLabel\}/)
  assert.match(librarySource, /value=\{language\}/)
  assert.match(librarySource, />Start reading<\/button>/)
  assert.match(librarySource, /backgroundIndexStatus/)
})

test('ready Library is a book reader with a right index and bottom chat composer', () => {
  assert.match(librarySource, /className="libraryReadingRoom"/)
  assert.match(librarySource, /className="libraryBookBody"/)
  assert.match(librarySource, /className="libraryBookIndex"/)
  assert.match(librarySource, /aria-label="Book contents"/)
  assert.match(librarySource, /className="libraryConversation"/)
  assert.match(librarySource, /className="libraryChatComposer"/)
  assert.match(librarySource, /placeholder="Ask this book anything…"/)
  assert.match(librarySource, /className="libraryReaderGrid"/)
})

test('successful book preparation creates an ordinary removable notebook note', () => {
  assert.match(librarySource, /onBookImported: \(book: ImportedBookNoteInput\) => Promise<NoteDocument>/)
  assert.match(librarySource, /onOpenBookNote: \(noteID: string\) => void/)
  assert.match(librarySource, /const createdNote = await onBookImported\(\{/)
  assert.match(librarySource, /setImportedNote\(createdNote\)/)
  assert.match(librarySource, />Open note<\/button>/)
  assert.match(librarySource, /Book imported, but its note could not be created/)
})
