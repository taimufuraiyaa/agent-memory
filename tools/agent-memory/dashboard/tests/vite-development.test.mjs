import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const viteConfig = await readFile(new URL('../vite.config.ts', import.meta.url), 'utf8')

test('Vite proxies the backend-owned runtime manifest during hot reload', () => {
  assert.match(viteConfig, /['"]\/dashboard\/runtime\.json['"]\s*:\s*\{/)
})

test('Vite proxies hosted API routes during container hot reload', () => {
  assert.match(viteConfig, /['"]\/v1['"]\s*:\s*\{[^}]*changeOrigin:\s*false/s)
})

test('Vite allows the documented development hostname without disabling host checks', () => {
  assert.match(viteConfig, /allowedHosts:\s*\[[^\]]*['"]agentmemory\.build['"][^\]]*\]/s)
  assert.doesNotMatch(viteConfig, /allowedHosts:\s*true/)
})
