import { Alert, Badge, Button, Card, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useCallback, useEffect, useState } from 'react'
import type { KnowledgeGateway, LocalProjectGraphQueue } from '../../lib/knowledgeGateway'

export function ProjectGraphQueue({ gateway }: { gateway: KnowledgeGateway }) {
  const [queue, setQueue] = useState<LocalProjectGraphQueue | null>(null)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')

  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (!gateway.getLocalProjectGraphQueue) {
      setError('Cross-project Graph queue status is unavailable in this runtime.')
      setBusy(false)
      return
    }
    setBusy(true)
    try {
      setQueue(await gateway.getLocalProjectGraphQueue(signal))
      setError('')
    } catch (cause) {
      if (!signal?.aborted) setError(cause instanceof Error ? cause.message : 'Graph queue status is unavailable.')
    } finally {
      if (!signal?.aborted) setBusy(false)
    }
  }, [gateway])

  useEffect(() => {
    const controller = new AbortController()
    void refresh(controller.signal)
    return () => controller.abort()
  }, [refresh])

  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setInterval(() => void refresh(controller.signal), queue?.legacy_fallback ? 30000 : queue?.jobs.length ? 3000 : 15000)
    return () => { window.clearInterval(timer); controller.abort() }
  }, [queue?.legacy_fallback, queue?.jobs.length, refresh])

  return <Card withBorder>
    <Stack gap="sm">
      <Group justify="space-between" align="flex-start">
        <div>
          <Title order={4}>Project queue</Title>
          <Text size="sm" c="dimmed">Queued jobs are waiting for the worker. Running jobs are being processed now.</Text>
        </div>
        <Group gap="xs">
          {queue ? <Badge color={error ? 'yellow' : queue.projects_unavailable ? 'yellow' : queue.jobs.length ? 'orange' : 'gray'}>{error ? 'Unavailable' : queue.projects_unavailable ? `${queue.jobs.length} active · scan incomplete` : `${queue.jobs.length} active · ${queue.projects_scanned} projects`}</Badge> : null}
          <Button size="xs" variant="default" loading={busy} onClick={() => void refresh()}>Refresh</Button>
        </Group>
      </Group>

      {error ? <Alert color="yellow" title="Queue status unavailable" role="alert">{queue ? `The last successful queue snapshot may be out of date. ${error}` : error}</Alert> : null}
      {queue && queue.projects_unavailable > 0 ? <Alert color="yellow" title="Queue scan incomplete" role="status">
        <Text size="sm">Could not read {queue.projects_unavailable} of {queue.projects_scanned} registered project statuses{queue.unavailable_project_names?.length ? `: ${queue.unavailable_project_names.join(', ')}` : '.'}</Text>
      </Alert> : null}

      {queue ? queue.jobs.length ? <Table.ScrollContainer minWidth={620}>
          <Table verticalSpacing="sm" highlightOnHover>
            <Table.Thead><Table.Tr><Table.Th>Project</Table.Th><Table.Th>State</Table.Th><Table.Th>Job</Table.Th><Table.Th>Elapsed</Table.Th><Table.Th>Pending records</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>{queue.jobs.map((job) => <Table.Tr key={`${job.workspace}:${job.job_id}`}>
              <Table.Td><Text fw={600}>{job.workspace}</Text></Table.Td>
              <Table.Td><Badge color={job.state === 'running' ? 'green' : 'orange'}>{job.state === 'running' ? 'Running' : 'Queued'}</Badge></Table.Td>
              <Table.Td><Text size="sm" ff="monospace">{job.job_id}</Text></Table.Td>
              <Table.Td><Text size="sm">{job.state === 'running' ? `Running for ${formatAge(job.age_seconds)}` : `Queued for ${formatAge(job.age_seconds)}`}</Text></Table.Td>
              <Table.Td>{job.pending_records}</Table.Td>
            </Table.Tr>)}</Table.Tbody>
          </Table>
      </Table.ScrollContainer> : queue.projects_unavailable > 0 ? <Text size="sm" c="dimmed">No active jobs were found in the projects whose status could be read.</Text> : <Text size="sm" c="dimmed">No queued or running jobs across {queue.projects_scanned} registered projects.</Text> : error ? <Text size="sm" c="dimmed">Queue status has not loaded. Check that the local API supports the Graph queue route, then refresh.</Text> : <Text size="sm" c="dimmed">Loading project queue…</Text>}
    </Stack>
  </Card>
}

function formatAge(seconds: number): string {
  const bounded = Math.max(0, Math.floor(seconds))
  if (bounded < 60) return `${bounded}s`
  if (bounded < 3600) return `${Math.floor(bounded / 60)}m ${bounded % 60}s`
  const hours = Math.floor(bounded / 3600)
  const minutes = Math.floor((bounded % 3600) / 60)
  return `${hours}h ${minutes}m`
}
