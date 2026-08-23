import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const sourcesSource = await readFile(new URL('../src/ui/workspace/SourcesView.tsx', import.meta.url), 'utf8').catch(() => '')
const importSource = await readFile(new URL('../src/ui/workspace/SourceImportDialog.tsx', import.meta.url), 'utf8').catch(() => '')
const appSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')

test('Sources combines codebase, document, and note inventory states', () => {
  assert.match(sourcesSource, /gateway\.listSources/)
  assert.match(sourcesSource, /codebase.*document.*note/s)
  for (const state of ['uploading', 'parsing', 'ocr-required', 'ocr-processing', 'indexing', 'ready', 'failed']) assert.match(sourcesSource, new RegExp(`'${state}'`))
  assert.match(sourcesSource, /window\.setInterval/)
  assert.match(sourcesSource, /gateway\.deleteSource/)
  assert.match(appSource, /<SourcesView/)
})

test('one Add source dialog declares formats, rights, and workspace scope', () => {
  assert.match(importSource, /PDF/)
  assert.match(importSource, /EPUB/)
  assert.match(importSource, /Markdown/)
  assert.match(importSource, /plain text/)
  assert.match(importSource, /Rights basis/)
  assert.match(importSource, /gateway\.uploadSource/)
  assert.match(importSource, /scope: \{ workspaceId \}/)
  assert.match(importSource, /OCR required/)
})

test('codebase Study preserves preview, bounded pages, errors, and continuation', () => {
  assert.match(sourcesSource, /maxFiles.*200/)
  assert.match(sourcesSource, /Preview without writing/)
  assert.match(sourcesSource, /runStudy\(result\.nextOffset\)/)
  assert.match(sourcesSource, /Previous batch/)
  assert.match(sourcesSource, /Continue study/)
  assert.match(sourcesSource, /result\.errors/)
})

test('study completion leads directly to Ask, Search, and Browse while preview can be written', () => {
  assert.match(sourcesSource, /Ask this workspace/)
  assert.match(sourcesSource, /Search memories/)
  assert.match(sourcesSource, /Browse memories/)
  assert.match(sourcesSource, /Write this batch/)
  assert.match(sourcesSource, /onNavigate/)
})

test('source failures override a stale ready label in the UI', () => {
  assert.match(sourcesSource, /source\.failure \? 'Needs attention'/)
  assert.match(sourcesSource, /stateColor\(source\.state, Boolean\(source\.failure\)\)/)
  assert.match(sourcesSource, /Resolve this source failure before relying on it/)
})
