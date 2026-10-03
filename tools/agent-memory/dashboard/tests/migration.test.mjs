import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/workspace/SettingsView.tsx', import.meta.url), 'utf8')
const migrationSource = await readFile(new URL('../src/ui/MigrationPanel.tsx', import.meta.url), 'utf8').catch(() => '')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')

test('standalone exposes a copy-first encrypted migration download', () => {
  assert.match(appSource, /MigrationPanel/)
  assert.match(migrationSource, /uploaded source originals are excluded/i)
  assert.match(migrationSource, /Local data is not deleted/i)
  assert.match(migrationSource, /type="password"/)
  assert.match(apiSource, /\/api\/v1\/migrations\/portable-export/)
  assert.match(apiSource, /response\.blob\(\)/)
})
