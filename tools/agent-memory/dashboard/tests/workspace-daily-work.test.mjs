import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const askSource = await readFile(new URL('../src/ui/workspace/AskView.tsx', import.meta.url), 'utf8').catch(() => '')
const memorySource = await readFile(new URL('../src/ui/workspace/MemoryExplorer.tsx', import.meta.url), 'utf8').catch(() => '')
const resultSource = await readFile(new URL('../src/ui/workspace/KnowledgeResultCard.tsx', import.meta.url), 'utf8').catch(() => '')
const appSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')

test('Ask keeps source evidence, durable memory, and weak context separate', () => {
  assert.match(askSource, /Source evidence/)
  assert.match(askSource, /Durable memory context/)
  assert.match(askSource, /Weak context/)
  assert.match(askSource, /gateway\.ask\(scope, question/)
  assert.match(askSource, /No grounded answer/)
  assert.match(appSource, /<AskView/)
  assert.match(askSource, /gateway\.listSources\(\{ workspaceId \}/)
  assert.match(askSource, /aria-label="Narrow Ask to source"/)
})

test('memory search explains ranking and continues with a cursor', () => {
  assert.match(memorySource, /gateway\.search\(scope, query, pageCursor/)
  assert.match(memorySource, /Load more/)
  assert.match(resultSource, /result\.explanation/)
  assert.match(resultSource, /result\.relevance/)
})

test('memory browse supports recent, pinned, and grouped types without a query', () => {
  assert.match(memorySource, /'recent'.*'pinned'.*'type'/s)
  assert.match(memorySource, /gateway\.browse\(scope, mode, pageCursor/)
  assert.match(memorySource, /groupByType/)
  assert.match(memorySource, /setMemoryPinned/)
  assert.match(memorySource, /deleteMemories/)
})

test('memory actions support selection, safe bulk export, print, and deletion', () => {
  assert.match(memorySource, /selectedIds/)
  assert.match(memorySource, /Export JSON/)
  assert.match(memorySource, /Print selected/)
  assert.match(memorySource, /Delete selected/)
  assert.match(memorySource, /window\.confirm/)
  assert.match(memorySource, /gateway\.deleteMemories/)
})

test('workspace changes abort stale Ask and memory requests', () => {
  assert.match(askSource, /AbortController/)
  assert.match(memorySource, /AbortController/)
  assert.match(askSource, /return \(\) => \{ sourceController\.abort\(\); controllerRef\.current\?\.abort\(\) \}/)
  assert.match(memorySource, /return \(\) => controllerRef\.current\?\.abort\(\)/)
})
