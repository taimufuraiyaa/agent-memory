import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/ui/workspace/SettingsView.tsx', import.meta.url), 'utf8')
const skills = await readFile(new URL('../src/ui/SkillsPanel.tsx', import.meta.url), 'utf8')
const workspaceCss = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')

test('primary settings keep account, data, and access ahead of System', () => {
  assert.match(source, /\['account', 'data', 'access', 'system'\]/)
  assert.match(source, /gateway\.getSettings\(\{ workspaceId \}/)
})

test('advanced tools live in one capability-aware System registry', () => {
  for (const tool of ['Diagnostics', 'Lifecycle', 'Benchmark', 'Clients', 'Skills', 'Migration']) assert.match(source, new RegExp(tool))
  assert.doesNotMatch(source, /Infrastructure/)
  assert.match(source, /gateway\.supports\(tool\.capability, \{ workspaceId \}\)/)
  assert.match(source, /Unavailable in/)
})

test('local system tools expose the expected capability contracts', () => {
  for (const capability of ['lifecycle', 'clients', 'skills']) {
    assert.match(source, new RegExp(`capability: '${capability}'`))
  }
  assert.match(source, /gateway\.listLifecycle/)
  assert.match(source, /gateway\.listSkills/)
  assert.match(source, /<ClientsPanel clientProfiles=\{gateway\}/)
})

test('SystemToolPanel declares shared callbacks before conditional tool rendering', () => {
  const panelStart = source.indexOf('function SystemToolPanel')
  const panel = source.slice(panelStart, source.indexOf('\nexport function WorkspaceSkillsView', panelStart))
  const firstConditionalReturn = panel.indexOf("if (id === 'diagnostics') return")
  const firstSkillCallback = panel.indexOf('const inspectSkill = useCallback')

  assert.ok(panelStart >= 0, 'SystemToolPanel should exist')
  assert.ok(firstConditionalReturn >= 0, 'SystemToolPanel should render tools conditionally')
  assert.ok(firstSkillCallback >= 0, 'SystemToolPanel should declare Skills callbacks')
  assert.ok(firstSkillCallback < firstConditionalReturn, 'all shared hooks must run before conditional returns')
})

test('System tools allocate extra-wide space to content and stack Skills before overflow', () => {
  assert.match(source, /span=\{\{ base: 12, md: 4, lg: 3, xl: 2 \}\}/)
  assert.match(source, /span=\{\{ base: 12, md: 8, lg: 9, xl: 10 \}\}/)
  assert.match(skills, /className="skillsBrowser"/)
  assert.match(skills, /className="skillsDirectory"/)
  assert.match(skills, /className="skillsDetail"/)
  assert.match(workspaceCss, /\.settingsView \.skillsBrowser\s*\{[^}]*grid-template-columns:\s*minmax\(240px,\s*300px\)\s+minmax\(0,\s*1fr\)/s)
  assert.match(workspaceCss, /@media \(max-width: 760px\)[\s\S]*\.settingsView \.skillsBrowser\s*\{[^}]*grid-template-columns:\s*1fr/s)
})
