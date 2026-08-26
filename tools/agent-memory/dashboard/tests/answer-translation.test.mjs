import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const read = (path) => readFile(new URL(`../src/${path}`, import.meta.url), 'utf8')

test('standalone gateway exposes local translation without advertising it in hosted', async () => {
  const [gateway, standalone, hosted, api] = await Promise.all([
    read('lib/knowledgeGateway.ts'),
    read('lib/adapters/standaloneKnowledgeGateway.ts'),
    read('lib/adapters/hostedKnowledgeGateway.ts'),
    read('lib/api.ts'),
  ])
  assert.match(gateway, /\| 'translation'/)
  assert.match(gateway, /translateAnswer\(/)
  assert.match(gateway, /getTranslationStatus\(/)
  assert.match(standalone, /'translation'/)
  assert.match(standalone, /translateLibraryAnswer/)
  assert.doesNotMatch(hosted, /capabilities[^\n]+translation/)
  assert.match(api, /\/api\/v1\/library\/local-llm\/translate/)
})

test('Ask answer has a compact local translation menu, settings, and original toggle', async () => {
  const [view, styles] = await Promise.all([read('ui/workspace/AskView.tsx'), read('ui/workspace/workspace.css')])
  for (const expected of ['Translate', "Don’t suggest translation for this answer", 'Translation settings', 'Show original', 'Show translation', 'Local model']) {
    assert.match(view, new RegExp(expected))
  }
  assert.match(view, /gateway\.supports\('translation'/)
  assert.match(view, /translationControllerRef\.current\?\.abort\(\)/)
  assert.match(view, /setTranslatedAnswer\(null\)/)
  assert.match(view, /aria-label="Target translation language"/)
  assert.match(styles, /\.askTranslationControl/)
  assert.match(styles, /@media \(max-width: 480px\)/)
})
