const connection = { token: '', tenant: '', workspace: '' }
let sources = []
let pollTimer = 0
let retrievedEvidence = []
let activeProposal = null

const form = document.querySelector('#connection-form')
const status = document.querySelector('#status')
const list = document.querySelector('#source-list')
const refresh = document.querySelector('#refresh')
const details = document.querySelector('#details')
const detailsContent = document.querySelector('#details-content')
const queryButton = document.querySelector('#query-button')
const queryStatus = document.querySelector('#query-status')
const queryResults = document.querySelector('#query-results')

form.addEventListener('submit', (event) => {
  event.preventDefault()
  connection.token = document.querySelector('#access-token').value
  connection.tenant = document.querySelector('#tenant-id').value.trim()
  connection.workspace = document.querySelector('#workspace-id').value.trim()
  refresh.disabled = false
  document.querySelector('#refresh-privacy').disabled = false
  document.querySelector('#refresh-billing').disabled = false
  document.querySelector('#upgrade-plan').disabled = false
  document.querySelector('#cancel-plan').disabled = false
  void Promise.all([loadSources(), loadPrivacy(), loadBilling()])
})
refresh.addEventListener('click', () => void loadSources())
document.querySelector('#refresh-privacy').addEventListener('click', () => void loadPrivacy())
document.querySelector('#refresh-billing').addEventListener('click', () => void loadBilling())
document.querySelector('#upgrade-plan').addEventListener('click', () => void changePlan('upgrade', 'individual'))
document.querySelector('#cancel-plan').addEventListener('click', () => { if (window.confirm('Cancel the paid plan? Existing sources remain intact; plan limits change only after the billing provider confirms cancellation.')) void changePlan('cancel', 'trial') })
document.querySelector('#close-details').addEventListener('click', () => { details.hidden = true })

async function loadSources() {
  window.clearTimeout(pollTimer)
  setStatus('Loading private source records…')
  try {
    const query = connection.workspace ? `?workspace_id=${encodeURIComponent(connection.workspace)}` : ''
    const envelope = await request(`/v1/sources${query}`)
    sources = Array.isArray(envelope.data) ? envelope.data : []
    renderSources()
	const readyCount = sources.filter((source) => source.progress.state === 'ready').length
	queryButton.disabled = readyCount === 0
	queryStatus.textContent = readyCount ? `${readyCount} ready ${readyCount === 1 ? 'source is' : 'sources are'} available for citation-first questions.` : 'Wait for at least one source to become ready.'
    setStatus(sources.length ? `${sources.length} private ${sources.length === 1 ? 'source' : 'sources'}.` : 'No sources have been uploaded yet.')
    if (sources.some((source) => ['uploading', 'validating', 'processing', 'indexing'].includes(source.progress.state))) {
      pollTimer = window.setTimeout(loadSources, 2500)
    }
  } catch (error) {
    sources = []
    list.replaceChildren()
    setStatus(error.message || 'Sources could not be loaded.')
  }
}

document.querySelector('#query-form').addEventListener('submit', async (event) => {
  event.preventDefault()
  const ready = sources.filter((source) => source.progress.state === 'ready')
  if (!ready.length) return
  queryButton.disabled = true
  queryStatus.textContent = 'Retrieving authorized evidence…'
  try {
    const envelope = await request('/v1/source-queries', { method: 'POST', body: JSON.stringify({
      source_ids: ready.map((source) => source.id), query: document.querySelector('#source-query').value,
      generate: document.querySelector('#generate-answer').checked, provider: 'local-minilm-scaffold', model: 'local-hash-v1'
    }) })
    retrievedEvidence = envelope.data.evidence || []
    renderEvidence(envelope.data)
    queryStatus.textContent = envelope.data.answerable ? `${retrievedEvidence.length} cited passage${retrievedEvidence.length === 1 ? '' : 's'} retrieved.` : 'No sufficient evidence. This question remains unanswered.'
    document.querySelector('#memory-review').hidden = retrievedEvidence.length === 0
  } catch (error) {
    queryStatus.textContent = error.message || 'Evidence could not be retrieved.'
  } finally { queryButton.disabled = false }
})

function renderEvidence(result) {
  const fragment = document.createDocumentFragment()
  if (result.synthesis) {
    const synthesis = document.createElement('article'); synthesis.className = 'synthesis'
    const heading = document.createElement('h3'); heading.textContent = 'Generated synthesis'
    const text = document.createElement('p'); text.textContent = result.synthesis
    synthesis.append(heading, text); fragment.append(synthesis)
  }
  for (const evidence of result.evidence || []) {
    const article = document.createElement('article'); article.className = 'evidence'
    const cite = document.createElement('span'); cite.className = 'citation'; cite.textContent = `[${evidence.source_id}:${evidence.citation_id}]`
    const text = document.createElement('p'); text.textContent = evidence.text
    article.append(cite, text); fragment.append(article)
  }
  queryResults.replaceChildren(fragment)
}

