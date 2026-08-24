import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const panelSource = await readFile(new URL('../src/ui/ClientsPanel.tsx', import.meta.url), 'utf8')
const appSource = await readFile(new URL('../src/ui/workspace/SettingsView.tsx', import.meta.url), 'utf8')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const stylesSource = await readFile(new URL('../src/ui/clients.css', import.meta.url), 'utf8')

test('clients surface is installation-scoped and reachable from shared navigation', () => {
  assert.match(appSource, /label: 'Clients'/)
  assert.match(appSource, /id: 'clients'/)
  assert.match(appSource, /<ClientsPanel clientProfiles=\{gateway\}/)
  assert.match(panelSource, /across every workspace/)
  assert.doesNotMatch(panelSource, /workspace=/)
})

test('client profile API supports revision-safe lifecycle operations', () => {
  for (const name of ['listClientProfiles', 'createClientProfile', 'updateClientProfile', 'deleteClientProfile']) {
    assert.match(apiSource, new RegExp(`export function ${name}`))
    assert.match(panelSource, new RegExp(`clientProfiles\.${name}`))
  }
  assert.match(apiSource, /expected_revision/)
  assert.match(panelSource, /profile\.revision/)
})

test('profile selection explains tool membership, connection id, and restart behavior', () => {
  assert.match(panelSource, /Default/)
  assert.match(panelSource, /Expanded/)
	assert.match(panelSource, /13 workflow tools/)
	assert.match(panelSource, /15 tools/)
	assert.match(panelSource, /value: 'kiro'/)
  assert.match(panelSource, /AGENT_MEMORY_CLIENT_ID=/)
  assert.match(panelSource, /reconnects or restarts/)
  assert.match(panelSource, /navigator\.clipboard\.writeText/)
})

test('client profile layout has responsive and keyboard-visible controls', () => {
  assert.match(stylesSource, /@media \(max-width: 680px\)/)
  assert.match(stylesSource, /:focus/)
  assert.match(panelSource, /aria-live="polite"/)
  assert.match(panelSource, /role="alert"/)
})
