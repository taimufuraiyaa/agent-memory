import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

async function source(path) {
  return readFile(new URL(`../src/ui/${path}`, import.meta.url), 'utf8')
}

const pager = await source('workspace/ListPagination.tsx').catch(() => '')
const activity = await source('workspace/ActivityView.tsx')
const memories = await source('workspace/MemoryExplorer.tsx')

test('shared pagination supports bounded arrays and opaque cursor pages', () => {
  assert.match(pager, /export const LIST_PAGE_SIZE = 10/)
  assert.match(pager, /export function paginateRecords/)
  assert.match(pager, /export function ListPagination/)
  assert.match(pager, /export function CursorPagination/)
  assert.match(pager, /Showing.*of.*records/s)
  assert.match(pager, /aria-label=.*pagination/)
})

test('Activity and Memories replace pages and retain cursor history', () => {
  for (const page of [activity, memories]) {
    assert.match(page, /cursorHistory/)
    assert.match(page, /<CursorPagination/)
    assert.doesNotMatch(page, /\[\.\.\.current, \.\.\.page\.items\]/)
  }
  assert.doesNotMatch(activity, /Load more activity/)
  assert.doesNotMatch(memories, />Load more</)
})

test('daily-work record collections use explicit pagination', async () => {
  for (const path of ['workspace/SourcesView.tsx', 'workspace/NotesView.tsx', 'workspace/HowHistoryView.tsx']) {
    const page = await source(path)
    assert.match(page, /paginateRecords/)
    assert.match(page, /<ListPagination/)
  }
})

test('System record collections use explicit pagination', async () => {
  for (const path of ['LifecyclePanel.tsx', 'SkillsPanel.tsx', 'ClientsPanel.tsx', 'BenchmarkPanel.tsx']) {
    const page = await source(path)
    assert.match(page, /paginateRecords/)
    assert.match(page, /<ListPagination/)
  }
})
