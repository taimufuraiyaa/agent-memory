import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/ui/workspace/SettingsView.tsx', import.meta.url), 'utf8')
const hosted = await readFile(new URL('../src/lib/adapters/hostedKnowledgeGateway.ts', import.meta.url), 'utf8')
const bootstrap = await readFile(new URL('../src/ui/HostedWorkspaceBootstrap.tsx', import.meta.url), 'utf8')
const skills = await readFile(new URL('../src/ui/SkillsPanel.tsx', import.meta.url), 'utf8')
const workspaceCss = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')

test('primary settings keep account, data, and access ahead of System', () => {
  assert.match(source, /\['account', 'data', 'access', 'system'\]/)
  assert.match(source, /gateway\.getSettings\(\{ workspaceId \}/)
})

test('advanced tools live in one capability-aware System registry', () => {
  for (const tool of ['Diagnostics', 'Lifecycle', 'Benchmark', 'Clients', 'Skills', 'Infrastructure', 'Migration']) assert.match(source, new RegExp(tool))
  assert.match(source, /gateway\.supports\(tool\.capability, \{ workspaceId \}\)/)
  assert.match(source, /Unavailable in/)
})

test('private-installation hosted mode requires the dedicated system-tools runtime feature', () => {
  assert.match(bootstrap, /runtime\.features\.includes\('local_system_tools'\)/)
  assert.match(bootstrap, /createHostedKnowledgeGateway\(connection, \{ localOwner: localOnboarding, localSystemTools \}\)/)
  assert.match(hosted, /localSystemTools \? \[/)
  assert.doesNotMatch(hosted, /localOwner \? \[/)
  assert.match(hosted, /capability === 'clients'/)
  assert.match(hosted, /isRegisteredProject\(scope\.workspaceId\)/)
  for (const capability of ['lifecycle', 'clients', 'skills']) {
    assert.match(source, new RegExp(`capability: '${capability}'`))
    assert.match(hosted, new RegExp(`'${capability}'`))
  }
  assert.match(source, /gateway\.listLifecycle/)
  assert.match(source, /gateway\.listSkills/)
  assert.match(source, /<ClientsPanel clientProfiles=\{gateway\}/)
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
