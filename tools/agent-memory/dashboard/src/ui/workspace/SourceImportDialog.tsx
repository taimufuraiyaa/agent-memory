import { useEffect, useState } from 'react'
import { Alert, Button, FileInput, Group, Modal, SegmentedControl, Select, Stack, Text } from '@mantine/core'
import { IconFileDescription, IconLock, IconNote, IconUpload } from '@tabler/icons-react'
import type { KnowledgeGateway, SourceSummary, SourceUploadInput } from '../../lib/knowledgeGateway'

type RightsBasis = SourceUploadInput['rightsBasis']

const rightsOptions: Array<{ value: RightsBasis; label: string }> = [
  { value: 'author-owned', label: 'I created or own it' },
  { value: 'licensed', label: 'I have permission or a license' },
  { value: 'public-domain-or-open', label: 'Public domain or compatible open license' },
  { value: 'lawfully-acquired-private-use', label: 'Lawfully acquired for private use' },
]

export function SourceImportDialog({ gateway, workspaceId, open, onClose, onImported, onCreateNote }: {
  gateway: KnowledgeGateway
  workspaceId: string
  open: boolean
  onClose: () => void
  onImported: (source: SourceSummary) => void
  onCreateNote: () => void
}) {
  const [file, setFile] = useState<File | null>(null)
  const [rights, setRights] = useState<RightsBasis | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) {
      setFile(null)
      setRights(null)
      setError('')
    }
  }, [open])

  async function upload() {
    if (!file || !rights || busy) return
    setBusy(true)
    setError('')
    try {
      const extension = file.name.toLowerCase().split('.').pop()
      const format = extension === 'pdf' || extension === 'epub' ? extension : extension === 'md' || extension === 'markdown' ? 'markdown' : extension === 'txt' ? 'text' : null
      if (!format) throw new Error('Choose a PDF, EPUB, Markdown, or plain-text file.')
      const source = await gateway.uploadSource({ scope: { workspaceId }, file, format, rightsBasis: rights })
      onImported(source)
      onClose()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'The source could not be uploaded.')
    } finally {
      setBusy(false)
    }
  }

  return <Modal opened={open} onClose={onClose} title="Add source" size="lg" closeButtonProps={{ 'aria-label': 'Close Add source' }}>
    <Stack gap="lg">
      <div><Text size="xs" fw={700} c="memory" tt="uppercase">{workspaceId} · Sources</Text><Text c="dimmed" size="sm">Import a private document without leaving this workspace.</Text></div>
      <SegmentedControl fullWidth value="upload" onChange={(value) => { if (value === 'note') onCreateNote() }} data={[
        { value: 'upload', label: <Group gap={6} justify="center"><IconUpload size={16} />Upload document</Group> },
        { value: 'note', label: <Group gap={6} justify="center"><IconNote size={16} />Create note</Group> },
      ]} />
      <FileInput label="Document" description="PDF · EPUB · Markdown · plain text" placeholder="Choose a file" accept=".pdf,.epub,.md,.markdown,.txt,application/pdf,application/epub+zip,text/markdown,text/plain" leftSection={<IconFileDescription size={18} />} value={file} onChange={setFile} clearable required />
      <Select label="Rights basis" placeholder="Select why you may process this file…" data={rightsOptions} value={rights} onChange={(value) => setRights(value as RightsBasis | null)} required />
      <Alert color="blue" icon={<IconLock size={18} />} title="Private, deterministic extraction">Text is extracted deterministically. A scanned PDF becomes <strong>OCR required</strong>; it is never shown as an empty ready source.</Alert>
      {error ? <Alert color="red" title="Upload failed" role="alert">{error}</Alert> : null}
      <Group justify="flex-end"><Button variant="default" onClick={onClose}>Cancel</Button><Button leftSection={<IconUpload size={17} />} loading={busy} disabled={!file || !rights} onClick={() => void upload()}>Upload privately</Button></Group>
    </Stack>
  </Modal>
}
