import { useEffect, useState } from 'react'
import { Alert, Badge, Button, Drawer, Group, Loader, Paper, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { IconActivity, IconBook2, IconMessageCircle, IconNotes, IconSettings } from '@tabler/icons-react'
import type { KnowledgeGateway, KnowledgeResult, WorkspaceSummary } from '../../lib/knowledgeGateway'
import { KnowledgeResultCard } from './KnowledgeResultCard'
import type { WorkspaceAction } from './WorkspaceExplorer'

export function WorkspaceOverviewPanel({ workspace, onAction }: { workspace: WorkspaceSummary; onAction: (action: WorkspaceAction) => void }) {
  return <Stack className="workspaceOverviewPanel" gap="md" aria-label="Workspace overview">
    <div><Text size="xs" tt="uppercase" fw={700} c="dimmed">Overview</Text><Title order={3} mt={4}>{workspace.name}</Title><Text size="sm" c="dimmed">{workspace.kind === 'registered-project' ? 'Registered project' : 'Knowledge workspace'}</Text></div>
    <SimpleGrid cols={2} spacing="xs">
      <Paper withBorder p="sm" radius="md"><Text size="xs" c="dimmed">Memories</Text><Text fw={700}>{workspace.memoryCount}</Text></Paper>
      <Paper withBorder p="sm" radius="md"><Text size="xs" c="dimmed">Sources</Text><Text fw={700}>{workspace.sourceCount}</Text></Paper>
      <Paper withBorder p="sm" radius="md"><Text size="xs" c="dimmed">Notes</Text><Text fw={700}>{workspace.noteCount}</Text></Paper>
      <Paper withBorder p="sm" radius="md"><Text size="xs" c="dimmed">Connection</Text><Badge size="xs" color={workspace.connectionState === 'connected' ? 'green' : 'gray'} variant="light">{workspace.connectionState}</Badge></Paper>
    </SimpleGrid>
    <Stack gap="xs">{workspace.capabilities.includes('ask') ? <Button variant="light" leftSection={<IconMessageCircle size={16} />} onClick={() => onAction('new-chat')}>New chat</Button> : null}{workspace.capabilities.includes('source') ? <Button variant="default" leftSection={<IconBook2 size={16} />} onClick={() => onAction('sources')}>Sources and study</Button> : null}{workspace.capabilities.includes('activity') ? <Button variant="default" leftSection={<IconActivity size={16} />} onClick={() => onAction('activity')}>Activity</Button> : null}{workspace.capabilities.includes('note') ? <Button variant="default" leftSection={<IconNotes size={16} />} onClick={() => onAction('notes')}>Notes</Button> : null}{workspace.capabilities.includes('settings') ? <Button variant="subtle" leftSection={<IconSettings size={16} />} onClick={() => onAction('settings')}>Settings</Button> : null}</Stack>
  </Stack>
}

export function WorkspaceRecentsPanel({ gateway, workspaceId, onBrowseAll }: { gateway: KnowledgeGateway; workspaceId: string; onBrowseAll: () => void }) {
  const [items, setItems] = useState<KnowledgeResult[]>([])
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<KnowledgeResult | null>(null)
  const canBrowse = gateway.supports('browse', { workspaceId })
  useEffect(() => {
    if (!canBrowse) { setBusy(false); setItems([]); return }
    const controller = new AbortController()
    setBusy(true); setError(''); setItems([])
    gateway.browse({ workspaceId }, 'recent', undefined, controller.signal).then((page) => { if (!controller.signal.aborted) setItems(page.items.slice(0, 6)) }).catch((reason) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Recent memories could not be loaded.') }).finally(() => { if (!controller.signal.aborted) setBusy(false) })
    return () => controller.abort()
  }, [gateway, workspaceId, canBrowse])
  if (!canBrowse) return <Stack className="workspaceRecentsPanel" gap="sm" aria-label="Recent memories"><Title order={3}>Recent memories</Title><Alert color="gray" title="Recents unavailable">This workspace does not support memory browsing.</Alert></Stack>
  return <Stack className="workspaceRecentsPanel" gap="sm" aria-label="Recent memories">
    <Group justify="space-between"><div><Text size="xs" tt="uppercase" fw={700} c="dimmed">Recents</Text><Title order={3} mt={4}>Recent memories</Title></div><Button size="xs" variant="subtle" onClick={onBrowseAll}>View all</Button></Group>
    {error ? <Alert color="red" title="Recents unavailable">{error}</Alert> : null}
    {busy ? <Group justify="center" py="xl" aria-live="polite"><Loader size="sm" /><Text size="sm" c="dimmed">Loading recent memories…</Text></Group> : null}
    {!busy && !error && !items.length ? <Paper withBorder p="md" radius="md"><Text size="sm" c="dimmed" ta="center">No recent memories in this workspace.</Text></Paper> : null}
    {items.map((item) => <KnowledgeResultCard key={`${item.workspaceId}:${item.id}`} result={item} previewLines={3} onOpen={() => setSelected(item)} />)}
    <Drawer opened={Boolean(selected)} onClose={() => setSelected(null)} position="right" size="md" title="Recent memory detail">
      {selected ? <Stack><Badge variant="light" color="memory">{selected.memoryType || 'Memory'}</Badge>{selected.title ? <Text fw={700}>{selected.title}</Text> : null}<Text lh={1.7} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{selected.content}</Text><Text size="sm" c="dimmed">{selected.provenance || 'Workspace memory'}</Text></Stack> : null}
    </Drawer>
  </Stack>
}
