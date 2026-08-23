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
