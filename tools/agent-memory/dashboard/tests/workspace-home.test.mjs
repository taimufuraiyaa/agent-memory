import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('../src/ui/workspace/HomeView.tsx', import.meta.url), 'utf8')

test('Home explains the continuous post-study workflow', () => {
  for (const action of ['Ask this project', 'Search extracted memories', 'Browse memories', 'Add source', 'View Activity']) assert.match(source, new RegExp(action))
  assert.match(source, /Codebases, books, documents, and notes/)
})
