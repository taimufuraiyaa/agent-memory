import { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Badge, Button, Grid, Group, NumberInput, Paper, Select, SimpleGrid, Stack, Switch, Text, Title, UnstyledButton } from '@mantine/core'
import { IconAlertTriangle, IconArrowLeft, IconBook2, IconChevronRight, IconCode, IconFileText, IconMessageCircleQuestion, IconPlayerPlay, IconRefresh, IconSearch, IconTrash } from '@tabler/icons-react'
import type { KnowledgeGateway, SourceSummary, StudyResult } from '../../lib/knowledgeGateway'

const sourceKinds: SourceSummary['kind'][] = ['codebase', 'document', 'note']
const processingStates: SourceSummary['state'][] = ['uploading', 'parsing', 'ocr-required', 'ocr-processing', 'indexing', 'ready', 'failed']

function stateColor(state: SourceSummary['state'], hasFailure = false): string {
  if (hasFailure) return 'red'
  if (state === 'ready') return 'memory'
  if (state === 'failed' || state === 'ocr-required') return 'red'
  return 'blue'
}

function sourceStatus(source: SourceSummary): string {
  return source.failure ? 'Needs attention' : processingStates.includes(source.state) ? source.statusLabel : 'Registered'
}

function SourceIcon({ kind }: { kind: SourceSummary['kind'] }) {
  if (kind === 'codebase') return <IconCode size={18} />
  if (kind === 'note') return <IconBook2 size={18} />
  return <IconFileText size={18} />
}

