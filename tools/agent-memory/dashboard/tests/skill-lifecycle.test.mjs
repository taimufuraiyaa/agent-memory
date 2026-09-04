import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const panel = await readFile(new URL('../src/ui/SkillsPanel.tsx', import.meta.url), 'utf8')
const settings = await readFile(new URL('../src/ui/workspace/SettingsView.tsx', import.meta.url), 'utf8')
const css = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')
const api = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const hosted = await readFile(new URL('../src/lib/hostedApi.ts', import.meta.url), 'utf8')

test('skills UI distinguishes immutable revision roles and provenance', () => {
  for (const label of ['Latest', 'Active', 'Canary', 'Last known good', 'Provenance', 'Evaluation']) assert.match(panel, new RegExp(label))
  assert.match(panel, /source_memory_ids/)
  assert.match(panel, /source_tool_lesson_ids/)
  assert.match(panel, /source_episode_ids/)
  assert.match(panel, /\|\| 'N\/A'/)
})

test('approval and rollback are explicit guarded operations', () => {
  assert.match(panel, /Approve latest/)
  assert.match(panel, /Rollback to last known good/)
  assert.match(panel, /disabled=\{acting/)
  assert.match(settings, /approval-required policy decision/)
  assert.match(settings, /expected_generation: activation\.generation/)
  assert.match(settings, /idempotency_key: crypto\.randomUUID\(\)/)
})

test('standalone and hosted transports expose lifecycle parity', () => {
  for (const route of ['/api/v1/skills/lifecycle/list', '/api/v1/skills/inspect', '/api/v1/skills/lifecycle']) assert.ok(api.includes(route))
  assert.match(hosted, /\/v1\/local-project-skills\/lifecycle/)
})

test('skills lifecycle remains keyboard and narrow-screen accessible', () => {
  assert.match(panel, /aria-label="Revision-managed skills"/)
  assert.match(panel, /aria-live="polite"/)
  assert.match(css, /\.skillDirectoryItem:focus-visible/)
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*\.skillStateGrid/)
  assert.match(css, /\.skillLifecycleActions button \{ width: 100%; \}/)
})
