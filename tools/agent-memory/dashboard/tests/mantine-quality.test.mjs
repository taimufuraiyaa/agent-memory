import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const css = await readFile(new URL('../src/ui/workspace/workspace.css', import.meta.url), 'utf8')
const app = await readFile(new URL('../src/ui/WorkspaceApp.tsx', import.meta.url), 'utf8')
const main = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const theme = await readFile(new URL('../src/ui/theme.ts', import.meta.url), 'utf8')
const vite = await readFile(new URL('../vite.config.ts', import.meta.url), 'utf8')

test('responsive shell retains scope and navigation at 320 px', () => {
  assert.match(app, /header=\{\{ height: 68 \}\}/)
  assert.match(app, /<Burger[^>]*aria-label="Open workspace explorer"/s)
  assert.match(app, /navbar=\{\{ width: 288, breakpoint: 'sm'/)
  assert.match(app, /aria-label="Workspace explorer"/)
  assert.match(app, /<Drawer[^>]*hiddenFrom="sm"/s)
  assert.match(css, /@media \(max-width: 600px\)/)
  assert.match(css, /\.workspaceHeaderContext \{[^}]*min-width: 0/)
  assert.match(css, /\.workspaceChatPanelOption \{ flex: 1;/)
  assert.match(css, /\.activityFilterScroller \{[^}]*overflow-x: auto/s)
})

test('motion and loading feedback respect accessibility preferences', () => {
  assert.match(css, /\.workspaceApp :focus-visible \{[^}]*outline: 3px solid/s)
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)/)
  assert.match(css, /transition-duration: \.01ms !important/)
  assert.match(css, /animation-duration: \.01ms !important/)
  assert.match(theme, /respectReducedMotion:\s*true/)
})

test('production frontend does not rely on remote visual assets', () => {
  assert.doesNotMatch(main + app + css, /https?:\/\//)
  assert.match(main, /@mantine\/core\/styles\.css/)
  assert.doesNotMatch(vite, /minify:\s*false/)
})
