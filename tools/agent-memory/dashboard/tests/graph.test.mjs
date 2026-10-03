import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const read = (path) => readFile(new URL(`../src/${path}`, import.meta.url), 'utf8')
const [contract, standalone, settings, explorer, review, ask, context, chat, records, api] = await Promise.all([
  read('lib/knowledgeGateway.ts'),
  read('lib/adapters/standaloneKnowledgeGateway.ts'),
  read('ui/workspace/GraphSettings.tsx'),
  read('ui/workspace/GraphExplorer.tsx'),
  read('ui/workspace/GraphReview.tsx'),
  read('ui/workspace/AskView.tsx'),
  read('ui/workspace/GraphContext.tsx'),
  read('ui/workspace/WorkspaceChatView.tsx'),
  read('ui/workspace/workspaceRecords.ts'),
  read('lib/api.ts'),
])

test('local graph controls expose compatible, fresh, bounded processing state', () => {
  for (const operation of ['getGraphReadiness', 'getGraphStatus', 'getGraphSnapshot', 'operateGraph', 'reviewGraph', 'submitGraphFeedback']) assert.match(contract, new RegExp(`${operation}\\(`))
  assert.match(standalone, /getGraphReadiness/)
  for (const state of ['Adapter', 'Revision', 'Pending', 'Queue age', 'Watermark', 'Last success', 'Compatibility', 'Cost']) assert.match(settings, new RegExp(state))
  assert.match(settings, /Basic retrieval remains available/)
})

test('graph explorer preserves trust, provenance, ambiguity, and optimistic review versions', () => {
  assert.match(explorer, /Canonical evidence/)
  assert.match(explorer, /Navigation summary — not source evidence/)
  assert.match(explorer, /ambiguous carry-forward/)
  assert.match(explorer, /trust !== 'rejected'/)
  assert.match(explorer, /record_version/)
  assert.match(explorer, /review_version/)
  for (const action of ['approve', 'reject', 'supersede', 'annotate', 'reconsider']) assert.match(review, new RegExp(`'${action}'`))
})

test('Ask defaults to Basic and makes graph routes, fallback, paths, coverage, and feedback explicit', () => {
  assert.match(ask, /useState<GraphQueryMode>\('basic'\)/)
  for (const route of ['Basic', 'Auto', 'Local Graph', 'Global']) assert.match(ask, new RegExp(route))
  assert.match(ask, /graphMode === 'local_graph' \|\| graphMode === 'global'/)
  for (const signal of ['Fell back to Basic', 'Relationship paths', 'Conflicting relationships', 'Community coverage', 'Canonical citation']) assert.match(context, new RegExp(signal))
  assert.match(context, /Navigation summaries — not source evidence/)
  assert.match(context, /submitGraphFeedback/)
})

test('workspace chat uses Auto Graph and restores bounded graph context with the answer', () => {
  assert.match(chat, /gateway\.ask\(scope, turn\.question, \{ mode: 'auto' \}/)
  assert.match(chat, /<GraphContext/)
  assert.match(chat, /supports\('graph', \{ workspaceId \}\)/)
  assert.match(records, /'graphRoute' \| 'graphContext'/)
  assert.match(records, /sanitizeGraphRoute/)
  assert.match(records, /sanitizeGraphContext/)
})

test('Graph Settings labels the retained status stale when a refresh fails', () => {
  assert.match(settings, /const \[statusStale, setStatusStale\] = useState\(false\)/)
  assert.match(settings, /setStatusStale\(true\)/)
  assert.match(settings, /setStatusStale\(false\)/)
  assert.match(settings, /statusStale \? `Stale · \$\{status\.state\}` : status\.state/)
  assert.match(settings, /status\.state === 'running' \? 'job' : 'queue'/)
})

test('graph read requests include the backend-required default configuration identity', () => {
  assert.match(api, /function graphQuery\(scope: \{ workspaceId: string \}, configurationId = 'default'\)/)
  assert.match(api, /query\.set\('configuration_id', configurationId\)/)
})
