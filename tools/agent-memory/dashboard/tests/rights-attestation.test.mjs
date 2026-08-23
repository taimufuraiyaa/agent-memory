import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const gateSource = await readFile(new URL('../src/ui/RightsAttestationGate.tsx', import.meta.url), 'utf8').catch(() => '')
const mainSource = await readFile(new URL('../src/main.tsx', import.meta.url), 'utf8')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const importSource = await readFile(new URL('../src/lib/adapters/standaloneKnowledgeGateway.ts', import.meta.url), 'utf8')
const stylesSource = await readFile(new URL('../src/ui/rights-attestation.css', import.meta.url), 'utf8').catch(() => '')

test('application startup is guarded by server-calculated rights attestation status', () => {
  assert.match(mainSource, /<RightsAttestationGate>/)
  assert.match(gateSource, /getRightsAttestationStatus/)
  assert.match(gateSource, /acceptRightsAttestation/)
  assert.match(gateSource, /status === 'active'/)
  assert.match(apiSource, /export function getRightsAttestationStatus/)
  assert.match(apiSource, /export function acceptRightsAttestation/)
})

test('confirmation uses separate unchecked required statements and refreshes server state', () => {
  assert.match(gateSource, /role="dialog"/)
  assert.match(gateSource, /aria-modal="true"/)
  assert.match(gateSource, /policy\.primary_confirmation/)
  assert.match(gateSource, /policy\.statements\.map/)
  assert.match(gateSource, /checked=\{acceptedStatementIDs\.has\(statement\.id\)\}/)
  assert.match(gateSource, /useState<Set<string>>\(new Set\(\)\)/)
  assert.match(gateSource, /disabled=\{submitting \|\| !allAccepted\}/)
  assert.match(gateSource, /await getStatus\(\)/)
  assert.match(gateSource, /await accept\(/)
  assert.doesNotMatch(gateSource, /defaultChecked/)
})

test('confirmation offers a controlled select-all option without changing the acceptance contract', () => {
  assert.match(gateSource, />Select all confirmations</)
  assert.match(gateSource, /checked=\{allAccepted\}/)
  assert.match(gateSource, /policy\.statements\.map\(\(statement\) => statement\.id\)/)
  assert.match(gateSource, /event\.target\.checked \? new Set/)
  assert.match(gateSource, /: new Set\(\)/)
  assert.match(gateSource, /accepted_statement_ids: \[\.\.\.acceptedStatementIDs\]/)
})

test('book import records one supported lightweight rights basis', () => {
  for (const basis of ['author_owned', 'licensed', 'public_domain', 'lawfully_acquired_private_use']) {
    assert.match(importSource, new RegExp(basis))
  }
  assert.match(importSource, /rights_basis: rightsBasis/)
  assert.match(apiSource, /rights_basis: RightsBasis/)
  assert.match(apiSource, /form\.append\('rights_basis', input\.rights_basis\)/)
})

test('attestation dialog has responsive keyboard-visible styling', () => {
  assert.match(stylesSource, /\.rightsAttestationBackdrop/)
  assert.match(stylesSource, /\.rightsAttestationDialog/)
  assert.match(stylesSource, /:focus-visible/)
  assert.match(stylesSource, /@media \(max-width: 640px\)/)
  assert.match(stylesSource, /prefers-reduced-motion/)
  assert.match(stylesSource, /\.rightsAttestationSelectAll/)
  assert.match(stylesSource, /font-size: clamp\(/)
  assert.match(stylesSource, /\.rightsAttestationPrimary[\s\S]*?font-size: clamp\(/)
  assert.match(stylesSource, /\.rightsAttestationStatements label[\s\S]*?font-size: clamp\(/)
})
