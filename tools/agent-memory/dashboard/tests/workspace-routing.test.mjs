import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const routeSource = await readFile(new URL('../src/ui/workspace/workspaceRoute.ts', import.meta.url), 'utf8').catch(() => '')
const cssSource = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8').catch(() => '')
const workspaceAppSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8').catch(() => '')

test('workspace routes preserve safe scope and destination', () => {
  assert.match(routeSource, /home.*ask.*knowledge.*activity.*settings/s)
  assert.match(routeSource, /\/w\/\$\{encodeURIComponent\(workspaceId\)\}/)
  assert.match(routeSource, /\(chat\|search\)/)
  assert.match(routeSource, /panel=\$\{route\.panel\}/)
  assert.match(routeSource, /history\.pushState/)
  assert.match(routeSource, /decodeURIComponent/)
})

test('workspace shell has explicit desktop, tablet, and mobile layouts', () => {
  assert.match(workspaceAppSource, /navbar=\{\{ width: 288, breakpoint: 'sm'/)
  assert.match(workspaceAppSource, /<Drawer[^>]*hiddenFrom="sm"/s)
  assert.match(cssSource, /@media \(max-width: 900px\)/)
  assert.match(cssSource, /@media \(max-width: 600px\)/)
  assert.match(cssSource, /min-width:\s*0/)
})

test('wide workspace canvas uses the available main-column width', () => {
  assert.match(cssSource, /\.workspaceCanvas\s*\{[^}]*width:\s*100%[^}]*max-width:\s*none/s)
  assert.doesNotMatch(cssSource, /\.workspaceCanvas\s*\{[^}]*min\(1500px,\s*100%\)/s)
})

test('direct workspace routes wait for discovery before mounting scoped views', () => {
  assert.match(workspaceAppSource, /const workspaceReady = Boolean\(workspace\)/)
  assert.match(workspaceAppSource, /!workspaceReady \? <Paper[\s\S]*Loading workspace/)
  assert.match(workspaceAppSource, /workspace && activeChat && gateway\.supports\('ask'/)
  assert.match(workspaceAppSource, /workspace && route\.workItemType === 'search' && activeSearch && gateway\.supports\('search'/)
  for (const destination of ["route.destination === 'ask'", "route.destination === 'knowledge' && route.knowledgeView === 'memories'", "route.destination === 'knowledge' && route.knowledgeView === 'history'", "route.destination === 'knowledge' && route.knowledgeView === 'sources'", "route.destination === 'knowledge' && route.knowledgeView === 'notes'", "route.destination === 'activity'", "route.destination === 'settings'"]) {
    assert.ok(workspaceAppSource.includes(`workspace && !activeWorkItem && ${destination}`), `Missing workspace-scoped route guard for ${destination}`)
  }
  assert.match(workspaceAppSource, /workspace \? <SourceImportDialog/)
})
