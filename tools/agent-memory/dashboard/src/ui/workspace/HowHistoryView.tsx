import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { Alert, Badge, Button, Card, Drawer, Group, Loader, Paper, Stack, Text, Title, UnstyledButton } from '@mantine/core'
import { IconChevronRight, IconRefresh } from '@tabler/icons-react'
import type { KnowledgeGateway, KnowledgeResult, SolutionEpisodeDetail, SolutionEpisodeSummary } from '../../lib/knowledgeGateway'

export function HowHistoryView({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [episodes, setEpisodes] = useState<SolutionEpisodeSummary[]>([])
  const [details, setDetails] = useState<Record<string, SolutionEpisodeDetail>>({})
  const [expandedId, setExpandedId] = useState('')
  const [ungrouped, setUngrouped] = useState<KnowledgeResult[]>([])
  const [selectedMemory, setSelectedMemory] = useState<KnowledgeResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [detailLoading, setDetailLoading] = useState('')
  const [error, setError] = useState('')

  async function load() {
    setLoading(true)
    setError('')
    try {
      const [history, memories] = await Promise.all([
        gateway.listHowHistory({ workspaceId }),
        gateway.browse({ workspaceId }, 'ungrouped'),
      ])
      setEpisodes(history)
      setUngrouped(memories.items)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'How History could not be loaded.')
    } finally { setLoading(false) }
  }

  useEffect(() => {
    setEpisodes([])
    setDetails({})
    setExpandedId('')
    setUngrouped([])
    void load()
  }, [gateway, workspaceId])

  async function toggle(episode: SolutionEpisodeSummary) {
    if (expandedId === episode.id) {
      setExpandedId('')
      return
    }
    setExpandedId(episode.id)
    if (details[episode.id]) return
    setDetailLoading(episode.id)
    setError('')
    try {
      const detail = await gateway.getSolutionEpisode({ workspaceId }, episode.id)
      setDetails((current) => ({ ...current, [episode.id]: detail }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The solution path could not be loaded.')
    } finally { setDetailLoading('') }
  }

  return <Stack className="howHistoryView" gap="lg" aria-label="How History">
    <Group justify="space-between" align="flex-start"><div><Title order={2}>How History</Title><Text c="dimmed">Trace how work produced knowledge, where its evidence came from, and what feedback affects trust.</Text></div><Button variant="default" leftSection={<IconRefresh size={16} />} loading={loading} onClick={() => void load()}>Refresh</Button></Group>
    {error ? <Alert color="red" title="How History unavailable" role="alert">{error}</Alert> : null}
    {loading && !episodes.length ? <Group justify="center" py="xl"><Loader size="sm" /><Text c="dimmed">Loading solution history…</Text></Group> : null}
    {!loading && !episodes.length ? <Paper withBorder p="xl" radius="lg"><Title order={3}>No How history yet</Title><Text c="dimmed" mt="xs">Completed agent work will appear here when it has a structured solution episode.</Text></Paper> : null}
    <Stack role="tree" aria-label="Solution-path knowledge tree" gap="sm">
      {episodes.map((episode) => {
        const expanded = expandedId === episode.id
        const detail = details[episode.id]
        return <Card key={episode.id} className="howTreeRoot" withBorder radius="lg" padding={0}>
          <UnstyledButton className="howTreeRootButton" role="treeitem" aria-expanded={expanded} onClick={() => void toggle(episode)}>
            <IconChevronRight className="howTreeChevron" data-expanded={expanded || undefined} size={19} aria-hidden="true" />
            <Stack gap={5} style={{ flex: 1, minWidth: 0 }}><Group gap="xs"><Badge color="memory">How</Badge><Badge variant="outline">{episode.status}</Badge>{episode.validation ? <Badge variant="light">{episode.validation}</Badge> : null}</Group><Text fw={750}>{episode.goal}</Text>{episode.summary ? <Text c="dimmed" size="sm" lineClamp={2}>{episode.summary}</Text> : null}<Text c="dimmed" size="xs">Updated {new Date(episode.updatedAt).toLocaleString()}</Text></Stack>
          </UnstyledButton>
          {expanded ? <Stack role="group" className="howTreeBranches" gap="sm">
            {detailLoading === episode.id && !detail ? <Group p="md"><Loader size="xs" /><Text size="sm" c="dimmed">Loading provenance…</Text></Group> : null}
            {detail ? <>
              <TreeBranch label="Steps" count={detail.steps.length}>{detail.steps.map((step) => <Paper key={step.id} withBorder p="sm" radius="md"><Group gap="xs"><Badge variant="light">{step.ordinal}</Badge><Badge variant="outline">{step.kind}</Badge>{step.misleading ? <Badge color="orange">Misleading</Badge> : null}{step.redacted ? <Badge color="red">Redacted</Badge> : null}</Group><Text mt="xs">{step.summary}</Text>{step.rationale ? <Text size="sm" c="dimmed" mt={4}>{step.rationale}</Text> : null}</Paper>)}</TreeBranch>
              <TreeBranch label="What" count={detail.promotionTargets.length}>{detail.promotionTargets.length ? detail.promotionTargets.map((target) => <Paper key={target.promotionId} withBorder p="sm" radius="md"><Group justify="space-between"><Group gap="xs"><Badge variant="light">{target.memoryType || target.kind}</Badge><Badge color={target.availability === 'available' ? 'memory' : target.state === 'failed' ? 'red' : 'gray'}>{target.availability}</Badge></Group>{target.memory ? <Button size="xs" variant="subtle" onClick={() => setSelectedMemory(target.memory!)}>Open memory</Button> : null}</Group>{target.memory ? <Text mt="xs" lineClamp={3}>{target.memory.content}</Text> : <Text mt="xs" size="sm" c="dimmed">Target content is unavailable; the promotion state remains part of history.</Text>}</Paper>) : <EmptyBranch text="No durable memory or skill was promoted from this path." />}</TreeBranch>
              <TreeBranch label="Where" count={detail.evidence.length}>{detail.evidence.length ? detail.evidence.map((reference, index) => <Paper key={`${reference.kind}:${reference.targetId}:${reference.locator || ''}:${index}`} withBorder p="sm" radius="md"><Group justify="space-between"><Text size="sm"><strong>{reference.kind}</strong>: {reference.locator || reference.targetId}</Text><Badge variant="outline">{reference.resolution || 'unverified'}</Badge></Group></Paper>) : <EmptyBranch text="No explicit evidence reference was stored." />}</TreeBranch>
              <TreeBranch label="Feedback" count={detail.pathFeedback.length + detail.steps.filter((step) => step.misleading || step.redacted || step.reviewReason).length}>{detail.pathFeedback.map((feedback) => <Paper key={feedback.id} withBorder p="sm" radius="md"><Group justify="space-between"><Text size="sm">Path retrieval feedback</Text><Badge color={feedback.outcome === 'helpful' ? 'memory' : feedback.outcome === 'harmful' ? 'red' : 'orange'}>{feedback.outcome}</Badge></Group><Text c="dimmed" size="xs" mt={4}>{new Date(feedback.createdAt).toLocaleString()}</Text></Paper>)}{detail.steps.filter((step) => step.misleading || step.redacted || step.reviewReason).map((step) => <Paper key={`review:${step.id}`} withBorder p="sm" radius="md"><Group gap="xs"><Badge variant="outline">Step {step.ordinal}</Badge>{step.misleading ? <Badge color="orange">Misleading</Badge> : null}{step.redacted ? <Badge color="red">Redacted</Badge> : null}</Group><Text size="sm" mt="xs">{step.reviewReason || step.reasonClass || 'Reviewed without an explanation.'}</Text></Paper>)}{!detail.pathFeedback.length && !detail.steps.some((step) => step.misleading || step.redacted || step.reviewReason) ? <EmptyBranch text="No path or step feedback has been recorded." /> : null}</TreeBranch>
            </> : null}
          </Stack> : null}
        </Card>
      })}
    </Stack>
    <Stack gap="sm"><div><Title order={3}>Ungrouped memories</Title><Text c="dimmed" size="sm">Durable memories that do not have an explicit solution-path promotion.</Text></div>{ungrouped.length ? ungrouped.map((memory) => <Paper key={memory.id} withBorder p="md" radius="md"><Group justify="space-between"><Badge variant="light">{memory.memoryType || 'memory'}</Badge><Button size="xs" variant="subtle" onClick={() => setSelectedMemory(memory)}>Open memory</Button></Group><Text mt="xs" lineClamp={3}>{memory.content}</Text></Paper>) : <EmptyBranch text="No ungrouped memories in the current page." />}</Stack>
    <Drawer opened={Boolean(selectedMemory)} onClose={() => setSelectedMemory(null)} position="right" size="lg" title="Memory detail">{selectedMemory ? <Stack><Badge color="memory" variant="light">{selectedMemory.memoryType || 'memory'}</Badge><Text lh={1.7}>{selectedMemory.content}</Text><Text size="sm" c="dimmed">{selectedMemory.provenance || 'No source locator recorded.'}</Text></Stack> : null}</Drawer>
  </Stack>
}

function TreeBranch({ label, count, children }: { label: string; count: number; children: ReactNode }) {
  return <Paper role="treeitem" className="howTreeBranch" withBorder p="md" radius="md"><Group gap="xs" mb="sm"><Badge color="memory" variant="light">{label}</Badge><Badge variant="outline">{count}</Badge></Group><Stack gap="xs">{children}</Stack></Paper>
}

function EmptyBranch({ text }: { text: string }) {
  return <Text c="dimmed" size="sm">{text}</Text>
}
