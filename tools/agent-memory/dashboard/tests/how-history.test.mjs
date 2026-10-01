import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const root = new URL('../src/', import.meta.url)
const read = (path) => readFile(new URL(path, root), 'utf8')

test('How History stays reachable from workspace actions alongside Activity and Memories', async () => {
  const [shell, explorer, route] = await Promise.all([
    read('ui/WorkspaceApp.tsx'),
    read('ui/workspace/WorkspaceExplorer.tsx'),
    read('ui/workspace/workspaceRoute.ts'),
  ])
  assert.match(explorer, /onWorkspaceAction\(id, 'history'\)\}>How history/)
  assert.match(shell, /route\.knowledgeView === 'history' \? <HowHistoryView gateway=\{gateway\} workspaceId=\{workspace\.id\}/)
  assert.match(shell, /route\.destination === 'activity' \? <ActivityView gateway=\{gateway\} workspaceId=\{workspace\.id\}/)
  assert.match(shell, /route\.knowledgeView === 'memories' \? <Stack gap="md"><Group[\s\S]*?<MemoryExplorer gateway=\{gateway\} workspaceId=\{workspace\.id\}/)
  assert.match(route, /'sources' \| 'memories' \| 'history' \| 'notes'/)
})

test('How History renders a lazy accessible provenance tree', async () => {
  const source = await read('ui/workspace/HowHistoryView.tsx')
  assert.match(source, /gateway\.listHowHistory\(\{ workspaceId \}/)
  assert.match(source, /gateway\.getSolutionEpisode\(\{ workspaceId \}, episode\.id\)/)
  assert.match(source, /role="tree"/)
  assert.match(source, /role="treeitem"/)
  assert.match(source, /aria-expanded=\{expanded\}/)
  for (const label of ['Steps', 'What', 'When', 'Where', 'Feedback', 'Ungrouped memories']) assert.match(source, new RegExp(label))
  assert.match(source, /NotApplicable/)
  assert.match(source, />N\/A</)
  assert.match(source, /Started/)
  assert.match(source, /Last updated/)
  assert.match(source, /Finalized/)
  assert.doesNotMatch(source, /similarity|semantic parent/i)
})

test('gateway contracts normalize promoted memories, evidence, and feedback in both runtimes', async () => {
  const [contract, adapter, standalone, hosted] = await Promise.all([
    read('lib/knowledgeGateway.ts'),
    read('lib/adapters/solutionEpisodeAdapter.ts'),
    read('lib/adapters/standaloneKnowledgeGateway.ts'),
    read('lib/adapters/hostedKnowledgeGateway.ts'),
  ])
  assert.match(contract, /listHowHistory\(scope: WorkspaceScope/)
  assert.match(contract, /promotionTargets:/)
  assert.match(contract, /pathFeedback:/)
  assert.match(contract, /finalizedAt\?: string/)
  assert.match(adapter, /promotion_targets/)
  assert.match(adapter, /path_feedback/)
  assert.match(adapter, /finalizedAt: record\.summary\?\.created_at/)
  assert.match(standalone, /listSolutionEpisodes/)
  assert.match(hosted, /listHostedProjectSolutions/)
})