export function SourcesView({ gateway, workspaceId, importedSource, onNavigate }: {
  gateway: KnowledgeGateway
  workspaceId: string
  importedSource?: SourceSummary | null
  onNavigate: (destination: 'ask' | 'search' | 'browse') => void
}) {
  const [sources, setSources] = useState<SourceSummary[]>([])
  const [selectedId, setSelectedId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [depth, setDepth] = useState<'shallow' | 'medium' | 'deep'>('medium')
  const [maxFiles, setMaxFiles] = useState(200)
  const [preview, setPreview] = useState(false)
  const [pages, setPages] = useState<StudyResult[]>([])
  const [pageIndex, setPageIndex] = useState(0)
  const controllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setSources([])
    setSelectedId('')
    setPages([])
    setError('')
    gateway.listSources({ workspaceId }, controller.signal).then((items) => {
      if (controller.signal.aborted) return
      setSources(items)
      setSelectedId(items[0]?.id || '')
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Sources could not be loaded.')
    })
    return () => controller.abort()
  }, [gateway, workspaceId])

  useEffect(() => {
    if (!importedSource || importedSource.workspaceId !== workspaceId) return
    setSources((current) => [importedSource, ...current.filter((source) => source.id !== importedSource.id)])
    setSelectedId(importedSource.id)
  }, [importedSource, workspaceId])

  useEffect(() => {
    const active = sources.some((source) => ['uploading', 'parsing', 'ocr-processing', 'indexing'].includes(source.state))
    if (!active) return
    const timer = window.setInterval(() => gateway.listSources({ workspaceId }).then((items) => setSources(items)).catch(() => undefined), 3000)
    return () => window.clearInterval(timer)
  }, [gateway, sources, workspaceId])

  const selected = sources.find((source) => source.id === selectedId) || null
  const result = pages[pageIndex] || null
  const counts = useMemo(() => sourceKinds.map((kind) => ({ kind, count: sources.filter((source) => source.kind === kind).length })), [sources])

  async function runStudy(offset: number, writePreview = false) {
    if (busy || !selected || selected.kind !== 'codebase') return
    setBusy(true)
    setError('')
    try {
      const next = await gateway.study({ workspaceId, sourceId: selected.id, depth, preview: writePreview ? false : preview, maxFiles, offset })
      const existing = pages.findIndex((page) => page.offset === next.offset && page.preview === next.preview)
      if (existing >= 0) {
        setPages((current) => current.map((page, index) => index === existing ? next : page))
        setPageIndex(existing)
      } else {
        setPages((current) => [...current, next])
        setPageIndex(pages.length)
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Project study could not complete.')
    } finally {
      setBusy(false)
    }
  }

  async function removeSource(source: SourceSummary) {
    if (!window.confirm(`Move “${source.title}” to Trash or begin its protected deletion workflow?`)) return
    setBusy(true)
    setError('')
    try {
      await gateway.deleteSource({ workspaceId }, source.id)
      setSources((current) => current.filter((item) => item.id !== source.id))
      setSelectedId((current) => current === source.id ? '' : current)
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'The source could not be removed.') }
    finally { setBusy(false) }
  }

  return <Stack className="sourcesWorkspace" gap="md">
    <SimpleGrid cols={{ base: 1, xs: 3 }} aria-label="Source inventory">{counts.map((item) => <Paper key={item.kind} withBorder p="md" radius="lg"><Text size="xs" c="dimmed" tt="uppercase">{item.kind}</Text><Text size="xl" fw={700}>{item.count}</Text></Paper>)}</SimpleGrid>
    {error ? <Alert color="red" title="Sources unavailable" role="alert">{error}</Alert> : null}
    <Grid gutter="md" align="stretch">
      <Grid.Col span={{ base: 12, md: 4, lg: 3 }}><Paper withBorder p="xs" radius="lg" h="100%" aria-label="Workspace sources"><Stack gap={4}>{sources.map((source) => <UnstyledButton className="sourceListItem" key={source.id} data-active={selectedId === source.id || undefined} aria-current={selectedId === source.id ? 'true' : undefined} onClick={() => setSelectedId(source.id)}><Group wrap="nowrap" align="flex-start"><SourceIcon kind={source.kind} /><div className="sourceListCopy"><Text size="xs" c="memory" tt="uppercase">{source.kind}</Text><Text fw={650} lineClamp={2}>{source.title}</Text><Badge size="xs" variant="light" color={stateColor(source.state, Boolean(source.failure))}>{sourceStatus(source)}</Badge></div><IconChevronRight className="sourceListChevron" size={16} /></Group></UnstyledButton>)}{!sources.length ? <Text c="dimmed" size="sm" p="md">No sources yet. Use Add source in the header.</Text> : null}</Stack></Paper></Grid.Col>
      <Grid.Col span={{ base: 12, md: 8, lg: 9 }}><Paper className="sourceDetail" withBorder p={{ base: 'md', md: 'xl' }} radius="lg" h="100%">
        {selected ? <Stack gap="lg">
          <Group justify="space-between" align="flex-start"><div><Text size="xs" c="memory" fw={700} tt="uppercase">{selected.kind} source</Text><Title order={2}>{selected.title}</Title><Text c="dimmed">{selected.format ? selected.format.toUpperCase() : selected.statusLabel}</Text></div><Stack gap="xs" align="flex-end"><Badge variant="light" color={stateColor(selected.state, Boolean(selected.failure))}>{sourceStatus(selected)}</Badge>{selected.kind !== 'codebase' ? <Button color="red" variant="light" size="xs" leftSection={<IconTrash size={15} />} onClick={() => void removeSource(selected)}>Remove source</Button> : null}</Stack></Group>
          {selected.failure ? <Alert color="red" icon={<IconAlertTriangle size={18} />} title={selected.failure.message} role="alert">{selected.failure.retryAllowed ? <Button mt="sm" size="xs" variant="light" leftSection={<IconRefresh size={15} />} onClick={() => void gateway.retrySource({ workspaceId }, selected.id)}>Retry</Button> : null}</Alert> : null}
          {selected.kind === 'codebase' ? <Stack className="studyWorkspace" gap="lg">
            <Paper component="form" withBorder p="md" radius="md" onSubmit={(event) => { event.preventDefault(); void runStudy(0) }}><Grid align="end"><Grid.Col span={{ base: 12, sm: 6, lg: 3 }}><Select label="Scan depth" data={[{ value: 'shallow', label: 'Shallow' }, { value: 'medium', label: 'Medium' }, { value: 'deep', label: 'Deep' }]} value={depth} onChange={(value) => value && setDepth(value as typeof depth)} /></Grid.Col><Grid.Col span={{ base: 12, sm: 6, lg: 3 }}><NumberInput label="Maximum files" min={1} max={200} value={maxFiles} onChange={(value) => setMaxFiles(Math.max(1, Math.min(200, Number(value) || 1)))} /></Grid.Col><Grid.Col span={{ base: 12, sm: 6, lg: 3 }}><Switch label="Preview without writing" checked={preview} onChange={(event) => setPreview(event.currentTarget.checked)} mb={8} /></Grid.Col><Grid.Col span={{ base: 12, sm: 6, lg: 3 }}><Button type="submit" fullWidth loading={busy} leftSection={<IconPlayerPlay size={17} />}>{preview ? 'Preview study' : 'Study project'}</Button></Grid.Col></Grid></Paper>
            {result ? <Paper className="studyResult" withBorder p="lg" radius="lg" aria-live="polite"><Stack gap="lg"><Group justify="space-between" align="flex-start"><div><Text size="xs" c="memory" fw={700} tt="uppercase">Batch {pageIndex + 1}</Text><Title order={3}>{result.preview ? 'Preview complete' : 'Study complete'}</Title></div><Badge variant="light" color={result.hasMore ? 'blue' : 'memory'}>{result.pageFiles} eligible files · {result.hasMore ? 'more available' : 'last batch'}</Badge></Group><SimpleGrid cols={{ base: 2, sm: 4 }}>{[{ label: 'Files scanned', value: result.scannedFiles }, { label: 'Extracted', value: result.extracted }, { label: 'Skipped', value: result.skipped }, { label: 'Written', value: result.writtenIds.length }].map((stat) => <Paper key={stat.label} bg="dark.8" p="md" radius="md"><Text size="xs" c="dimmed">{stat.label}</Text><Text size="xl" fw={700}>{stat.value}</Text></Paper>)}</SimpleGrid>{result.errors.length ? <Alert color="yellow" icon={<IconAlertTriangle size={18} />} title="Files needing attention"><Stack gap="xs">{result.errors.map((item) => <div key={`${item.path}:${item.reason}`}><Text fw={600} size="sm">{item.path}</Text><Text c="dimmed" size="sm">{item.reason}</Text></div>)}</Stack></Alert> : null}<Group gap="xs">{pageIndex > 0 ? <Button variant="default" leftSection={<IconArrowLeft size={16} />} onClick={() => setPageIndex((index) => index - 1)}>Previous batch</Button> : null}{result.preview ? <Button onClick={() => void runStudy(result.offset, true)}>Write this batch</Button> : <><Button variant="light" leftSection={<IconMessageCircleQuestion size={16} />} onClick={() => onNavigate('ask')}>Ask this workspace</Button><Button variant="light" leftSection={<IconSearch size={16} />} onClick={() => onNavigate('search')}>Search memories</Button><Button variant="light" leftSection={<IconBook2 size={16} />} onClick={() => onNavigate('browse')}>Browse memories</Button></>}{result.hasMore ? <Button ml="auto" rightSection={<IconChevronRight size={16} />} onClick={() => void runStudy(result.nextOffset)}>{result.preview ? 'Preview next batch' : 'Continue study'}</Button> : null}</Group></Stack></Paper> : null}
          </Stack> : <Text c="dimmed">{selected.failure ? 'Resolve this source failure before relying on it in workspace Ask and retrieval.' : 'This source participates in workspace Ask and retrieval when its state is ready.'}</Text>}
        </Stack> : <Text c="dimmed">Select a source to inspect it.</Text>}
      </Paper></Grid.Col>
    </Grid>
  </Stack>
}
