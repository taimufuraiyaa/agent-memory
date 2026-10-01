import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const contract = await readFile(new URL('../src/lib/knowledgeGateway.ts', import.meta.url), 'utf8')
const standalone = await readFile(new URL('../src/lib/adapters/standaloneKnowledgeGateway.ts', import.meta.url), 'utf8')
const hosted = await readFile(new URL('../src/lib/adapters/hostedKnowledgeGateway.ts', import.meta.url), 'utf8')
const hostedApi = await readFile(new URL('../src/lib/hostedApi.ts', import.meta.url), 'utf8')
const settings = await readFile(new URL('../src/ui/workspace/GraphSettings.tsx', import.meta.url), 'utf8')
const queue = await readFile(new URL('../src/ui/workspace/ProjectGraphQueue.tsx', import.meta.url), 'utf8')
const explorer = await readFile(new URL('../src/ui/workspace/GraphExplorer.tsx', import.meta.url), 'utf8')
const review = await readFile(new URL('../src/ui/workspace/GraphReview.tsx', import.meta.url), 'utf8')
const ask = await readFile(new URL('../src/ui/workspace/AskView.tsx', import.meta.url), 'utf8')
const context = await readFile(new URL('../src/ui/workspace/GraphContext.tsx', import.meta.url), 'utf8')
const chat = await readFile(new URL('../src/ui/workspace/WorkspaceChatView.tsx', import.meta.url), 'utf8')
const records = await readFile(new URL('../src/ui/workspace/workspaceRecords.ts', import.meta.url), 'utf8')

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

test('workspace chat uses Auto Graph and restores bounded graph context with the answer', () => {
  assert.match(chat, /gateway\.ask\(scope, turn\.question, \{ mode: 'auto' \}/)
  assert.match(chat, /<GraphContext/)
  assert.match(chat, /supports\('graph', \{ workspaceId \}\)/)
  assert.match(records, /'graphRoute' \| 'graphContext'/)
  assert.match(records, /sanitizeGraphRoute/)
  assert.match(records, /sanitizeGraphContext/)
})

test('hosted-connected project chat uses project recall and the explorer omits a synthetic host workspace', () => {
  assert.match(hostedApi, /askHostedProject/)
  assert.match(hosted, /askHostedProject/)
  assert.match(hosted, /if \(isRegisteredProject\(scope\.workspaceId\)\)/)
  assert.doesNotMatch(hosted, /name: 'Hosted workspace'/)
})

test('registered projects expose owner-scoped Graph status and an explicit Reindex action', () => {
  assert.match(hosted, /if \(isRegisteredProject\(scope\.workspaceId\)\)/)
  assert.match(hosted, /getHostedLocalProjectGraphReadiness/)
  assert.match(hosted, /getHostedLocalProjectGraphStatus/)
  assert.match(hosted, /reindexHostedLocalProjectGraph/)
  assert.match(hostedApi, /\/v1\/local-projects\/graph-index\/readiness/)
  assert.match(hostedApi, /\/v1\/local-projects\/graph-index\/operations/)
  assert.match(settings, /localProject \? <Button/)
  assert.match(settings, />Reindex<\/Button>/)
})

test('registered-project Graph Settings expose the cross-project active queue without mutation controls', () => {
  assert.match(hostedApi, /getHostedLocalProjectGraphQueue/)
  assert.match(hosted, /getHostedLocalProjectGraphQueue/)
  assert.match(queue, /Queued/)
  assert.match(queue, /Running/)
  assert.match(queue, /Queued for|Waiting for a worker/)
  assert.match(queue, /being processed/)
  assert.match(queue, /projects_scanned/)
  assert.match(queue, /projects_unavailable/)
  assert.match(queue, /window\.setInterval/)
  assert.match(queue, /queue\?\.legacy_fallback \? 30000 : queue\?\.jobs\.length \? 3000 : 15000/)
  assert.match(queue, /error \? <Text[^>]*>Queue status has not loaded\./)
  assert.match(hostedApi, /The server returned HTTP \$\{response\.status\}\./)
  assert.match(hostedApi, /The request was not accepted \(HTTP \$\{response\.status\}\)\./)
  assert.match(settings, /<ProjectGraphQueue/)
  assert.doesNotMatch(queue, /operateGraph|rebuild|cancel/)
})

test('an older local API queue 404 falls back to bounded registered-project status reads', () => {
  assert.match(hostedApi, /class HostedRequestError extends Error/)
  assert.match(hostedApi, /error instanceof HostedRequestError && error\.status === 404/)
  assert.match(hostedApi, /listHostedProjects\(connection, signal\)/)
  assert.match(hostedApi, /const concurrency = 4/)
  assert.match(hostedApi, /getHostedLocalProjectGraphStatus\(connection, project\.name, signal\)/)
  assert.match(hostedApi, /job\.state !== 'queued' && job\.state !== 'running'/)
  assert.match(hostedApi, /unavailable_project_names/)
  assert.match(hostedApi, /legacy_fallback: true/)
})

test('Graph Settings labels the retained status stale when a refresh fails', () => {
  assert.match(settings, /const \[statusStale, setStatusStale\] = useState\(false\)/)
  assert.match(settings, /setStatusStale\(true\)/)
  assert.match(settings, /setStatusStale\(false\)/)
  assert.match(settings, /statusStale \? `Stale · \$\{status\.state\}` : status\.state/)
  assert.match(settings, /status\.state === 'running' \? 'job' : 'queue'/)
})
