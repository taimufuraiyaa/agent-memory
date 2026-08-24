import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const main = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const hostedBootstrap = await readFile(new URL('../src/ui/HostedWorkspaceBootstrap.tsx', import.meta.url), 'utf8')
const workspaceApp = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')
const workspaceCss = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')

test('the shared provider restores and persists a controlled color scheme', () => {
  assert.match(main, /agent-memory:color-scheme/)
  assert.match(main, /value === 'light' \|\| value === 'dark'/)
  assert.match(main, /useState<ColorScheme>/)
  assert.match(main, /localStorage\.getItem/)
  assert.match(main, /localStorage\.setItem/)
  assert.match(main, /defaultColorScheme="dark"/)
  assert.match(main, /forceColorScheme=\{colorScheme\}/)
  assert.doesNotMatch(main, /forceColorScheme="dark"/)
})

test('both runtimes receive an accessible theme action in the canonical shell', () => {
  assert.match(main, /<HostedWorkspaceBootstrap[^>]*colorScheme=\{colorScheme\}[^>]*onColorSchemeChange=\{setColorScheme\}/s)
  assert.match(main, /<WorkspaceApp[^>]*colorScheme=\{colorScheme\}[^>]*onColorSchemeChange=\{setColorScheme\}/s)
  assert.match(hostedBootstrap, /<WorkspaceApp[^>]*colorScheme=\{colorScheme\}[^>]*onColorSchemeChange=\{onColorSchemeChange\}/s)
  assert.match(workspaceApp, /aria-label=\{`Switch to \$\{colorScheme === 'dark' \? 'light' : 'dark'\} theme`\}/)
  assert.match(workspaceApp, /IconSun/)
  assert.match(workspaceApp, /IconMoon/)
})

test('workspace-specific surfaces use semantic tokens with light overrides', () => {
  assert.match(workspaceCss, /--workspace-canvas:/)
  assert.match(workspaceCss, /--workspace-card:/)
  assert.match(workspaceCss, /body\.light \.workspaceApp/)
  assert.match(workspaceCss, /background: var\(--workspace-canvas\)/)
  assert.match(workspaceCss, /background: var\(--workspace-card\)/)
})

