import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/App.tsx', import.meta.url), 'utf8')
const stylesSource = await readFile(new URL('../src/ui/styles.css', import.meta.url), 'utf8')

test('compact navigation exposes its state and menu relationship', () => {
  assert.match(appSource, /className="navMenuTrigger"/)
  assert.match(appSource, /aria-expanded=\{navOpen\}/)
  assert.match(appSource, /aria-controls="responsive-navigation-menu"/)
  assert.match(appSource, /id="responsive-navigation-menu"/)
})

test('desktop and compact navigation share one destination definition', () => {
  const desktopNavigation = appSource.match(/<nav className="topbarCenter"[\s\S]*?<\/nav>/)?.[0] ?? ''
  assert.match(appSource, /const navigationItems/)
  assert.match(appSource, /navigationItems\.map/)
  assert.match(desktopNavigation, /navigationItems\.map/)
  assert.doesNotMatch(desktopNavigation, /<button className=/)
})

test('responsive CSS swaps the command strip for a bounded compact menu', () => {
  assert.match(stylesSource, /@media \(max-width: 2160px\)/)
  assert.match(stylesSource, /\.topbarCenter\s*\{[^}]*display:\s*none/s)
  assert.match(stylesSource, /\.navMenuShell\s*\{[^}]*display:\s*block/s)
  assert.match(stylesSource, /\.navMenuPanel\s*\{[^}]*max-width:\s*calc\(100vw\s*-\s*24px\)/s)
})
