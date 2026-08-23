import assert from 'node:assert/strict'
import { access, readFile } from 'node:fs/promises'
import test from 'node:test'

const root = new URL('../src/ui/', import.meta.url)

test('one canonical frontend has retired legacy application shells', async () => {
  for (const file of ['App.tsx', 'HostedApp.tsx', 'NotebookWorkspace.tsx', 'LibraryWorkspace.tsx', 'WikiPanel.tsx', 'hosted.css', 'notebook.css']) {
    await assert.rejects(access(new URL(file, root)))
  }
  const main = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
  assert.match(main, /WorkspaceApp|HostedWorkspaceBootstrap/)
  assert.doesNotMatch(main, /VITE_UNIFIED_WORKSPACE_ENABLED|HostedApp|from '.\/ui\/App'/)
})
