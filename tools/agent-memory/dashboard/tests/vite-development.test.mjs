import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const viteConfig = await readFile(new URL('../vite.config.ts', import.meta.url), 'utf8')

test('Vite proxies the backend-owned runtime manifest during hot reload', () => {
  assert.match(viteConfig, /['"]\/dashboard\/runtime\.json['"]\s*:\s*\{/)
})
