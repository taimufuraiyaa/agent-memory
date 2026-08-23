import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/lib/knowledgeGateway.ts', import.meta.url), 'utf8').catch(() => '')

test('knowledge gateway exposes every unified workspace capability', () => {
  for (const capability of ['workspace', 'ask', 'search', 'browse', 'source', 'study', 'note', 'activity', 'settings']) {
    assert.match(source, new RegExp(`'${capability}'`))
  }
  assert.match(source, /export interface KnowledgeGateway/)
  assert.match(source, /capabilities: ReadonlySet<KnowledgeCapability>/)
})

test('unsupported capabilities fail explicitly', () => {
  assert.match(source, /class UnsupportedCapabilityError extends Error/)
  assert.match(source, /requireCapability/)
  assert.doesNotMatch(source, /as any/)
})

test('scope contracts use stable workspace and source IDs without browser paths', () => {
  assert.match(source, /workspaceId: string/)
  assert.match(source, /sourceId\?: string/)
  assert.doesNotMatch(source, /dbPath|workspacePath|filesystemPath/)
})
