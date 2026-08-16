import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/HostedApp.tsx', import.meta.url), 'utf8')
const apiSource = await readFile(new URL('../src/lib/hostedApi.ts', import.meta.url), 'utf8').catch(() => '')

test('hosted connection stays in React memory and scopes every request', () => {
  assert.match(appSource, /useState<HostedConnection>/)
  assert.match(apiSource, /headers\.set\('Authorization', `Bearer \$\{connection\.token\}`\)/)
  assert.match(apiSource, /headers\.set\('X-Agent-Memory-Tenant', connection\.tenant\)/)
  assert.doesNotMatch(appSource + apiSource, /localStorage|sessionStorage/)
})

test('hosted surface preserves source custody, search, query, and proposal review', () => {
  for (const marker of ['Your sources', 'Memory context', 'Ask about this source', 'Keep as memory']) {
    assert.match(appSource, new RegExp(marker))
  }
  for (const path of ['/v1/sources', '/v1/search', '/v1/source-queries', '/v1/memory-proposals']) {
    assert.match(apiSource, new RegExp(path.replaceAll('/', '\\/')))
  }
  assert.match(appSource, /acceptHostedProposal/)
  assert.match(appSource, /rejectHostedProposal/)
})

test('hosted errors are bounded and response content is rendered by React', () => {
  assert.match(apiSource, /The request was not accepted/)
  assert.doesNotMatch(appSource, /dangerouslySetInnerHTML/)
  assert.doesNotMatch(appSource, /innerHTML/)
})

test('hosted surface preserves privacy billing export credential and deletion controls', () => {
  for (const marker of ['Privacy &amp; retention', 'Plan &amp; usage', 'Data export', 'Agent credentials', 'Delete hosted account']) {
    assert.match(appSource, new RegExp(marker))
  }
  for (const path of ['/v1/privacy', '/v1/billing', '/v1/exports', '/v1/credentials', '/v1/account']) {
    assert.match(apiSource, new RegExp(path.replaceAll('/', '\\/')))
  }
  assert.match(appSource, /window\.confirm/)
})
