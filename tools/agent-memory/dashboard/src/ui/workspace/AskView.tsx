import { useEffect, useRef, useState } from 'react'
import { Alert, Badge, Button, Group, Modal, Paper, Select, Stack, Text, Textarea, Title } from '@mantine/core'
import { IconArrowRight, IconBooks, IconMessageCircleQuestion, IconSearch, IconSparkles } from '@tabler/icons-react'
import type { AskResponse, KnowledgeGateway, KnowledgeResult, SourceSummary, WorkspaceScope } from '../../lib/knowledgeGateway'
import { KnowledgeResultCard } from './KnowledgeResultCard'

export function AskView({ gateway, workspaceId, onOpenSearch, onOpenSources }: {
  gateway: KnowledgeGateway
  workspaceId: string
  onOpenSearch: () => void
  onOpenSources: () => void
}) {
  const [question, setQuestion] = useState('')
  const [response, setResponse] = useState<AskResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [sources, setSources] = useState<SourceSummary[]>([])
  const [sourceId, setSourceId] = useState('')
  const [selectedEvidence, setSelectedEvidence] = useState<KnowledgeResult | null>(null)
  const [copiedEvidenceId, setCopiedEvidenceId] = useState('')
  const [copyStatus, setCopyStatus] = useState('')
  const controllerRef = useRef<AbortController | null>(null)
  const scope: WorkspaceScope = { workspaceId, sourceId }

  useEffect(() => {
    controllerRef.current?.abort()
    setResponse(null)
    setError('')
    setSourceId('')
    setSelectedEvidence(null)
    setCopiedEvidenceId('')
    setCopyStatus('')
    const sourceController = new AbortController()
    gateway.listSources({ workspaceId }, sourceController.signal).then((items) => {
      if (!sourceController.signal.aborted) setSources(items)
    }).catch(() => {
      if (!sourceController.signal.aborted) setSources([])
    })
    return () => { sourceController.abort(); controllerRef.current?.abort() }
  }, [gateway, workspaceId])

  async function submit() {
    const normalized = question.trim()
    if (!normalized || busy) return
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setBusy(true)
    setError('')
    setSelectedEvidence(null)
    setCopiedEvidenceId('')
    setCopyStatus('')
    try {
      const next = await gateway.ask(scope, question, controller.signal)
      if (!controller.signal.aborted) setResponse(next)
    } catch (reason) {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Ask could not complete.')
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  async function copyEvidence(evidence: KnowledgeResult) {
    try {
      const copyOperation = navigator.clipboard?.writeText(evidence.content)
      if (!copyOperation) throw new Error('Clipboard access is unavailable.')
      await copyOperation
      setCopiedEvidenceId(evidence.id)
      setCopyStatus('Evidence copied to clipboard.')
    } catch {
      setCopiedEvidenceId('')
      setCopyStatus('Evidence could not be copied. Check browser clipboard permissions and try again.')
    }
  }

  const sourceOptions = [{ value: '', label: 'All workspace sources' }, ...sources.map((source) => ({ value: source.id, label: `${source.title} · ${source.kind}` }))]
  const hasEvidence = Boolean(response && (response.sourceEvidence.length || response.durableMemory.length || response.weakContext.length))

  return <div className="askWorkspace" data-has-evidence={hasEvidence || undefined} aria-busy={busy}>
    <Stack className="askPrimary" gap="xl">
      <Paper component="form" className="askComposer" withBorder p={{ base: 'md', sm: 'xl' }} radius="lg" onSubmit={(event) => { event.preventDefault(); void submit() }}>
        <Stack gap="md">
          <Group justify="space-between" align="flex-start"><div><Text c="memory" size="xs" fw={700} tt="uppercase">Grounded workspace conversation</Text><Title order={2}>Ask this workspace</Title></div><IconMessageCircleQuestion size={28} color="var(--mantine-color-memory-5)" /></Group>
          <Select aria-label="Narrow Ask to source" label="Knowledge scope" data={sourceOptions} value={sourceId} onChange={(value) => { setSourceId(value || ''); setResponse(null); setSelectedEvidence(null); setCopiedEvidenceId(''); setCopyStatus('') }} searchable />
          <Textarea id="workspace-question" label="Question" value={question} onChange={(event) => setQuestion(event.currentTarget.value)} placeholder="Ask about decisions, code, documents, or notes…" autosize minRows={4} maxRows={10} />
          <Group justify="flex-end"><Button type="submit" loading={busy} disabled={!question.trim()} leftSection={<IconSparkles size={17} />}>Ask</Button></Group>
        </Stack>
      </Paper>
      {error ? <Alert color="red" title="Ask failed" role="alert">{error}</Alert> : null}
      {response ? response.answerable ? response.answer?.trim() ? <Paper className="askAnswer" withBorder p={{ base: 'md', sm: 'xl' }} radius="lg" aria-live="polite"><Text lh={1.7} style={{ whiteSpace: 'pre-wrap' }}>{response.answer}</Text></Paper> : null : <Alert color="yellow" title="No grounded answer" aria-live="polite"><Stack gap="md"><Text>{response.unavailableReason || 'The selected workspace does not have enough trusted context.'}</Text><Group><Button variant="light" leftSection={<IconSearch size={16} />} onClick={onOpenSearch}>Search memories</Button><Button variant="default" leftSection={<IconBooks size={16} />} onClick={onOpenSources}>Review Sources</Button></Group></Stack></Alert> : null}
    </Stack>
    {response && hasEvidence ? <Stack className="askEvidence" gap="xl" aria-live="polite">
      {response.sourceEvidence.length ? <ResultSection title="Source evidence" items={response.sourceEvidence} previewLines={5} copiedEvidenceId={copiedEvidenceId} onOpen={setSelectedEvidence} onCopy={(evidence) => void copyEvidence(evidence)} /> : null}
      {response.durableMemory.length ? <ResultSection title="Durable memory context" items={response.durableMemory} /> : null}
      {response.weakContext.length ? <ResultSection title="Weak context" items={response.weakContext} weak /> : null}
      <Text className="askCopyStatus" role="status" aria-live="polite" size="sm" c="dimmed">{copyStatus}</Text>
    </Stack> : null}
    <Modal opened={Boolean(selectedEvidence)} onClose={() => setSelectedEvidence(null)} title="Source evidence detail" size="lg" closeButtonProps={{ 'aria-label': 'Close source evidence detail' }}>
      {selectedEvidence ? <Stack gap="md" className="evidenceDetail">
        <Badge variant="light" color="blue">Source evidence</Badge>
        {selectedEvidence.title ? <Title order={3}>{selectedEvidence.title}</Title> : null}
        <Text lh={1.7} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{selectedEvidence.content}</Text>
        <Group gap="md" c="dimmed">
          {selectedEvidence.provenance ? <Text size="sm">{selectedEvidence.provenance}</Text> : null}
          {typeof selectedEvidence.relevance === 'number' ? <Text size="sm">{Math.round(selectedEvidence.relevance * 100)}% relevance</Text> : null}
          {selectedEvidence.updatedAt ? <Text component="time" size="sm" dateTime={selectedEvidence.updatedAt}>{new Date(selectedEvidence.updatedAt).toLocaleString()}</Text> : null}
        </Group>
      </Stack> : null}
    </Modal>
  </div>
}

function ResultSection({ title, items, weak = false, previewLines, copiedEvidenceId, onOpen, onCopy }: {
  title: string
  items: AskResponse['durableMemory']
  weak?: boolean
  previewLines?: number
  copiedEvidenceId?: string
  onOpen?: (item: KnowledgeResult) => void
  onCopy?: (item: KnowledgeResult) => void
}) {
  return <Stack className={weak ? 'askResultSection isWeak' : 'askResultSection'} gap="sm"><Group gap="xs"><Title order={3}>{title}</Title><IconArrowRight size={18} /></Group><div className="knowledgeResultList">{items.map((item) => <KnowledgeResultCard key={`${item.kind}:${item.id}`} result={item} previewLines={previewLines} copied={copiedEvidenceId === item.id} onOpen={onOpen ? () => onOpen(item) : undefined} onCopy={onCopy ? () => onCopy(item) : undefined} />)}</div></Stack>
}
