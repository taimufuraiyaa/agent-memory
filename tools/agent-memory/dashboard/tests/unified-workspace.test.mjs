import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const workspaceAppSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8').catch(() => '')
const mainSource = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')

test('canonical workspace shell freezes one navigation and scope language', () => {
  for (const destination of ['Home', 'Ask', 'Knowledge', 'Activity', 'Settings']) {
    assert.match(workspaceAppSource, new RegExp(`label: '${destination}'`))
  }
  assert.match(workspaceAppSource, /aria-label="Workspace"/)
  assert.match(workspaceAppSource, /data-workspace-picker/)
})

test('Knowledge owns Sources, Memories, and Notes', () => {
  assert.match(workspaceAppSource, /value: 'sources', label: 'Sources'/)
  assert.match(workspaceAppSource, /value: 'memories', label: 'Memories'/)
  assert.match(workspaceAppSource, /value: 'notes', label: 'Notes'/)
})

test('both runtimes are inputs to one shell instead of separate navigation contracts', () => {
  assert.match(workspaceAppSource, /DashboardRuntime/)
  assert.match(workspaceAppSource, /KnowledgeGateway/)
  assert.doesNotMatch(workspaceAppSource, /HostedApp|<App\s/)
})

test('canonical shell is the only mounted workspace without changing stored data', () => {
  assert.doesNotMatch(mainSource, /VITE_UNIFIED_WORKSPACE_ENABLED|<HostedApp|<App\s/)
  assert.match(mainSource, /<WorkspaceApp runtime=\{runtime\} gateway=\{gateway\}/)
  assert.match(mainSource, /createStandaloneKnowledgeGateway/)
  assert.match(mainSource, /<HostedWorkspaceBootstrap runtime=\{runtime\}/)
})
