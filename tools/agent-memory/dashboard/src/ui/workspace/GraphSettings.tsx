import { Alert, Badge, Button, Card, Group, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { useEffect, useState } from 'react'
import type { GraphOperationAction, GraphReadiness, GraphStatus, KnowledgeGateway } from '../../lib/knowledgeGateway'
import { GraphExplorer } from './GraphExplorer'

export function GraphSettings({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [readiness, setReadiness] = useState<GraphReadiness | null>(null)
  const [status, setStatus] = useState<GraphStatus | null>(null)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  async function load(signal?: AbortSignal) {
    setBusy(true); setError('')
    try { const [nextReadiness, nextStatus] = await Promise.all([gateway.getGraphReadiness({ workspaceId }, signal), gateway.getGraphStatus({ workspaceId }, signal)]); setReadiness(nextReadiness); setStatus(nextStatus) } catch (cause) { if (!signal?.aborted) setError(cause instanceof Error ? cause.message : 'Graph status is unavailable.') } finally { if (!signal?.aborted) setBusy(false) }
  }
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [gateway, workspaceId])
  useEffect(() => { if (!status || !['queued', 'running'].includes(status.state)) return; const timer = window.setInterval(() => void load(), 3000); return () => window.clearInterval(timer) }, [status?.state])
  async function operate(action: GraphOperationAction) {
    if (!status || busy) return
    if ((action === 'disable' || action === 'rollback') && !window.confirm(`${action === 'disable' ? 'Disable' : 'Roll back'} this graph index? Basic retrieval remains available.`)) return
    setBusy(true); setError('')
    try { setStatus(await gateway.operateGraph({ workspaceId }, status.configuration_id, action, status.active_revision_id, action === 'cancel' ? status.current_job?.id : action === 'retry' ? status.last_job_id : undefined)) } catch (cause) { setError(cause instanceof Error ? cause.message : 'Graph operation was not accepted.') } finally { setBusy(false) }
  }
  return <Stack gap="xl" aria-busy={busy}>
    <Card withBorder><Stack><Group justify="space-between"><div><Title order={3}>Derived graph index</Title><Text c="dimmed">Microsoft GraphRAG builds an offline, versioned index. Online Ask reads only Agent Memory-owned normalized records.</Text></div>{status ? <Badge color={status.fresh ? 'green' : status.degraded ? 'yellow' : 'gray'}>{status.state}</Badge> : null}</Group>
      {error ? <Alert color="yellow" title="Graph control unavailable">{error}</Alert> : null}
      {readiness && !readiness.ready ? <Alert color="orange" title={readiness.state}>{readiness.reason || readiness.reason_code || 'Graph adapter is not ready.'}</Alert> : null}
      {status ? <><SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }}><Metric label="Adapter" value={`${status.adapter_name || 'unknown'} ${status.adapter_version || ''}`} /><Metric label="Revision" value={status.active_revision_id || 'Not indexed'} /><Metric label="Pending" value={`${status.pending_records} records`} /><Metric label="Queue age" value={`${status.queue_age_seconds}s`} /><Metric label="Watermark" value={String(status.indexed_watermark.sequence)} /><Metric label="Last success" value={status.last_successful_at ? new Date(status.last_successful_at).toLocaleString() : 'Never'} /><Metric label="Compatibility" value={status.compatible ? 'Supported' : 'Unsupported'} /><Metric label="Cost" value={status.cost_available ? `$${status.estimated_cost_usd.toFixed(4)}` : 'Not reported'} /></SimpleGrid>
      {status.remediation_code ? <Text size="sm" c="dimmed">Remediation: {status.remediation_code.replaceAll('_', ' ')}</Text> : null}
      <Group>{status.authorized_operations.map((action) => <Button key={action} size="xs" variant={action === 'disable' ? 'outline' : 'light'} color={action === 'disable' ? 'red' : undefined} disabled={busy || (action === 'retry' && !status.last_job_id)} onClick={() => void operate(action)}>{action[0].toUpperCase() + action.slice(1)}</Button>)}<Button size="xs" variant="default" loading={busy} onClick={() => void load()}>Refresh</Button></Group></> : null}
    </Stack></Card>
    {status?.active_revision_id ? <GraphExplorer gateway={gateway} workspaceId={workspaceId} /> : <Alert color="gray" title="No active graph revision">Run a rebuild after the adapter is ready. Basic retrieval is unaffected.</Alert>}
  </Stack>
}

function Metric({ label, value }: { label: string; value: string }) { return <div><Text size="xs" c="dimmed" tt="uppercase" fw={700}>{label}</Text><Text size="sm" style={{ overflowWrap: 'anywhere' }}>{value}</Text></div> }