document.querySelector('#proposal-form').addEventListener('submit', async (event) => {
  event.preventDefault()
  const firstSource = sources.find((source) => source.id === retrievedEvidence[0]?.source_id)
  try {
    const envelope = await request('/v1/memory-proposals', { method: 'POST', body: JSON.stringify({
      workspace_id: firstSource?.workspace_id || connection.workspace, memory_type: document.querySelector('#proposal-type').value,
      content: document.querySelector('#proposal-content').value, transformation: 'interpretation',
      evidence: retrievedEvidence.map(({ source_id, source_version, passage_id, citation_id }) => ({ source_id, source_version, passage_id, citation_id }))
    }) })
    activeProposal = envelope.data
    document.querySelector('#proposal-edit').value = activeProposal.content
    document.querySelector('#proposal-review').hidden = false
    document.querySelector('#proposal-status').textContent = 'Suggested only — no durable memory has been written.'
  } catch (error) { document.querySelector('#proposal-status').textContent = error.message || 'Proposal could not be created.'; document.querySelector('#proposal-review').hidden = false }
})

document.querySelector('#save-proposal').addEventListener('click', () => void changeProposal('edit'))
document.querySelector('#accept-proposal').addEventListener('click', () => void changeProposal('accept'))
document.querySelector('#reject-proposal').addEventListener('click', () => void changeProposal('reject'))

async function changeProposal(action) {
  if (!activeProposal) return
  const path = action === 'edit' ? `/v1/memory-proposals/${activeProposal.id}` : `/v1/memory-proposals/${activeProposal.id}/${action}`
  const options = action === 'edit' ? { method: 'PATCH', body: JSON.stringify({ content: document.querySelector('#proposal-edit').value, transformation: 'user_edit' }) } : { method: 'POST' }
  try {
    const envelope = await request(path, options); activeProposal = envelope.data
    document.querySelector('#proposal-status').textContent = action === 'accept' ? 'Accepted. A derived memory and lineage record were created.' : action === 'reject' ? 'Rejected. No memory was created.' : 'Edit saved. Explicit acceptance is still required.'
    if (action !== 'edit') document.querySelectorAll('#proposal-review button').forEach((button) => { button.disabled = true })
  } catch (error) { document.querySelector('#proposal-status').textContent = error.message || 'The proposal could not be changed.' }
}

function renderSources() {
  const template = document.querySelector('#source-template')
  const fragment = document.createDocumentFragment()
  for (const source of sources) {
    const card = template.content.firstElementChild.cloneNode(true)
    card.querySelector('h3').textContent = source.filename || 'Untitled source'
    card.querySelector('.format').textContent = source.media_type || 'Source'
    const badge = card.querySelector('.state-badge')
    badge.textContent = source.progress.label
    badge.dataset.state = source.progress.state
    const progress = card.querySelector('.progress-track span')
    progress.style.width = `${Math.max(0, Math.min(100, source.progress.percent))}%`
    progress.parentElement.setAttribute('role', 'progressbar')
    progress.parentElement.setAttribute('aria-label', `${source.filename} ${source.progress.label}`)
    progress.parentElement.setAttribute('aria-valuenow', String(source.progress.percent))
    progress.parentElement.setAttribute('aria-valuemin', '0')
    progress.parentElement.setAttribute('aria-valuemax', '100')
    card.querySelector('.progress-copy').textContent = `${source.progress.label} · ${source.progress.percent}%`
    const failure = card.querySelector('.failure')
    if (source.failure?.message) {
      failure.hidden = false
      const message = document.createElement('strong')
      message.textContent = source.failure.message
      failure.append(message, document.createTextNode(source.failure.action || ''))
    }
    card.querySelector('.details-button').addEventListener('click', () => showDetails(source))
    const retry = card.querySelector('.retry-button')
    if (source.failure?.retry_allowed) {
      retry.hidden = false
      retry.addEventListener('click', () => void retrySource(source, retry))
    }
    const remove = card.querySelector('.delete-button')
    if (['deleting', 'deleted'].includes(source.progress.state)) remove.hidden = true
    else remove.addEventListener('click', () => void deleteSource(source, remove))
    fragment.append(card)
  }
  list.replaceChildren(fragment)
}

async function deleteSource(source, button) {
  if (!window.confirm(`Delete ${source.filename || 'this source'}? Access will be revoked immediately and physical purge will continue in the background.`)) return
  button.disabled = true
  try {
    await request(`/v1/sources/${encodeURIComponent(source.id)}`, { method: 'DELETE', headers: { 'Idempotency-Key': crypto.randomUUID() } })
    setStatus('Access revoked. Verified physical deletion is now in progress.')
    await Promise.all([loadSources(), loadPrivacy()])
  } catch (error) { setStatus(error.message || 'Deletion could not be started.'); button.disabled = false }
}

