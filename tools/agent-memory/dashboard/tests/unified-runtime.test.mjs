import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const mainSource = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const runtimeSource = await readFile(new URL('../src/lib/runtime.ts', import.meta.url), 'utf8').catch(() => '')
const hostedSource = await readFile(new URL('../src/ui/HostedApp.tsx', import.meta.url), 'utf8').catch(() => '')

test('shared dashboard loads a versioned runtime manifest before mounting', () => {
  assert.match(runtimeSource, /agent-memory-dashboard-runtime-v1/)
  assert.match(runtimeSource, /fetch\('\/dashboard\/runtime\.json'/)
  assert.match(runtimeSource, /mode === 'standalone'/)
  assert.match(runtimeSource, /mode === 'hosted'/)
  assert.match(mainSource, /loadDashboardRuntime/)
  assert.match(mainSource, /runtime\.mode === 'hosted'/)
})

test('standalone keeps its rights gate and hosted mounts a separate authorized surface', () => {
  assert.match(mainSource, /<RightsAttestationGate>/)
  assert.match(mainSource, /<HostedApp runtime=\{runtime\}/)
  assert.match(hostedSource, /Hosted Agent Memory/)
})

test('invalid runtime discovery renders recovery instead of guessing a mode', () => {
  assert.match(mainSource, /Dashboard runtime unavailable/)
  assert.match(mainSource, /catch/)
  assert.doesNotMatch(runtimeSource, /return.*standalone.*catch/s)
})

