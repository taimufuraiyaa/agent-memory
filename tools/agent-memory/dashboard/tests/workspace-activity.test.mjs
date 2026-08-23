import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const root = new URL('../src/', import.meta.url)
const read = (path) => readFile(new URL(path, root), 'utf8')

test('Activity is one workspace-scoped timeline with filters and paging', async () => {
  const source = await read('ui/workspace/ActivityView.tsx')
  for (const label of ['Study', 'Uploads', 'Indexing', 'Sessions', 'Retrieval', 'Feedback', 'Deletion', 'Load more activity']) assert.match(source, new RegExp(label))
  assert.match(source, /gateway\.listActivity\(\{ workspaceId \}, nextCursor\)/)
})

test('failed work can retry and retrievals accept scored feedback', async () => {
  const source = await read('ui/workspace/ActivityView.tsx')
  assert.match(source, /gateway\.retryActivity/)
  assert.match(source, /gateway\.submitFeedback/)
  assert.match(source, /Score \(0–5\)/)
  assert.match(source, /replace\(\/\^retrieval:/)
})
