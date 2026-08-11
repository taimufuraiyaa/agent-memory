import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/App.tsx', import.meta.url), 'utf8')
const migrationSource = await readFile(new URL('../src/ui/MigrationPanel.tsx', import.meta.url), 'utf8').catch(() => '')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const hostedSource = await readFile(new URL('../src/ui/HostedApp.tsx', import.meta.url), 'utf8')
const hostedAPISource = await readFile(new URL('../src/lib/hostedApi.ts', import.meta.url), 'utf8')

test('standalone exposes a copy-first encrypted migration download', () => {
  assert.match(appSource, /MigrationPanel/)
  assert.match(migrationSource, /uploaded source originals are excluded/i)
  assert.match(migrationSource, /Local data is not deleted/i)
  assert.match(migrationSource, /type="password"/)
  assert.match(apiSource, /\/api\/v1\/migrations\/portable-export/)
  assert.match(apiSource, /response\.blob\(\)/)
})

test('hosted import keeps passphrase in memory and reuses one idempotency key for retry', () => {
  assert.match(hostedSource, /Import standalone migration/)
  assert.match(hostedSource, /type="file"/)
  assert.match(hostedSource, /type="password"/)
  assert.match(hostedSource, /useState\(crypto\.randomUUID\(\)\)/)
  assert.match(hostedAPISource, /X-Agent-Memory-Bundle-Passphrase/)
  assert.match(hostedAPISource, /Idempotency-Key/)
  assert.match(hostedAPISource, /\/v1\/imports/)
  assert.doesNotMatch(hostedSource + hostedAPISource, /localStorage|sessionStorage/)
})

test('hosted migration reports bounded counts instead of imported content', () => {
  assert.match(hostedSource, /Imported.*merged.*skipped.*failed/s)
  assert.doesNotMatch(hostedSource, /JSON\.stringify\(migrationResult/)
})
