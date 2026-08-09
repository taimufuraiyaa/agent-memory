import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const panelSource = await readFile(new URL('../src/ui/DeploymentPanel.tsx', import.meta.url), 'utf8')
const appSource = await readFile(new URL('../src/ui/App.tsx', import.meta.url), 'utf8')
const apiSource = await readFile(new URL('../src/lib/api.ts', import.meta.url), 'utf8')
const stylesSource = await readFile(new URL('../src/ui/deployment.css', import.meta.url), 'utf8')

test('infrastructure settings are internal and installation-scoped', () => {
  assert.match(appSource, /label: 'Infrastructure'/)
  assert.match(appSource, /surface: 'deployment'/)
  assert.match(appSource, /<DeploymentPanel \/>/)
  assert.match(panelSource, /internal operator/i)
  assert.match(panelSource, /self-managed/i)
  assert.doesNotMatch(panelSource, /workspace=/)
  assert.doesNotMatch(panelSource, /tenant selector/i)
})

test('infrastructure profile uses the revision-safe narrowed API contract', () => {
  assert.match(apiSource, /monthly_infrastructure_operations_budget_usd/)
  assert.match(apiSource, /expected_revision/)
  assert.match(panelSource, /profile\.revision/)
  assert.doesNotMatch(apiSource, /DeploymentCloudProvider/)
  assert.doesNotMatch(apiSource, /cloud_provider/)
  assert.doesNotMatch(apiSource, /paid_infrastructure_authorized/)
  assert.doesNotMatch(apiSource, /monthly_staging_budget_usd/)
})

test('form exposes configurable USD 1,000 operations budget and decision status', () => {
  assert.match(panelSource, /Monthly infrastructure operations budget/)
  assert.match(panelSource, /\$1,000/)
  assert.match(panelSource, /Decision status/)
  assert.match(panelSource, /monthly_infrastructure_operations_budget_usd: monthlyBudget/)
  assert.match(panelSource, /never deploys or spends/i)
})

test('external provider and paid cloud authorization controls are absent', () => {
  for (const forbidden of [
    /Cloud provider/,
    /Amazon Web Services/,
    /Google Cloud Platform/,
    /Microsoft Azure/,
    /Other provider/,
    /Authorize paid infrastructure/,
    /permission to spend/i,
  ]) {
    assert.doesNotMatch(panelSource, forbidden)
  }
})

test('infrastructure panel is responsive and exposes status feedback', () => {
  assert.match(stylesSource, /@media \(max-width: 680px\)/)
  assert.match(stylesSource, /:focus/)
  assert.match(panelSource, /aria-live="polite"/)
  assert.match(panelSource, /role="alert"/)
})
