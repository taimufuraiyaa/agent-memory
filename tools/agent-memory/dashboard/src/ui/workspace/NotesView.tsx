import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Grid, Group, Paper, ScrollArea, SegmentedControl, Stack, Text, TextInput, Textarea, Title, UnstyledButton } from '@mantine/core'
import { IconFilePlus, IconRefresh, IconRestore, IconTrash } from '@tabler/icons-react'
import type { KnowledgeGateway, NoteSummary, WorkspaceNote } from '../../lib/knowledgeGateway'
import { MarkdownView } from '../MarkdownView'

export function NotesView({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [notes, setNotes] = useState<NoteSummary[]>([])
  const [trash, setTrash] = useState<NoteSummary[]>([])
  const [showTrash, setShowTrash] = useState(false)
  const [active, setActive] = useState<WorkspaceNote | null>(null)
  const [preview, setPreview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const controllerRef = useRef<AbortController | null>(null)
  const scope = { workspaceId }

  async function refresh(signal?: AbortSignal) {
    const items = await gateway.listNotes(scope, true, signal)
    setNotes(items.filter((note) => !note.deleted))
    setTrash(items.filter((note) => note.deleted))
  }

  useEffect(() => {
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setActive(null)
    setError('')
    refresh(controller.signal).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Notes are unavailable in this workspace.')
    })
    return () => controller.abort()
  }, [gateway, workspaceId])

  async function open(noteId: string) {
    setBusy(true)
    setError('')
    try { setActive(await gateway.getNote(scope, noteId)) }
    catch (reason) { setError(reason instanceof Error ? reason.message : 'The note could not be opened.') }
    finally { setBusy(false) }
  }

  async function create() {
    setBusy(true)
    try {
      const title = `Untitled ${notes.length + 1}`
      const note = await gateway.createNote(scope, { title, path: `${title}.md`, body: `# ${title}\n\n`, properties: {} })
      setActive(note)
      await refresh()
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'The note could not be created.') }
    finally { setBusy(false) }
  }

  async function save() {
    if (!active) return
    setBusy(true)
    try { setActive(await gateway.updateNote(scope, active)); await refresh() }
    catch (reason) { setError(reason instanceof Error ? reason.message : 'The note could not be saved.') }
    finally { setBusy(false) }
  }

  async function moveToTrash(noteId: string) {
    await gateway.trashNote(scope, noteId)
    if (active?.id === noteId) setActive(null)
    await refresh()
  }

  async function restore(noteId: string) { await gateway.restoreNote(scope, noteId); await refresh() }

  async function remove(noteId: string, title: string) {
    if (!window.confirm(`Permanently delete “${title}”? This cannot be undone.`)) return
    await gateway.deleteNote(scope, noteId)
    await refresh()
  }

  async function retryIndex() {
    if (!active) return
    await gateway.retryNoteIndex(scope, active.id)
    setActive({ ...active, indexState: 'pending', indexError: undefined })
  }

  const visibleNotes = showTrash ? trash : notes

  return <Stack className="notesWorkspace" gap="md">
    {error ? <Alert color="red" title="Notes unavailable" role="alert">{error}</Alert> : null}
    <Grid gutter="md" align="stretch">
      <Grid.Col span={{ base: 12, md: 4, lg: 3 }}>
        <Paper className="notesExplorer" withBorder p="sm" radius="lg" h="100%">
          <Stack gap="sm">
            <Group><Button size="xs" leftSection={<IconFilePlus size={15} />} onClick={() => void create()} loading={busy}>New note</Button><Button size="xs" variant={showTrash ? 'light' : 'default'} leftSection={<IconTrash size={15} />} onClick={() => setShowTrash((value) => !value)}>Trash</Button><Text size="xs" c="dimmed">{trash.length}</Text></Group>
            <ScrollArea h={520} type="auto"><Stack component="nav" aria-label={showTrash ? 'Trashed notes' : 'Notes'} gap={4}>
              {visibleNotes.map((note) => <Paper key={note.id} withBorder p="xs" radius="md"><Stack gap="xs"><UnstyledButton className="noteListItem" data-active={active?.id === note.id || undefined} aria-current={active?.id === note.id ? 'true' : undefined} onClick={() => void open(note.id)}><Text fw={650}>{note.title}</Text><Text size="xs" c="dimmed" lineClamp={1}>{note.path}</Text></UnstyledButton>{showTrash ? <Group gap="xs"><Button size="compact-xs" variant="light" leftSection={<IconRestore size={13} />} onClick={() => void restore(note.id)}>Restore</Button><Button size="compact-xs" color="red" variant="subtle" onClick={() => void remove(note.id, note.title)}>Delete</Button></Group> : <Button size="compact-xs" variant="subtle" color="red" leftSection={<IconTrash size={13} />} onClick={() => void moveToTrash(note.id)}>Trash</Button>}</Stack></Paper>)}
              {!visibleNotes.length ? <Text c="dimmed" size="sm" p="md">{showTrash ? 'Trash is empty.' : 'Create the first note in this workspace.'}</Text> : null}
            </Stack></ScrollArea>
          </Stack>
        </Paper>
      </Grid.Col>
      <Grid.Col span={{ base: 12, md: 8, lg: 9 }}>
        <Paper className="noteEditor" withBorder p={{ base: 'md', md: 'lg' }} radius="lg" h="100%">
          {active ? <Stack gap="md">
            <Group justify="space-between" align="flex-start"><div><Text c="memory" size="xs" fw={700} tt="uppercase">Workspace note</Text><Title order={2}>Editor</Title></div><Group><SegmentedControl value={preview ? 'preview' : 'edit'} onChange={(value) => setPreview(value === 'preview')} data={[{ value: 'edit', label: 'Edit' }, { value: 'preview', label: 'Markdown preview' }]} /><Button onClick={() => void save()} loading={busy}>Save</Button></Group></Group>
            <TextInput label="Note title" value={active.title} onChange={(event) => setActive({ ...active, title: event.currentTarget.value })} />
            <TextInput label="Note path" value={active.path} onChange={(event) => setActive({ ...active, path: event.currentTarget.value })} />
            {active.indexState === 'failed' ? <Alert color="red" title="Indexing failed" role="alert">{active.indexError || 'Indexing failed.'}<Button mt="sm" size="xs" variant="light" leftSection={<IconRefresh size={15} />} onClick={() => void retryIndex()}>Retry indexing</Button></Alert> : <Text size="sm" c="dimmed">Index: {active.indexState} · revision {active.revision}</Text>}
            {preview ? <Paper className="noteMarkdownPreview" withBorder p="lg" radius="md"><MarkdownView markdown={active.body} clamp={false} theme="dark" /></Paper> : <Textarea label="Markdown editor" value={active.body} onChange={(event) => setActive({ ...active, body: event.currentTarget.value })} autosize minRows={18} maxRows={36} styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />}
          </Stack> : <Stack align="center" justify="center" mih={520}><Text c="dimmed">{busy ? 'Opening note…' : 'Choose a note or create a new one.'}</Text></Stack>}
        </Paper>
      </Grid.Col>
    </Grid>
  </Stack>
}
