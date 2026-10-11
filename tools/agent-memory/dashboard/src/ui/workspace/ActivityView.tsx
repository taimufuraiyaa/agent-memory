import { useEffect, useRef, useState } from 'react'
import { Alert, Badge, Button, Card, Divider, Drawer, Group, Modal, NumberInput, Paper, Progress, SegmentedControl, Stack, Text, Textarea, TextInput, Title } from '@mantine/core'
import { IconMessageReport, IconRefresh } from '@tabler/icons-react'
import type { ActivityFilter, ActivityItem, KnowledgeGateway, SolutionEpisodeDetail, SolutionEpisodeReviewInput } from '../../lib/knowledgeGateway'
import { CursorPagination } from './ListPagination'

const filters: Array<{ value: ActivityFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'study', label: 'Study' },
  { value: 'upload', label: 'Uploads' },
  { value: 'indexing', label: 'Indexing' },
  { value: 'session', label: 'Sessions' },
  { value: 'episode', label: 'Episodes' },
  { value: 'retrieval', label: 'Retrieval' },
  { value: 'feedback', label: 'Feedback' },
  { value: 'deletion', label: 'Deletion' },
]

export function ActivityView({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [items, setItems] = useState<ActivityItem[]>([])
  const [nextCursor, setNextCursor] = useState<string>()
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined])
  const [pageIndex, setPageIndex] = useState(0)
  const [filter, setFilter] = useState<ActivityFilter>('all')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [feedbackId, setFeedbackId] = useState('')
  const [score, setScore] = useState(4)
  const [reason, setReason] = useState('')
  const [feedbackError, setFeedbackError] = useState('')
  const [feedbackSaving, setFeedbackSaving] = useState(false)
  const [selectedItem, setSelectedItem] = useState<ActivityItem | null>(null)
  const [episodeDetail, setEpisodeDetail] = useState<SolutionEpisodeDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [correction, setCorrection] = useState('')
  const [reviewStepId, setReviewStepId] = useState('')
  const [reviewReason, setReviewReason] = useState('')
  const [successorEpisodeId, setSuccessorEpisodeId] = useState('')
  const loadSequence = useRef(0)

  async function load(pageCursor = cursorHistory[pageIndex], targetPage = pageIndex, activityFilter = filter) {
    const sequence = ++loadSequence.current
    setLoading(true)
    setError('')
    try {
      const page = await gateway.listActivity({ workspaceId }, pageCursor, activityFilter)
      if (sequence === loadSequence.current) {
        setItems(page.items)
        setNextCursor(page.nextCursor)
        setPageIndex(targetPage)
      }
    } catch (cause) {
      if (sequence === loadSequence.current) setError(cause instanceof Error ? cause.message : 'Activity could not be loaded.')
    } finally {
      if (sequence === loadSequence.current) setLoading(false)
    }
  }

  useEffect(() => { setItems([]); setNextCursor(undefined); setCursorHistory([undefined]); setPageIndex(0); setFeedbackId(''); setReason(''); setFeedbackError(''); setSelectedItem(null); setEpisodeDetail(null); void load(undefined, 0) }, [gateway, workspaceId])
  const visible = items

  function openFeedback(item: ActivityItem) {
    setFeedbackId(item.id)
    setScore(4)
    setReason('')
    setFeedbackError('')
  }

  function closeFeedback() {
    if (feedbackSaving) return
    setFeedbackId('')
    setReason('')
    setFeedbackError('')
  }

  async function retry(item: ActivityItem) {
    setError('')
    try { await gateway.retryActivity({ workspaceId }, item.id); await load() }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'The activity could not be retried.') }
  }

  async function sendFeedback() {
    if (!feedbackId || !reason.trim()) return
    setFeedbackSaving(true)
    setFeedbackError('')
    try {
      await gateway.submitFeedback({ workspaceId }, feedbackId.replace(/^retrieval:/, ''), score, reason.trim())
      setFeedbackId('')
      setReason('')
      await load()
    } catch (cause) { setFeedbackError(cause instanceof Error ? cause.message : 'Feedback could not be saved.') }
    finally { setFeedbackSaving(false) }
  }

  async function openItem(item: ActivityItem) {
    setSelectedItem(item)
    if (!item.episode) return
    setDetailLoading(true)
    setEpisodeDetail(null)
    setError('')
    try {
      const detail = await gateway.getSolutionEpisode({ workspaceId }, item.episode.id)
      setEpisodeDetail(detail)
      setCorrection(detail.summary || '')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Episode details could not be loaded.')
    } finally { setDetailLoading(false) }
  }

  async function reviewEpisode(input: Omit<SolutionEpisodeReviewInput, 'principalId' | 'episodeId'>) {
    if (!episodeDetail) return
    setDetailLoading(true)
    setError('')
    try {
      await gateway.reviewSolutionEpisode({ workspaceId }, { ...input, principalId: episodeDetail.principalId, episodeId: episodeDetail.id })
      if (input.action === 'delete') {
        setSelectedItem(null)
        setEpisodeDetail(null)
      } else {
        const detail = await gateway.getSolutionEpisode({ workspaceId }, episodeDetail.id)
        setEpisodeDetail(detail)
        setCorrection(detail.summary || '')
      }
      setReviewStepId('')
      setReviewReason('')
      await load()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Episode review could not be saved.')
    } finally { setDetailLoading(false) }
  }

  return <Stack className="activityView" gap="md" aria-label="Workspace activity">
    <Group justify="space-between" align="flex-start"><div><Title order={2}>Activity</Title><Text c="dimmed">Background work, agent sessions, retrievals, and feedback for this workspace.</Text></div><Button variant="default" leftSection={<IconRefresh size={16} />} onClick={() => void load()} loading={loading}>Refresh</Button></Group>
    <div className="activityFilterScroller"><SegmentedControl fullWidth value={filter} onChange={(value) => { const nextFilter = value as ActivityFilter; setFilter(nextFilter); setCursorHistory([undefined]); setPageIndex(0); void load(undefined, 0, nextFilter) }} aria-label="Activity filters" data={filters} /></div>
    {error ? <Alert color="red" title="Activity unavailable" role="alert">{error}</Alert> : null}
    {!loading && visible.length === 0 ? <Paper withBorder p="xl" radius="lg"><Stack align="center"><Title order={3}>No activity here yet</Title><Text c="dimmed">Study a project, add a source, or ask a question to start the timeline.</Text></Stack></Paper> : null}
    <Stack className="activityTimeline" gap="sm">{visible.map((item) => {
      const opensFeedback = item.kind === 'feedback' && Boolean(item.feedback)
      const opensEpisode = item.kind === 'episode' && Boolean(item.episode)
      const opensDetails = opensFeedback || opensEpisode
      return <Card
        key={item.id}
        className={opensDetails ? 'activityFeedbackCard' : undefined}
        withBorder
        radius="lg"
        padding="lg"
        role={opensDetails ? 'button' : undefined}
        tabIndex={opensDetails ? 0 : undefined}
        aria-label={opensFeedback ? `Open feedback details for ${item.title}` : opensEpisode ? `Open episode details for ${item.title}` : undefined}
        onClick={opensDetails ? () => void openItem(item) : undefined}
        onKeyDown={opensDetails ? (event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            void openItem(item)
          }
        } : undefined}
      ><Group justify="space-between" align="flex-start"><Stack gap="xs" style={{ flex: 1 }}><Group gap="xs"><Badge variant="light">{item.kind}</Badge><Badge variant="dot" color={item.failure ? 'red' : item.state === 'completed' ? 'memory' : 'blue'}>{item.state}</Badge>{item.episode?.pinned ? <Badge color="memory">Pinned</Badge> : null}{item.episode?.validation ? <Badge variant="outline">{item.episode.validation}</Badge> : null}</Group><Title order={3}>{item.title}</Title>{item.episode?.summary ? <Text lineClamp={2}>{item.episode.summary}</Text> : null}<Text c="dimmed" size="sm">Updated {new Date(item.updatedAt).toLocaleString()}</Text>{typeof item.progress === 'number' ? <Progress value={item.progress} aria-label={`${item.progress}% complete`} /> : null}{item.failure ? <Alert color="red" title={item.failure.message} /> : null}</Stack><Group>{opensDetails ? <Text size="xs" fw={700} c="memory">View details</Text> : null}{item.failure?.retryAllowed ? <Button size="xs" variant="light" onClick={() => void retry(item)}>Retry</Button> : null}{item.kind === 'retrieval' ? <Button size="xs" variant="default" leftSection={<IconMessageReport size={15} />} onClick={() => openFeedback(item)}>Rate retrieval</Button> : null}</Group></Group></Card>
    })}</Stack>
    <CursorPagination page={pageIndex + 1} hasNext={Boolean(nextCursor)} busy={loading} label="Activity" onPrevious={() => void load(cursorHistory[pageIndex - 1], pageIndex - 1)} onNext={() => { if (!nextCursor) return; const target = pageIndex + 1; setCursorHistory((current) => [...current.slice(0, target), nextCursor]); void load(nextCursor, target) }} />
    <Modal opened={Boolean(feedbackId)} onClose={closeFeedback} centered size="lg" title="Retrieval feedback" closeButtonProps={{ 'aria-label': 'Close retrieval feedback' }} closeOnClickOutside={!feedbackSaving} closeOnEscape={!feedbackSaving}>
      <Stack component="form" gap="md" onSubmit={(event) => { event.preventDefault(); void sendFeedback() }}>
        <Text c="dimmed" size="sm">Score this retrieval without losing your current place in Activity.</Text>
        {feedbackError ? <Alert color="red" title="Feedback could not be saved" role="alert">{feedbackError}</Alert> : null}
        <NumberInput label="Score (0–5)" min={0} max={5} value={score} onChange={(value) => setScore(Number(value))} />
        <Textarea label="What was useful or missing?" value={reason} onChange={(event) => setReason(event.currentTarget.value)} autosize minRows={4} />
        <Group justify="flex-end"><Button variant="default" onClick={closeFeedback} disabled={feedbackSaving}>Cancel</Button><Button type="submit" loading={feedbackSaving} disabled={!reason.trim()}>Save feedback</Button></Group>
      </Stack>
    </Modal>
    <Drawer opened={Boolean(selectedItem)} onClose={() => { setSelectedItem(null); setEpisodeDetail(null) }} title={selectedItem?.episode ? 'Episode details' : 'Feedback details'} position="right" size="lg" closeButtonProps={{ 'aria-label': selectedItem?.episode ? 'Close episode details' : 'Close feedback details' }}>
      {selectedItem?.feedback ? <Stack gap="lg">
        <Group gap="xs"><Badge variant="light">{selectedItem.feedback.requestType}</Badge><Badge color={selectedItem.feedback.score >= 4 ? 'memory' : 'orange'}>{selectedItem.feedback.score >= 0 ? `${selectedItem.feedback.score}/5` : 'Pending'}</Badge></Group>
        <div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Question / task</Text><Text mt={4} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{selectedItem.feedback.query || 'Unavailable'}</Text></div>
        <Divider />
        <Group grow align="flex-start"><div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Quality score</Text><Text fw={700}>{selectedItem.feedback.score >= 0 ? `${selectedItem.feedback.score} / 5` : 'Pending'}</Text></div><div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Request type</Text><Text fw={700}>{selectedItem.feedback.requestType || 'Unavailable'}</Text></div></Group>
        <Group grow align="flex-start"><div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Useful hits</Text><Text fw={700}>{typeof selectedItem.feedback.usefulCount === 'number' && selectedItem.feedback.usefulCount >= 0 ? selectedItem.feedback.usefulCount : 'Unavailable'}</Text></div><div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Total hits</Text><Text fw={700}>{typeof selectedItem.feedback.totalCount === 'number' && selectedItem.feedback.totalCount >= 0 ? selectedItem.feedback.totalCount : 'Unavailable'}</Text></div></Group>
        <div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Feedback reason</Text><Paper withBorder p="md" mt={4} radius="md"><Text style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{selectedItem.feedback.reason || 'No explanation was provided.'}</Text></Paper></div>
        <Divider />
        <div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Request ID</Text><Text ff="monospace" size="sm" style={{ overflowWrap: 'anywhere' }}>{selectedItem.feedback.requestId}</Text></div>
        <div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Logged time</Text><Text>{new Date(selectedItem.updatedAt).toLocaleString()}</Text></div>
      </Stack> : selectedItem?.episode ? detailLoading && !episodeDetail ? <Text c="dimmed">Loading episode details…</Text> : episodeDetail ? <Stack gap="lg">
        <Group gap="xs"><Badge variant="light">{episodeDetail.status}</Badge>{episodeDetail.outcome ? <Badge color={episodeDetail.outcome === 'success' ? 'memory' : 'orange'}>{episodeDetail.outcome}</Badge> : null}<Badge variant="outline">Retention: {episodeDetail.retention}</Badge>{episodeDetail.pinned ? <Badge color="memory">Pinned</Badge> : null}</Group>
        <div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Goal</Text><Title order={3}>{episodeDetail.goal}</Title></div>
        {episodeDetail.summary ? <div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Outcome summary</Text><Text style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{episodeDetail.summary}</Text></div> : null}
        <Group grow><div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Session</Text><Text ff="monospace" size="sm">{episodeDetail.sessionId}</Text></div><div><Text size="xs" fw={700} c="dimmed" tt="uppercase">Steps</Text><Text fw={700}>{episodeDetail.stepCount}</Text></div></Group>
        {episodeDetail.supersededBy ? <Alert color="orange" title="Superseded path">Replaced by episode {episodeDetail.supersededBy}</Alert> : null}
        <Divider label="Safe ordered path" />
        <Stack gap="sm">{episodeDetail.steps.map((step) => <Paper key={step.id} withBorder p="md" radius="md"><Stack gap="xs"><Group justify="space-between"><Group gap="xs"><Badge variant="light">{step.ordinal}</Badge><Badge variant="outline">{step.kind}</Badge>{step.misleading ? <Badge color="orange">Misleading</Badge> : null}{step.redacted ? <Badge color="red">Redacted</Badge> : null}</Group><Text size="xs" c="dimmed">{Math.round(step.confidence * 100)}%</Text></Group><Text>{step.summary}</Text>{step.rationale ? <Text size="sm" c="dimmed">{step.rationale}</Text> : null}{step.references.length ? <Text size="xs" c="dimmed">Evidence: {step.references.map((reference) => `${reference.kind}:${reference.targetId}`).join(', ')}</Text> : null}{step.reviewReason ? <Alert color="orange" title="Review note">{step.reviewReason}</Alert> : null}{!step.redacted ? <Group><Button size="xs" variant="light" onClick={() => setReviewStepId(step.id)}>Mark misleading</Button><Button size="xs" color="red" variant="subtle" onClick={() => { if (window.confirm('Redact this safe step? The stored text will be replaced.')) void reviewEpisode({ action: 'redact', stepId: step.id, reasonClass: 'user_request' }) }}>Redact step</Button></Group> : null}{reviewStepId === step.id ? <Stack><Textarea label="Why is this step misleading?" value={reviewReason} onChange={(event) => setReviewReason(event.currentTarget.value)} autosize minRows={2} /><Group justify="flex-end"><Button size="xs" variant="default" onClick={() => setReviewStepId('')}>Cancel</Button><Button size="xs" disabled={!reviewReason.trim()} onClick={() => void reviewEpisode({ action: 'misleading', stepId: step.id, reason: reviewReason.trim() })}>Save review</Button></Group></Stack> : null}</Stack></Paper>)}</Stack>
        {episodeDetail.evidence.length ? <><Divider label="Linked evidence" /><Stack gap="xs">{episodeDetail.evidence.map((reference, index) => <Text key={`${reference.kind}:${reference.targetId}:${index}`} size="sm">{reference.kind}: {reference.targetId} · {reference.resolution || 'unverified'}</Text>)}</Stack></> : null}
        {episodeDetail.promotions.length ? <><Divider label="Promotions" /><Stack gap="xs">{episodeDetail.promotions.map((promotion) => <Text key={promotion.id} size="sm">{promotion.kind}{promotion.memoryType ? ` · ${promotion.memoryType}` : ''} · {promotion.state}</Text>)}</Stack></> : null}
        <Divider label="Review controls" />
        {episodeDetail.summary ? <Stack><Textarea label="Correct outcome summary" value={correction} onChange={(event) => setCorrection(event.currentTarget.value)} autosize minRows={3} /><Button variant="light" disabled={!correction.trim() || correction.trim() === episodeDetail.summary.trim()} onClick={() => void reviewEpisode({ action: 'correct', summary: correction.trim(), idempotencyKey: crypto.randomUUID() })}>Publish correction</Button></Stack> : null}
        <Button variant="default" onClick={() => void reviewEpisode({ action: 'pin', pinned: !episodeDetail.pinned })}>{episodeDetail.pinned ? 'Unpin episode' : 'Pin episode'}</Button>
        <Group align="flex-end"><TextInput style={{ flex: 1 }} label="Successor episode ID" value={successorEpisodeId} onChange={(event) => setSuccessorEpisodeId(event.currentTarget.value)} /><Button variant="light" disabled={!successorEpisodeId.trim()} onClick={() => void reviewEpisode({ action: 'supersede', successorEpisodeId: successorEpisodeId.trim() })}>Supersede path</Button></Group>
        <Button color="red" variant="light" onClick={() => { if (window.confirm('Delete this solution episode and its stored path? This cannot be undone.')) void reviewEpisode({ action: 'delete', reason: 'user_request' }) }}>Delete episode</Button>
      </Stack> : null : null}
    </Drawer>
  </Stack>
}
