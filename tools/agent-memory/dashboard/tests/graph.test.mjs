import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const contract = await readFile(new URL('../src/lib/knowledgeGateway.ts', import.meta.url), 'utf8')
const standalone = await readFile(new URL('../src/lib/adapters/standaloneKnowledgeGateway.ts', import.meta.url), 'utf8')
const hosted = await readFile(new URL('../src/lib/adapters/hostedKnowledgeGateway.ts', import.meta.url), 'utf8')
const hostedApi = await readFile(new URL('../src/lib/hostedApi.ts', import.meta.url), 'utf8')
const settings = await readFile(new URL('../src/ui/workspace/GraphSettings.tsx', import.meta.url), 'utf8')
const explorer = await readFile(new URL('../src/ui/workspace/GraphExplorer.tsx', import.meta.url), 'utf8')
const review = await readFile(new URL('../src/ui/workspace/GraphReview.tsx', import.meta.url), 'utf8')
const ask = await readFile(new URL('../src/ui/workspace/AskView.tsx', import.meta.url), 'utf8')
const context = await readFile(new URL('../src/ui/workspace/GraphContext.tsx', import.meta.url), 'utf8')

test('graph index controls expose compatible, fresh, bounded processing state in both runtimes', () => {
  for (const operation of ['getGraphReadiness', 'getGraphStatus', 'getGraphSnapshot', 'operateGraph', 'reviewGraph', 'submitGraphFeedback']) assert.match(contract, new RegExp(`${operation}\\(`))
  assert.match(standalone, /getGraphReadiness/)
  assert.match(hosted, /getHostedGraphReadiness/)
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

test('Ask defaults to Basic and makes graph routes, fallback, paths, conflicts, coverage, and feedback explicit', () => {
  assert.match(ask, /useState<GraphQueryMode>\('basic'\)/)
  for (const route of ['Basic', 'Auto', 'Local Graph', 'Global']) assert.match(ask, new RegExp(route))
  assert.match(ask, /graphMode === 'local_graph' \|\| graphMode === 'global'/)
  for (const signal of ['Fell back to Basic', 'Relationship paths', 'Conflicting relationships', 'Community coverage', 'Canonical citation']) assert.match(context, new RegExp(signal))
  assert.match(context, /Navigation summaries — not source evidence/)
  assert.match(context, /submitGraphFeedback/)
  assert.match(hostedApi, /recallHostedGraph/)
  assert.match(hosted, /recallHostedGraph/)
  assert.doesNotMatch(hosted, /Graph-enriched Ask is unavailable/)
})
