import { useEffect, useMemo, useState } from 'react'
import { Alert, Badge, Button, Card, Divider, Drawer, Group, NumberInput, Paper, Progress, SegmentedControl, Stack, Text, Textarea, Title } from '@mantine/core'
import { IconMessageReport, IconRefresh } from '@tabler/icons-react'
import type { ActivityItem, KnowledgeGateway } from '../../lib/knowledgeGateway'

type ActivityFilter = 'all' | ActivityItem['kind']

const filters: Array<{ value: ActivityFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'study', label: 'Study' },
  { value: 'upload', label: 'Uploads' },
  { value: 'indexing', label: 'Indexing' },
  { value: 'session', label: 'Sessions' },
  { value: 'retrieval', label: 'Retrieval' },
  { value: 'feedback', label: 'Feedback' },
  { value: 'deletion', label: 'Deletion' },
]

export function ActivityView({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [items, setItems] = useState<ActivityItem[]>([])
  const [cursor, setCursor] = useState<string>()
  const [filter, setFilter] = useState<ActivityFilter>('all')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [feedbackId, setFeedbackId] = useState('')
  const [score, setScore] = useState(4)
  const [reason, setReason] = useState('')
  const [selectedItem, setSelectedItem] = useState<ActivityItem | null>(null)

  async function load(nextCursor?: string) {
    setLoading(true)
    setError('')
    try {
      const page = await gateway.listActivity({ workspaceId }, nextCursor)
      setItems((current) => nextCursor ? [...current, ...page.items] : page.items)
      setCursor(page.nextCursor)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Activity could not be loaded.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { setItems([]); setCursor(undefined); setSelectedItem(null); void load() }, [gateway, workspaceId])
  const visible = useMemo(() => filter === 'all' ? items : items.filter((item) => item.kind === filter), [filter, items])

  async function retry(item: ActivityItem) {
    setError('')
    try { await gateway.retryActivity({ workspaceId }, item.id); await load() }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'The activity could not be retried.') }
  }

  async function sendFeedback() {
    if (!feedbackId || !reason.trim()) return
    setError('')
    try {
      await gateway.submitFeedback({ workspaceId }, feedbackId.replace(/^retrieval:/, ''), score, reason.trim())
      setFeedbackId('')
      setReason('')
      await load()
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Feedback could not be saved.') }
  }

  return <Stack className="activityView" gap="md" aria-label="Workspace activity">
    <Group justify="space-between" align="flex-start"><div><Title order={2}>Activity</Title><Text c="dimmed">Background work, agent sessions, retrievals, and feedback for this workspace.</Text></div><Button variant="default" leftSection={<IconRefresh size={16} />} onClick={() => void load()} loading={loading}>Refresh</Button></Group>
    <div className="activityFilterScroller"><SegmentedControl fullWidth value={filter} onChange={(value) => setFilter(value as ActivityFilter)} aria-label="Activity filters" data={filters} /></div>
    {error ? <Alert color="red" title="Activity unavailable" role="alert">{error}</Alert> : null}
    {!loading && visible.length === 0 ? <Paper withBorder p="xl" radius="lg"><Stack align="center"><Title order={3}>No activity here yet</Title><Text c="dimmed">Study a project, add a source, or ask a question to start the timeline.</Text></Stack></Paper> : null}
    <Stack className="activityTimeline" gap="sm">{visible.map((item) => {
      const opensFeedback = item.kind === 'feedback' && Boolean(item.feedback)
      return <Card
        key={item.id}
        className={opensFeedback ? 'activityFeedbackCard' : undefined}
        withBorder
        radius="lg"
        padding="lg"
        role={opensFeedback ? 'button' : undefined}
        tabIndex={opensFeedback ? 0 : undefined}
        aria-label={opensFeedback ? `Open feedback details for ${item.title}` : undefined}
        onClick={opensFeedback ? () => setSelectedItem(item) : undefined}
        onKeyDown={opensFeedback ? (event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            setSelectedItem(item)
          }
        } : undefined}
      ><Group justify="space-between" align="flex-start"><Stack gap="xs" style={{ flex: 1 }}><Group gap="xs"><Badge variant="light">{item.kind}</Badge><Badge variant="dot" color={item.failure ? 'red' : item.state === 'completed' ? 'memory' : 'blue'}>{item.state}</Badge></Group><Title order={3}>{item.title}</Title><Text c="dimmed" size="sm">Updated {new Date(item.updatedAt).toLocaleString()}</Text>{typeof item.progress === 'number' ? <Progress value={item.progress} aria-label={`${item.progress}% complete`} /> : null}{item.failure ? <Alert color="red" title={item.failure.message} /> : null}</Stack><Group>{opensFeedback ? <Text size="xs" fw={700} c="memory">View details</Text> : null}{item.failure?.retryAllowed ? <Button size="xs" variant="light" onClick={() => void retry(item)}>Retry</Button> : null}{item.kind === 'retrieval' ? <Button size="xs" variant="default" leftSection={<IconMessageReport size={15} />} onClick={() => setFeedbackId(item.id)}>Rate retrieval</Button> : null}</Group></Group></Card>
    })}</Stack>
    {cursor ? <Button variant="light" onClick={() => void load(cursor)} loading={loading}>Load more activity</Button> : null}
    {feedbackId ? <Paper component="form" withBorder p="lg" radius="lg" onSubmit={(event) => { event.preventDefault(); void sendFeedback() }}><Stack><Title order={3}>Retrieval feedback</Title><NumberInput label="Score (0–5)" min={0} max={5} value={score} onChange={(value) => setScore(Number(value))} /><Textarea label="What was useful or missing?" value={reason} onChange={(event) => setReason(event.currentTarget.value)} autosize minRows={3} /><Group justify="flex-end"><Button variant="default" onClick={() => setFeedbackId('')}>Cancel</Button><Button type="submit" disabled={!reason.trim()}>Save feedback</Button></Group></Stack></Paper> : null}
    <Drawer opened={Boolean(selectedItem)} onClose={() => setSelectedItem(null)} title="Feedback details" position="right" size="md" closeButtonProps={{ 'aria-label': 'Close feedback details' }}>
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
      </Stack> : null}
    </Drawer>
  </Stack>
}