async function loadPrivacy() {
  const statusNode = document.querySelector('#privacy-status')
  const content = document.querySelector('#privacy-content')
  statusNode.textContent = 'Loading retention and deletion status…'
  try {
    const envelope = await request('/v1/privacy')
    const value = envelope.data
    const items = [
      ['Consent expires', formatDate(value.consent_expires_at) || 'No active upload consent'],
      ['Immediate revocation', value.revocation_explanation],
      ['Physical purge', value.physical_purge_explanation],
      ['Retained classes', `${value.retained_classes.length} classes with versioned policies`],
      ['Source access events', `${value.source_access.length} recent content-free events`],
      ['Exports', `${value.exports.length} export requests`],
      ['Deletion operations', `${value.deletions.length} operations; ${value.deletions.filter((item) => item.state !== 'completed').length} still in progress`]
    ]
    const fragment = document.createDocumentFragment()
    for (const [term, description] of items) { const cell = document.createElement('div'); const heading = document.createElement('dt'); heading.textContent = term; const detail = document.createElement('dd'); detail.textContent = description; cell.append(heading, detail); fragment.append(cell) }
    content.replaceChildren(fragment)
    statusNode.textContent = 'Privacy controls are current.'
  } catch (error) { content.replaceChildren(); statusNode.textContent = error.message || 'Privacy controls could not be loaded.' }
}

async function loadBilling() {
  const statusNode = document.querySelector('#billing-status'); const content = document.querySelector('#billing-content')
  statusNode.textContent = 'Loading reconciled usage…'
  try {
    const envelope = await request('/v1/billing'); const value = envelope.data
    const items = [['Plan', `${value.plan_name} · ${label(value.subscription_state)}`], ['Metering', value.metering_disclosure], ['Invoices', `${value.invoices.length} available`]]
    for (const metric of value.metrics) items.push([label(metric.metric), `${metric.used} used · ${metric.forecast} forecast${metric.limit ? ` · ${metric.limit} limit` : ''}`])
    const fragment = document.createDocumentFragment(); for (const [term, description] of items) { const cell = document.createElement('div'); const heading = document.createElement('dt'); heading.textContent = term; const detail = document.createElement('dd'); detail.textContent = description; cell.append(heading, detail); fragment.append(cell) }; content.replaceChildren(fragment); statusNode.textContent = 'Plan and usage are current.'
  } catch (error) { content.replaceChildren(); statusNode.textContent = error.message || 'Billing status could not be loaded.' }
}

async function changePlan(action, planId) {
  const statusNode = document.querySelector('#billing-status'); statusNode.textContent = 'Submitting plan change…'
  try { await request('/v1/billing/plan-changes', { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ action, plan_id: planId }) }); statusNode.textContent = 'Plan change queued. Limits update only after verified provider confirmation.' }
  catch (error) { statusNode.textContent = error.message || 'Plan change could not be submitted.' }
}

async function retrySource(source, button) {
  button.disabled = true
  setStatus(`Retrying ${source.filename}…`)
  try {
    await request(`/v1/sources/${encodeURIComponent(source.id)}/retry`, { method: 'POST' })
    await loadSources()
  } catch (error) {
    setStatus(error.message || 'This source could not be retried.')
    button.disabled = false
  }
}

function showDetails(source) {
  document.querySelector('#details-title').textContent = source.filename || 'Source details'
  const rows = [
    ['State', source.progress.label], ['Rights basis', label(source.rights_basis)],
    ['Attestation policy', source.attestation?.policy_version], ['Attestation expires', formatDate(source.attestation?.expires_at)],
    ['Retention', label(source.retention_state)], ['Source version', source.provenance?.source_version || 'Pending'],
    ['Parser', source.provenance?.parser_version || 'Pending'], ['Normalization', source.provenance?.normalization_version || 'Pending'],
    ['Encryption', source.provenance?.vault_encryption_version || 'Pending'], ['Published', formatDate(source.provenance?.published_at)],
  ]
  const grid = document.createElement('dl')
  grid.className = 'detail-grid'
  for (const [term, value] of rows) {
    const cell = document.createElement('div')
    const dt = document.createElement('dt'); dt.textContent = term
    const dd = document.createElement('dd'); dd.textContent = value || 'Not available'
    cell.append(dt, dd); grid.append(cell)
  }
  detailsContent.replaceChildren(grid)
  details.hidden = false
  document.querySelector('#close-details').focus()
}

async function request(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${connection.token}`, 'X-Agent-Memory-Tenant': connection.tenant, ...(options.headers || {}) } })
  const value = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(value.error?.message || 'The request was not accepted.')
  return value
}

function setStatus(message) { status.textContent = message }
function label(value = '') { return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase()) }
function formatDate(value) { return value ? new Date(value).toLocaleString() : '' }
