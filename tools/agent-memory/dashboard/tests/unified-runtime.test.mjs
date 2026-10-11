import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const mainSource = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const runtimeSource = await readFile(new URL('../src/lib/runtime.ts', import.meta.url), 'utf8').catch(() => '')

test('shared dashboard loads a versioned runtime manifest before mounting', () => {
  assert.match(runtimeSource, /agent-memory-dashboard-runtime-v1/)
  assert.match(runtimeSource, /fetch\('\/dashboard\/runtime\.json'/)
  assert.match(runtimeSource, /mode !== 'standalone'/)
  assert.match(mainSource, /loadDashboardRuntime/)
  assert.match(mainSource, /createStandaloneKnowledgeGateway/)
})

test('standalone keeps its rights gate and canonical workspace shell', () => {
  assert.match(mainSource, /<RightsAttestationGate>/)
  assert.match(mainSource, /<WorkspaceApp runtime=\{runtime\} gateway=\{gateway\}/)
})

test('invalid runtime discovery renders recovery instead of guessing a mode', () => {
  assert.match(mainSource, /Dashboard runtime unavailable/)
  assert.match(mainSource, /catch/)
  assert.doesNotMatch(runtimeSource, /return.*standalone.*catch/s)
})
