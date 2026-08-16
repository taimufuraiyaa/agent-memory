import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/ui/HostedApp.tsx', import.meta.url), 'utf8')
const apiSource = await readFile(new URL('../src/lib/hostedApi.ts', import.meta.url), 'utf8')
const cssSource = await readFile(new URL('../src/ui/hosted.css', import.meta.url), 'utf8')

test('hosted product uses task navigation instead of one long control sheet', () => {
  assert.match(appSource, /type HostedArea = 'home' \| 'library' \| 'settings'/)
  assert.match(appSource, /aria-label="Product areas"/)
  assert.match(appSource, /id="hosted-main-content"/)
  assert.match(appSource, /className="skipLink"/)
  for (const area of ['Home', 'Library', 'Settings']) {
    assert.match(appSource, new RegExp(`label: '${area}'`))
  }
  assert.doesNotMatch(appSource, /label: 'Memory'/)
  assert.doesNotMatch(appSource, /label: 'Data'/)
  assert.match(appSource, /activeArea === 'library'/)
  assert.match(appSource, /activeArea === 'settings'/)
})

test('library is a selected-source conversation workspace', () => {
  assert.match(appSource, /selectedSourceId/)
  assert.match(appSource, /conversationsBySource/)
  assert.match(appSource, /className="librarySourceRail"/)
  assert.match(appSource, /className="libraryConversation"/)
  assert.match(appSource, /className="conversationComposer"/)
  assert.match(appSource, /queryHostedSources\(connection, \[selectedSource\.id\]/)
})

test('conversation recalls durable memory without confusing it with source citations', () => {
  assert.match(appSource, /Promise\.allSettled/)
  assert.match(appSource, /searchHostedMemories\(connection, query\)/)
  assert.match(appSource, /Memory context/)
  assert.match(appSource, /Previously reviewed durable knowledge/)
  assert.match(appSource, /Keep as memory/)
  assert.doesNotMatch(appSource, /activeArea === 'memory'/)
})

test('privacy and portability controls are consolidated into settings', () => {
  assert.doesNotMatch(appSource, /activeArea === 'data'/)
  assert.match(appSource, /activeArea === 'settings'/)
  for (const marker of ['Privacy &amp; retention', 'Data export', 'Import standalone migration']) {
    assert.match(appSource, new RegExp(marker))
  }
})

test('unconnected sessions get a dedicated private connection entry', () => {
  assert.match(appSource, /if \(!connected\)/)
  assert.match(appSource, /Connect your private workspace/)
  assert.match(appSource, /cleared when you reload/i)
  assert.doesNotMatch(appSource + apiSource, /localStorage|sessionStorage/)
})

test('library provides checksum-bound four-format source upload', () => {
  assert.match(appSource, /accept="\.pdf,\.epub,\.md,\.markdown,\.txt"/)
  assert.match(appSource, /Rights basis/)
  assert.match(apiSource, /crypto\.subtle\.digest\('SHA-256'/)
  assert.match(apiSource, /\/v1\/sources\/uploads/)
  assert.match(apiSource, /grant\.upload_path/)
  assert.match(apiSource, /method: 'PUT'/)
  assert.match(apiSource, /application\/epub\+zip/)
})

test('library polls only while a source is processing and stops at terminal state', () => {
  assert.match(appSource, /terminalSourceStates/)
  assert.match(appSource, /hasProcessingSources/)
  assert.match(appSource, /window\.setInterval/)
  assert.match(appSource, /window\.clearInterval/)
  for (const state of ['ready', 'failed', 'rejected', 'disabled', 'deleted']) {
    assert.match(appSource, new RegExp(`'${state}'`))
  }
})

test('citation identifiers and passages wrap in independent rows', () => {
  assert.match(cssSource, /\.evidenceList article\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/s)
  assert.match(cssSource, /\.evidenceList span\s*\{[^}]*overflow-wrap:\s*anywhere/s)
  assert.match(cssSource, /\.evidenceList p\s*\{[^}]*min-width:\s*0[^}]*overflow-wrap:\s*anywhere/s)
  assert.doesNotMatch(cssSource, /\.evidenceList article\s*\{[^}]*grid-template-columns:\s*36px/s)
})

test('library presents reconstructive source context separately from durable memory', () => {
  assert.match(appSource, /reconstructed source context/)
  assert.match(appSource, /Source evidence and reviewed memory stay visibly separate/)
  assert.match(appSource, /citation/)
  assert.match(appSource, /item\.locator\?\.display/)
  assert.doesNotMatch(appSource, /Add generated synthesis/)
  assert.match(apiSource, /reconstruction_strategy\?: string/)
  assert.match(apiSource, /included_passage_ids\?: string\[\]/)
  assert.match(apiSource, /included_citation_ids\?: string\[\]/)
	assert.match(apiSource, /window_clipped\?: boolean/)
	assert.match(apiSource, /planner_used\?: boolean/)
	assert.match(apiSource, /reranker_used\?: boolean/)
	assert.match(appSource, /Understood as/)
  assert.match(apiSource, /evidence_available: boolean/)
  assert.match(appSource, /not a source citation/)
})

test('customer operations use bounded field presentation and partial loading', () => {
  assert.match(appSource, /function ProductFields/)
  assert.match(appSource, /Promise\.allSettled/)
  assert.doesNotMatch(appSource, /JSON\.stringify\(privacy/)
  assert.doesNotMatch(appSource, /JSON\.stringify\(billing/)
})

test('visual system is scoped, responsive, accessible, and motion-safe', () => {
  for (const marker of [
    '.hostedProduct',
    '.productNavigation',
    '.productEmpty',
    ':focus-visible',
    '@media (prefers-reduced-motion: reduce)',
    'min-height: 100dvh',
  ]) {
    assert.match(cssSource, new RegExp(marker.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
  }
  assert.doesNotMatch(cssSource, /https?:\/\//)
  assert.doesNotMatch(cssSource, /linear-gradient/)
})

test('credential secrets have a dedicated ephemeral presentation', () => {
  assert.match(appSource, /credentialSecret/)
  assert.match(appSource, /Copy this secret now/)
  assert.match(appSource, /setCredentialSecret\(''\)/)
})

test('local onboarding creates and resumes an owner without exposing routing credentials', () => {
  assert.match(appSource, /runtime\.features\.includes\('local_onboarding'\)/)
  assert.match(appSource, /Create local owner/)
  assert.match(appSource, /private_installation_confirmed/)
  assert.match(appSource, /getLocalSession/)
  assert.match(appSource, /signupLocalOwner/)
  assert.match(appSource, /logoutLocalSession/)
  assert.match(apiSource, /if \(connection\.token\) headers\.set\('Authorization'/)
  assert.match(apiSource, /credentials: 'same-origin'/)
  assert.doesNotMatch(apiSource, /localStorage|sessionStorage/)
})

test('connected hosted sessions require server-owned rights attestation before source upload', () => {
  assert.match(apiSource, /getHostedRightsAttestationStatus/)
  assert.match(apiSource, /acceptHostedRightsAttestation/)
  assert.match(apiSource, /\/v1\/attestations\/rights/)
  assert.match(appSource, /<RightsAttestationGate/)
  assert.match(appSource, /getStatus=\{getHostedAttestationStatus\}/)
  assert.match(appSource, /accept=\{acceptHostedAttestation\}/)
})
