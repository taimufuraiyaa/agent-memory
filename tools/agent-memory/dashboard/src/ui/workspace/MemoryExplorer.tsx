import { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Badge, Button, Checkbox, Drawer, Group, Loader, Paper, SegmentedControl, Stack, Text, TextInput, Title } from '@mantine/core'
import { IconDownload, IconSearch, IconTrash, IconX } from '@tabler/icons-react'
import type { KnowledgeGateway, KnowledgeResult, WorkspaceScope } from '../../lib/knowledgeGateway'
import { KnowledgeResultCard } from './KnowledgeResultCard'

type MemoryMode = 'recent' | 'pinned' | 'type'

export function MemoryExplorer({ gateway, workspaceId, initialView = 'search' }: { gateway: KnowledgeGateway; workspaceId: string; initialView?: 'search' | 'browse' }) {
  const [view, setView] = useState<'search' | 'browse'>(initialView)
  const [mode, setMode] = useState<MemoryMode>('recent')
  const [query, setQuery] = useState('')
  const [items, setItems] = useState<KnowledgeResult[]>([])
  const [cursor, setCursor] = useState<string | undefined>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<KnowledgeResult | null>(null)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const controllerRef = useRef<AbortController | null>(null)
  const scope: WorkspaceScope = { workspaceId }
  const grouped = useMemo(() => groupByType(items), [items])

  useEffect(() => {
    controllerRef.current?.abort()
    setItems([])
    setCursor(undefined)
    setSelected(null)
    setSelectedIds(new Set())
    setError('')
    return () => controllerRef.current?.abort()
  }, [workspaceId])

  useEffect(() => { setView(initialView) }, [initialView])
  useEffect(() => { if (view === 'browse') void load(false) }, [view, mode, workspaceId])

  async function load(append: boolean) {
    if (busy) return
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setBusy(true)
    setError('')
    try {
      const pageCursor = append ? cursor : undefined
      const page = view === 'search' ? await gateway.search(scope, query, pageCursor, controller.signal) : await gateway.browse(scope, mode, pageCursor, controller.signal)
      if (!controller.signal.aborted) {
        setItems((current) => append ? [...current, ...page.items] : page.items)
        setCursor(page.nextCursor)
      }
    } catch (reason) {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Memories could not be loaded.')
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  async function togglePin(result: KnowledgeResult) {
    await gateway.setMemoryPinned(scope, result.id, !result.pinned)
    setItems((current) => current.map((item) => item.id === result.id ? { ...item, pinned: !item.pinned, actions: item.actions.map((action) => action === 'pin' ? 'unpin' : action === 'unpin' ? 'pin' : action) } : item))
  }

  async function remove(result: KnowledgeResult) {
    if (!window.confirm('Delete this memory from the selected workspace?')) return
    await gateway.deleteMemories(scope, [result.id])
    setItems((current) => current.filter((item) => item.id !== result.id))
    if (selected?.id === result.id) setSelected(null)
  }

  const selectedItems = items.filter((item) => selectedIds.has(item.id))

  async function removeSelected() {
    if (!selectedItems.length || !window.confirm(`Delete ${selectedItems.length} selected memories from this workspace?`)) return
    await gateway.deleteMemories(scope, selectedItems.map((item) => item.id))
    setItems((current) => current.filter((item) => !selectedIds.has(item.id)))
    setSelectedIds(new Set())
    if (selected && selectedIds.has(selected.id)) setSelected(null)
  }

  function exportSelected() {
    if (!selectedItems.length) return
    const blob = new Blob([JSON.stringify({ workspace: workspaceId, memories: selectedItems }, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url; anchor.download = `agent-memory-${workspaceId}.json`; anchor.click(); URL.revokeObjectURL(url)
  }

  function printSelected() {
    if (!selectedItems.length) return
    const popup = window.open('', '_blank', 'noopener,noreferrer')
    if (!popup) return
    const escape = (value: string) => value.replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character)
    popup.document.write(`<title>${escape(workspaceId)} memories</title>${selectedItems.map((item) => `<article><h2>${escape(item.memoryType || 'Memory')}</h2><pre>${escape(item.content)}</pre><p>${escape(item.provenance || '')}</p></article>`).join('')}`)
    popup.document.close(); popup.print()
  }

  return <Stack className="memoryExplorer" gap="md">
    <SegmentedControl value={view} onChange={(value) => { const next = value as 'search' | 'browse'; setView(next); if (next === 'search') { setItems([]); setCursor(undefined) } }} aria-label="Memory discovery mode" data={[{ value: 'search', label: 'Search' }, { value: 'browse', label: 'Browse' }]} />
    {view === 'search' ? <Paper component="form" withBorder p="md" radius="lg" onSubmit={(event) => { event.preventDefault(); void load(false) }}><Group align="flex-end"><TextInput style={{ flex: 1 }} label="Search memories" type="search" value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Search durable memories…" leftSection={<IconSearch size={17} />} /><Button type="submit" loading={busy} disabled={!query.trim()}>Search</Button></Group></Paper> : <SegmentedControl value={mode} onChange={(value) => setMode(value as MemoryMode)} aria-label="Browse memories" data={[{ value: 'recent', label: 'Recent' }, { value: 'pinned', label: 'Pinned' }, { value: 'type', label: 'By type' }]} />}
    {error ? <Alert color="red" title="Memories unavailable" role="alert">{error}</Alert> : null}
    {selectedIds.size ? <Paper withBorder p="sm" radius="md" role="toolbar" aria-label="Selected memory actions"><Group><Badge variant="light">{selectedIds.size} selected</Badge><Button size="xs" variant="light" leftSection={<IconDownload size={15} />} onClick={exportSelected}>Export JSON</Button><Button size="xs" variant="light" onClick={printSelected}>Print selected</Button><Button size="xs" color="red" variant="light" leftSection={<IconTrash size={15} />} onClick={() => void removeSelected()}>Delete selected</Button><Button size="xs" variant="subtle" leftSection={<IconX size={15} />} onClick={() => setSelectedIds(new Set())}>Clear</Button></Group></Paper> : null}
    <Stack gap="md">
      {mode === 'type' && view === 'browse' ? Object.entries(grouped).map(([type, results]) => <Stack className="memoryGroup" key={type} gap="sm"><Title order={2}>{type}</Title><ResultList items={results} selected={selected} selectedIds={selectedIds} onSelect={(id, checked) => setSelectedIds((current) => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })} onOpen={setSelected} onTogglePin={(item) => void togglePin(item)} onDelete={(item) => void remove(item)} /></Stack>) : <ResultList items={items} selected={selected} selectedIds={selectedIds} onSelect={(id, checked) => setSelectedIds((current) => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })} onOpen={setSelected} onTogglePin={(item) => void togglePin(item)} onDelete={(item) => void remove(item)} />}
      {!busy && !items.length ? <Paper withBorder p="xl" radius="lg"><Text c="dimmed" ta="center">{view === 'search' ? 'Enter a query to search this workspace.' : 'No memories in this view.'}</Text></Paper> : null}
      {busy ? <Group justify="center" py="xl" aria-live="polite"><Loader size="sm" /><Text c="dimmed">Loading memories…</Text></Group> : null}
      {cursor ? <Button variant="light" onClick={() => void load(true)} loading={busy}>Load more</Button> : null}
    </Stack>
    <Drawer opened={Boolean(selected)} onClose={() => setSelected(null)} position="right" size="lg" title="Memory detail" aria-label="Memory detail">
      {selected ? <Stack><Badge variant="light" color="memory">{selected.memoryType || 'Memory'}</Badge><Text lh={1.7}>{selected.content}</Text><Paper withBorder p="md"><Stack gap="xs"><Group justify="space-between"><Text c="dimmed" size="sm">Workspace</Text><Text size="sm">{selected.workspaceId}</Text></Group><Group justify="space-between"><Text c="dimmed" size="sm">Provenance</Text><Text size="sm">{selected.provenance || 'Not recorded'}</Text></Group><Group justify="space-between"><Text c="dimmed" size="sm">Confidence</Text><Text size="sm">{typeof selected.confidence === 'number' ? selected.confidence.toFixed(2) : 'Not scored'}</Text></Group><Group justify="space-between"><Text c="dimmed" size="sm">Updated</Text><Text size="sm">{selected.updatedAt ? new Date(selected.updatedAt).toLocaleString() : 'Unknown'}</Text></Group></Stack></Paper></Stack> : null}
    </Drawer>
  </Stack>
}

function ResultList({ items, selected, selectedIds, onSelect, onOpen, onTogglePin, onDelete }: { items: KnowledgeResult[]; selected: KnowledgeResult | null; selectedIds: Set<string>; onSelect: (id: string, checked: boolean) => void; onOpen: (item: KnowledgeResult) => void; onTogglePin: (item: KnowledgeResult) => void; onDelete: (item: KnowledgeResult) => void }) {
  return <div className="knowledgeResultList">{items.map((item) => <Paper className="memorySelectable" key={item.id} bg="transparent"><Checkbox className="memorySelectControl" label="Select memory" checked={selectedIds.has(item.id)} onChange={(event) => onSelect(item.id, event.currentTarget.checked)} /><KnowledgeResultCard result={item} selected={selected?.id === item.id} onOpen={() => onOpen(item)} onTogglePin={() => onTogglePin(item)} onDelete={() => onDelete(item)} /></Paper>)}</div>
}

function groupByType(items: KnowledgeResult[]): Record<string, KnowledgeResult[]> {
  return items.reduce<Record<string, KnowledgeResult[]>>((groups, item) => {
    const key = item.memoryType || 'other'
    groups[key] = [...(groups[key] || []), item]
    return groups
  }, {})
}
