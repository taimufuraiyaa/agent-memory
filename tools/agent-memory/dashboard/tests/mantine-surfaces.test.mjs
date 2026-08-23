import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

async function source(name) {
  return readFile(new URL(`../src/ui/workspace/${name}`, import.meta.url), 'utf8')
}

const sourcesView = await source('SourcesView.tsx')
const importDialog = await source('SourceImportDialog.tsx')
const askView = await source('AskView.tsx')
const memoryExplorer = await source('MemoryExplorer.tsx')
const resultCard = await source('KnowledgeResultCard.tsx')
const homeView = await source('HomeView.tsx')
const notesView = await source('NotesView.tsx')
const activityView = await source('ActivityView.tsx')
const settingsView = await source('SettingsView.tsx')

function requires(sourceText, primitives) {
  for (const primitive of primitives) assert.match(sourceText, new RegExp(`\\b${primitive}\\b`))
}

test('Sources and Add source use the shared Mantine interaction system', () => {
  requires(sourcesView, ['Alert', 'Badge', 'Button', 'NumberInput', 'Paper', 'Select', 'SimpleGrid', 'Switch'])
  requires(importDialog, ['Alert', 'Button', 'FileInput', 'Modal', 'Select', 'SegmentedControl'])
  assert.doesNotMatch(importDialog, /sourceDialogBackdrop/)
})

test('Ask and Memories share Mantine discovery and result primitives', () => {
  requires(askView, ['Alert', 'Button', 'Paper', 'Select', 'Textarea'])
  requires(memoryExplorer, ['Alert', 'Button', 'Checkbox', 'Drawer', 'Loader', 'SegmentedControl', 'TextInput'])
  requires(resultCard, ['ActionIcon', 'Badge', 'Paper'])
})

test('Home, Notes, Activity, and Settings use the shared Mantine surface language', () => {
  requires(homeView, ['Button', 'Card', 'SimpleGrid', 'ThemeIcon'])
  requires(notesView, ['Alert', 'Button', 'Paper', 'SegmentedControl', 'TextInput', 'Textarea'])
  requires(activityView, ['Alert', 'Badge', 'Button', 'Card', 'Progress', 'SegmentedControl'])
  requires(settingsView, ['Alert', 'Card', 'NavLink', 'Paper', 'Tabs'])
})

test('migrated daily-work surfaces no longer render bespoke native buttons', () => {
  for (const surface of [sourcesView, importDialog, askView, memoryExplorer, resultCard, homeView, notesView, activityView, settingsView]) {
    assert.doesNotMatch(surface, /<button\b/)
  }
})

