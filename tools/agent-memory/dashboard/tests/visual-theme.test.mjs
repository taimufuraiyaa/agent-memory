import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const main = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const hostedBootstrap = await readFile(new URL('../src/ui/HostedWorkspaceBootstrap.tsx', import.meta.url), 'utf8')
const workspaceApp = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')
const workspaceCss = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')

test('the shared root restores and persists an exact visual theme with Atlas as the safe default', () => {
  assert.match(main, /agent-memory:visual-theme/)
  assert.match(main, /value === 'atlas' \|\| value === 'classic'/)
  assert.match(main, /return 'atlas'/)
  assert.match(main, /useState<VisualTheme>/)
  assert.match(main, /localStorage\.getItem\(VISUAL_THEME_KEY\)/)
  assert.match(main, /localStorage\.setItem\(VISUAL_THEME_KEY, visualTheme\)/)
})

test('both runtimes receive one controlled visual theme independently from color scheme', () => {
  assert.match(main, /<HostedWorkspaceBootstrap[^>]*visualTheme=\{visualTheme\}[^>]*onVisualThemeChange=\{setVisualTheme\}/s)
  assert.match(main, /<WorkspaceApp[^>]*visualTheme=\{visualTheme\}[^>]*onVisualThemeChange=\{setVisualTheme\}/s)
  assert.match(hostedBootstrap, /<WorkspaceApp[^>]*visualTheme=\{visualTheme\}[^>]*onVisualThemeChange=\{onVisualThemeChange\}/s)
  assert.match(workspaceApp, /data-visual-theme=\{visualTheme\}/)
})

test('the top-left header exposes the Atlas and Classic theme choices after the brand', () => {
  assert.match(workspaceApp, /className="workspaceBrand"[\s\S]*className="workspaceThemePicker"/)
  assert.match(workspaceApp, /className="workspaceThemePicker"/)
  assert.match(workspaceApp, /aria-label="Visual theme"/)
  assert.match(workspaceApp, /value:\s*'atlas',\s*label:\s*'Living Memory Atlas'/)
  assert.match(workspaceApp, /value:\s*'classic',\s*label:\s*'Classic Workspace'/)
})

test('Atlas supplies explicit dark and light semantic tokens and journey styling', () => {
  assert.match(workspaceCss, /\.workspaceApp\[data-visual-theme="atlas"\]\s*\{/)
  assert.match(workspaceCss, /body\.light \.workspaceApp\[data-visual-theme="atlas"\]\s*\{/)
  assert.match(workspaceCss, /--atlas-temporal:/)
  assert.match(workspaceCss, /--atlas-verified:/)
  assert.match(workspaceCss, /--atlas-review:/)
  assert.match(workspaceCss, /\.workspaceApp\[data-visual-theme="atlas"\] \.howTreeBranches::before/)
  assert.match(workspaceCss, /\.workspaceApp\[data-visual-theme="atlas"\] \.knowledgeResult/)
})

test('the visual theme selector is bounded across desktop and phone layouts', () => {
  assert.match(workspaceCss, /\.workspaceThemePicker\s*\{[^}]*flex:[^;}]*;[^}]*min-width:[^;}]*;[^}]*max-width:/s)
  assert.match(workspaceCss, /@media \(max-width: 520px\)[\s\S]*\.workspaceThemePicker\s*\{[^}]*min-width:\s*0;[^}]*max-width:/s)
})
