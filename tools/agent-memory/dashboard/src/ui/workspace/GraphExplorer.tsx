import { Alert, Badge, Button, Card, Group, SegmentedControl, Stack, Tabs, Text, Title } from '@mantine/core'
import { useEffect, useMemo, useState } from 'react'
import type { GraphReviewInput, GraphSnapshot, KnowledgeGateway } from '../../lib/knowledgeGateway'
import { GraphReview } from './GraphReview'

type ReviewTarget = { kind: GraphReviewInput['targetKind']; id: string; trust: string; version: number; label: string }

function EvidenceList({ evidence }: { evidence: GraphSnapshot['nodes'][number]['evidence'] }) {
  return <Stack gap={2}><Text size="xs" fw={700}>Canonical evidence</Text>{evidence.length ? evidence.map((item) => <Text key={`${item.canonical_kind}:${item.canonical_id}:${item.canonical_fingerprint}`} size="xs" c="dimmed">{item.canonical_kind}: {item.canonical_id}{item.locator ? ` · ${item.locator}` : ''}</Text>) : <Text size="xs" c="dimmed">No canonical evidence is attached.</Text>}</Stack>
}

export function GraphExplorer({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [snapshot, setSnapshot] = useState<GraphSnapshot | null>(null)
  const [view, setView] = useState<'candidates' | 'rejected' | 'all'>('candidates')
  const [target, setTarget] = useState<ReviewTarget | null>(null)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  async function load(signal?: AbortSignal) {
    setBusy(true); setError('')
    try { setSnapshot(await gateway.getGraphSnapshot({ workspaceId }, signal)) } catch (cause) { if (!signal?.aborted) setError(cause instanceof Error ? cause.message : 'Graph explorer is unavailable.') } finally { if (!signal?.aborted) setBusy(false) }
  }
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [gateway, workspaceId])
  const visible = (trust: string) => view === 'all' || (view === 'rejected' ? trust === 'rejected' : trust !== 'rejected' && trust !== 'deleted' && trust !== 'superseded')
  const nodes = useMemo(() => (snapshot?.nodes || []).filter((item) => visible(item.entity.trust)), [snapshot, view])
  const edges = useMemo(() => (snapshot?.edges || []).filter((item) => visible(item.edge.trust)), [snapshot, view])
  const communities = useMemo(() => (snapshot?.communities || []).filter((item) => visible(item.report.trust)), [snapshot, view])
  async function review(input: GraphReviewInput) { await gateway.reviewGraph({ workspaceId }, input); await load() }
  if (error) return <Alert color="yellow" title="Graph explorer unavailable">{error}</Alert>
  return <Stack gap="md" aria-busy={busy}>
    <Group justify="space-between"><div><Title order={3}>Graph explorer</Title><Text c="dimmed" size="sm">Inspect relationships and their canonical provenance. Reports help navigation and are never source evidence.</Text></div><Button variant="default" loading={busy} onClick={() => void load()}>Refresh</Button></Group>
    {snapshot ? <Group><Badge variant="light">Revision {snapshot.revision_id || 'none'}</Badge><Badge color={snapshot.fresh ? 'green' : 'yellow'}>{snapshot.fresh ? 'Fresh' : 'Stale'}</Badge></Group> : null}
    <SegmentedControl aria-label="Graph review queue" value={view} onChange={(value) => setView(value as typeof view)} data={[{ value: 'candidates', label: 'Candidates' }, { value: 'rejected', label: 'Rejected' }, { value: 'all', label: 'All' }]} />
    <Tabs defaultValue="entities">
      <Tabs.List><Tabs.Tab value="entities">Entities ({nodes.length})</Tabs.Tab><Tabs.Tab value="relationships">Relationships ({edges.length})</Tabs.Tab><Tabs.Tab value="communities">Communities ({communities.length})</Tabs.Tab></Tabs.List>
      <Tabs.Panel value="entities" pt="md"><Stack>{nodes.map((item) => <Card key={item.entity.id} withBorder><Group justify="space-between" align="flex-start"><div><Group gap="xs"><Text fw={700}>{item.version.name}</Text><Badge size="sm" variant="outline">{item.version.entity_type}</Badge><Badge size="sm">{item.entity.trust}</Badge></Group><Text size="sm" mt="xs">{item.version.description}</Text><EvidenceList evidence={item.evidence} /></div><Button size="xs" variant="light" onClick={() => setTarget({ kind: 'entity', id: item.entity.id, trust: item.entity.trust, version: item.record_version, label: item.version.name })}>Review</Button></Group></Card>)}</Stack></Tabs.Panel>
      <Tabs.Panel value="relationships" pt="md"><Stack>{edges.map((item) => <Card key={item.edge.id} withBorder><Group justify="space-between" align="flex-start"><div><Group gap="xs"><Text fw={700}>{item.edge.normalized_kind}</Text><Badge size="sm">{item.edge.trust}</Badge><Badge size="sm" variant="outline">{item.version.origin}</Badge></Group><Text size="sm">{item.edge.source_entity_id} → {item.edge.target_entity_id}</Text><Text size="sm" c="dimmed">{item.version.description}</Text><EvidenceList evidence={item.evidence} /></div><Button size="xs" variant="light" onClick={() => setTarget({ kind: 'edge', id: item.edge.id, trust: item.edge.trust, version: item.record_version, label: item.edge.normalized_kind })}>Review</Button></Group></Card>)}</Stack></Tabs.Panel>
      <Tabs.Panel value="communities" pt="md"><Stack>{communities.map((item) => <Card key={item.community.id} withBorder><Group justify="space-between" align="flex-start"><div><Group gap="xs"><Text fw={700}>{item.report.title || 'Untitled community'}</Text><Badge size="sm">{item.report.trust}</Badge>{item.report.stale ? <Badge size="sm" color="yellow">Stale</Badge> : null}{item.community.unresolved_count ? <Badge size="sm" color="orange">{item.community.unresolved_count} ambiguous carry-forward</Badge> : null}</Group><Alert mt="sm" color="blue" title="Navigation summary — not source evidence">{item.report.summary}</Alert><EvidenceList evidence={item.evidence} /></div><Button size="xs" variant="light" onClick={() => setTarget({ kind: 'report', id: item.report.id, trust: item.report.trust, version: item.report.review_version, label: item.report.title || 'community report' })}>Review</Button></Group></Card>)}</Stack></Tabs.Panel>
    </Tabs>
    <GraphReview opened={Boolean(target)} target={target} onClose={() => setTarget(null)} onSubmit={review} />
  </Stack>
}
