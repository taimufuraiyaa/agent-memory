import { useEffect, useMemo, useState } from 'react'
import { Alert, Badge, Button, Card, Group, NumberInput, Paper, Progress, SegmentedControl, Stack, Text, Textarea, Title } from '@mantine/core'
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

  useEffect(() => { setItems([]); setCursor(undefined); void load() }, [gateway, workspaceId])
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
    <Stack className="activityTimeline" gap="sm">{visible.map((item) => <Card key={item.id} withBorder radius="lg" padding="lg"><Group justify="space-between" align="flex-start"><Stack gap="xs" style={{ flex: 1 }}><Group gap="xs"><Badge variant="light">{item.kind}</Badge><Badge variant="dot" color={item.failure ? 'red' : item.state === 'completed' ? 'memory' : 'blue'}>{item.state}</Badge></Group><Title order={3}>{item.title}</Title><Text c="dimmed" size="sm">Updated {new Date(item.updatedAt).toLocaleString()}</Text>{typeof item.progress === 'number' ? <Progress value={item.progress} aria-label={`${item.progress}% complete`} /> : null}{item.failure ? <Alert color="red" title={item.failure.message} /> : null}</Stack><Group>{item.failure?.retryAllowed ? <Button size="xs" variant="light" onClick={() => void retry(item)}>Retry</Button> : null}{item.kind === 'retrieval' ? <Button size="xs" variant="default" leftSection={<IconMessageReport size={15} />} onClick={() => setFeedbackId(item.id)}>Rate retrieval</Button> : null}</Group></Group></Card>)}</Stack>
    {cursor ? <Button variant="light" onClick={() => void load(cursor)} loading={loading}>Load more activity</Button> : null}
    {feedbackId ? <Paper component="form" withBorder p="lg" radius="lg" onSubmit={(event) => { event.preventDefault(); void sendFeedback() }}><Stack><Title order={3}>Retrieval feedback</Title><NumberInput label="Score (0–5)" min={0} max={5} value={score} onChange={(value) => setScore(Number(value))} /><Textarea label="What was useful or missing?" value={reason} onChange={(event) => setReason(event.currentTarget.value)} autosize minRows={3} /><Group justify="flex-end"><Button variant="default" onClick={() => setFeedbackId('')}>Cancel</Button><Button type="submit" disabled={!reason.trim()}>Save feedback</Button></Group></Stack></Paper> : null}
  </Stack>
}
