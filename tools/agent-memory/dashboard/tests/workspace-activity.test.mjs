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

test('feedback cards open full retrieval details without losing Activity context', async () => {
  const [view, contract, standalone, hosted] = await Promise.all([
    read('ui/workspace/ActivityView.tsx'),
    read('lib/knowledgeGateway.ts'),
    read('lib/adapters/standaloneKnowledgeGateway.ts'),
    read('lib/adapters/hostedKnowledgeGateway.ts'),
  ])
  assert.match(contract, /feedback\?: \{[\s\S]*requestId:[\s\S]*requestType:[\s\S]*query:[\s\S]*score:[\s\S]*reason:[\s\S]*usefulCount\?:[\s\S]*totalCount\?:/)
  for (const adapter of [standalone, hosted]) {
    for (const field of ['requestId', 'requestType', 'query', 'score', 'reason', 'usefulCount', 'totalCount']) assert.match(adapter, new RegExp(field))
  }
  assert.match(view, /<Drawer[\s\S]*title="Feedback details"/)
  assert.match(view, /Open feedback details for \$\{item\.title\}/)
  for (const label of ['Question / task', 'Quality score', 'Feedback reason', 'Useful hits', 'Total hits', 'Request type', 'Request ID', 'Logged time']) assert.match(view, new RegExp(label))
})
