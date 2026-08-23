import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/ui/workspace/SettingsView.tsx', import.meta.url), 'utf8')

test('primary settings keep account, data, and access ahead of System', () => {
  assert.match(source, /\['account', 'data', 'access', 'system'\]/)
  assert.match(source, /gateway\.getSettings\(\{ workspaceId \}/)
})

test('advanced tools live in one capability-aware System registry', () => {
  for (const tool of ['Diagnostics', 'Lifecycle', 'Benchmark', 'Clients', 'Skills', 'Infrastructure', 'Migration']) assert.match(source, new RegExp(tool))
  assert.match(source, /tool\.runtimes\.includes\(gateway\.runtime\)/)
  assert.match(source, /Unavailable in/)
})
