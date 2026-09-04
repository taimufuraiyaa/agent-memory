import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const routeSource = await readFile(new URL('../src/ui/workspace/workspaceRoute.ts', import.meta.url), 'utf8').catch(() => '')
const cssSource = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8').catch(() => '')
const workspaceAppSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8').catch(() => '')

test('workspace routes preserve safe scope and destination', () => {
  assert.match(routeSource, /home.*ask.*knowledge.*activity.*settings/s)
  assert.match(routeSource, /\/w\/\$\{encodeURIComponent\(workspaceId\)\}/)
  assert.match(routeSource, /history\.pushState/)
  assert.match(routeSource, /decodeURIComponent/)
})

test('workspace shell has explicit desktop, tablet, and mobile layouts', () => {
  assert.match(workspaceAppSource, /navbar=\{\{ width: 248, breakpoint: 'sm'/)
  assert.match(workspaceAppSource, /<Drawer[^>]*hiddenFrom="sm"/s)
  assert.match(cssSource, /@media \(max-width: 900px\)/)
  assert.match(cssSource, /@media \(max-width: 520px\)/)
  assert.match(cssSource, /min-width:\s*0/)
})

test('wide workspace canvas uses the available main-column width', () => {
  assert.match(cssSource, /\.workspaceCanvas\s*\{[^}]*width:\s*100%[^}]*max-width:\s*none/s)
  assert.doesNotMatch(cssSource, /\.workspaceCanvas\s*\{[^}]*min\(1500px,\s*100%\)/s)
})

test('direct workspace routes wait for discovery before mounting scoped views', () => {
  assert.match(workspaceAppSource, /const workspaceReady = Boolean\(workspace\)/)
  for (const view of ['AskView', 'MemoryExplorer', 'SourcesView', 'NotesView', 'ActivityView', 'SettingsView', 'SourceImportDialog']) {
    assert.match(workspaceAppSource, new RegExp(`workspaceReady \\? <${view}`))
  }
})
