import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const root = new URL('../src/', import.meta.url)
const read = (path) => readFile(new URL(path, root), 'utf8')

test('Activity is one local workspace-scoped timeline with filters and paging', async () => {
  const [source, contract, standalone] = await Promise.all([read('ui/workspace/ActivityView.tsx'), read('lib/knowledgeGateway.ts'), read('lib/adapters/standaloneKnowledgeGateway.ts')])
  for (const label of ['Study', 'Uploads', 'Indexing', 'Sessions', 'Episodes', 'Retrieval', 'Feedback', 'Deletion']) assert.match(source, new RegExp(label))
  assert.match(source, /gateway\.listActivity\(\{ workspaceId \}, pageCursor, activityFilter/)
  assert.match(source, /cursorHistory/)
  assert.match(source, /<CursorPagination/)
  assert.doesNotMatch(source, /Load more activity/)
  assert.match(contract, /ACTIVITY_PAGE_SIZE = 10/)
  assert.match(standalone, /numericCursor\(cursor\)/)
  assert.match(standalone, /filter === 'all'.*item\.kind === filter/s)
  assert.match(standalone, /slice\(offset, offset \+ ACTIVITY_PAGE_SIZE\)/)
})

test('Rate retrieval opens a viewport modal instead of appending an editor', async () => {
  const source = await read('ui/workspace/ActivityView.tsx')
  assert.match(source, /<Modal[\s\S]*opened=\{Boolean\(feedbackId\)\}/)
  assert.match(source, /title="Retrieval feedback"/)
  assert.match(source, /closeButtonProps=.*Close retrieval feedback/)
  assert.match(source, /feedbackError/)
  assert.doesNotMatch(source, /feedbackId \? <Paper component="form"/)
})

test('episode cards open a keyboard-accessible safe-path drawer with review controls', async () => {
  const [view, contract, standalone] = await Promise.all([read('ui/workspace/ActivityView.tsx'), read('lib/knowledgeGateway.ts'), read('lib/adapters/standaloneKnowledgeGateway.ts')])
  assert.match(contract, /kind: 'study'.*'episode'.*'retrieval'/)
  assert.match(contract, /getSolutionEpisode\(scope: WorkspaceScope, episodeId: string/)
  assert.match(contract, /reviewSolutionEpisode\(scope: WorkspaceScope, input: SolutionEpisodeReviewInput/)
  assert.match(standalone, /getStandaloneSolutionEpisode\(\{ workspace: scope\.workspaceId, episode_id: episodeId \}\)/)
  assert.match(standalone, /reviewStandaloneSolutionEpisode\(\{ workspace: scope\.workspaceId/)
  assert.match(view, /title=\{selectedItem\?\.episode \? 'Episode details' : 'Feedback details'\}/)
  assert.match(view, /Open episode details for \$\{item\.title\}/)
  for (const label of ['Safe ordered path', 'Linked evidence', 'Promotions', 'Retention', 'Mark misleading', 'Redact step', 'Publish correction', 'Pin episode', 'Supersede path', 'Delete episode']) assert.match(view, new RegExp(label, 'i'))
  assert.match(view, /event\.key === 'Enter' \|\| event\.key === ' '/)
})

test('failed work can retry and retrievals accept scored feedback', async () => {
  const source = await read('ui/workspace/ActivityView.tsx')
  assert.match(source, /gateway\.retryActivity/)
  assert.match(source, /gateway\.submitFeedback/)
  assert.match(source, /Score \(0–5\)/)
  assert.match(source, /replace\(\/\^retrieval:/)
})
