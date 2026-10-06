import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const mainSource = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const workspaceAppSource = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')
const explorerSource = await readFile(new URL('../src/ui/workspace/WorkspaceExplorer.tsx', import.meta.url), 'utf8')
const chatSource = await readFile(new URL('../src/ui/workspace/WorkspaceChatView.tsx', import.meta.url), 'utf8')
const packageManifest = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'))
const themeSource = await readFile(new URL('../src/ui/theme.ts', import.meta.url), 'utf8').catch(() => '')

test('one Mantine provider and theme wrap both dashboard runtimes', () => {
  assert.match(mainSource, /@mantine\/core\/styles\.css/)
  assert.match(mainSource, /MantineProvider/)
  assert.match(mainSource, /agentMemoryTheme/)
  assert.match(themeSource, /createTheme/)
  assert.match(themeSource, /primaryColor:\s*'memory'/)
})

test('the canonical shell uses Mantine application primitives', () => {
  for (const primitive of ['AppShell', 'Button', 'Drawer']) assert.match(workspaceAppSource, new RegExp(`\\b${primitive}\\b`))
  for (const primitive of ['Menu', 'ScrollArea', 'UnstyledButton']) assert.match(explorerSource, new RegExp(`\\b${primitive}\\b`))
  for (const primitive of ['Textarea', 'Modal', 'Button']) assert.match(chatSource, new RegExp(`\\b${primitive}\\b`))
  assert.doesNotMatch(chatSource, /\bSelect\b/)
  assert.match(workspaceAppSource, /useDisclosure/)
})

test('Mantine dependencies are explicit and locally bundled', () => {
  assert.ok(packageManifest.dependencies['@mantine/core'])
  assert.ok(packageManifest.dependencies['@mantine/hooks'])
  assert.ok(packageManifest.dependencies['@tabler/icons-react'])
  assert.doesNotMatch(mainSource, /https?:\/\//)
})
