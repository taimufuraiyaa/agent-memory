import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Group, Paper, Select, Stack, Text, Textarea, Title } from '@mantine/core'
import { IconArrowRight, IconBooks, IconMessageCircleQuestion, IconSearch, IconSparkles } from '@tabler/icons-react'
import type { AskResponse, KnowledgeGateway, SourceSummary, WorkspaceScope } from '../../lib/knowledgeGateway'
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
  const controllerRef = useRef<AbortController | null>(null)
  const scope: WorkspaceScope = { workspaceId, sourceId }

  useEffect(() => {
    controllerRef.current?.abort()
    setResponse(null)
    setError('')
    setSourceId('')
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
    try {
      const next = await gateway.ask(scope, question, controller.signal)
      if (!controller.signal.aborted) setResponse(next)
    } catch (reason) {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Ask could not complete.')
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  const sourceOptions = [{ value: '', label: 'All workspace sources' }, ...sources.map((source) => ({ value: source.id, label: `${source.title} · ${source.kind}` }))]

  return <Stack className="askWorkspace" gap="xl" aria-busy={busy}>
    <Paper component="form" className="askComposer" withBorder p={{ base: 'md', sm: 'xl' }} radius="lg" onSubmit={(event) => { event.preventDefault(); void submit() }}>
      <Stack gap="md">
        <Group justify="space-between" align="flex-start"><div><Text c="memory" size="xs" fw={700} tt="uppercase">Grounded workspace conversation</Text><Title order={2}>Ask this workspace</Title></div><IconMessageCircleQuestion size={28} color="var(--mantine-color-memory-5)" /></Group>
        <Select aria-label="Narrow Ask to source" label="Knowledge scope" data={sourceOptions} value={sourceId} onChange={(value) => { setSourceId(value || ''); setResponse(null) }} searchable />
        <Textarea id="workspace-question" label="Question" value={question} onChange={(event) => setQuestion(event.currentTarget.value)} placeholder="Ask about decisions, code, documents, or notes…" autosize minRows={4} maxRows={10} />
        <Group justify="flex-end"><Button type="submit" loading={busy} disabled={!question.trim()} leftSection={<IconSparkles size={17} />}>Ask</Button></Group>
      </Stack>
    </Paper>
    {error ? <Alert color="red" title="Ask failed" role="alert">{error}</Alert> : null}
    {response ? <Stack className="askResponse" gap="xl" aria-live="polite">
      {response.answerable ? <Paper className="askAnswer" withBorder p={{ base: 'md', sm: 'xl' }} radius="lg"><Text c="memory" size="xs" fw={700} tt="uppercase">Grounded answer</Text><Text mt="sm" lh={1.7} style={{ whiteSpace: 'pre-wrap' }}>{response.answer}</Text></Paper> : <Alert color="yellow" title="No grounded answer"><Stack gap="md"><Text>{response.unavailableReason || 'The selected workspace does not have enough trusted context.'}</Text><Group><Button variant="light" leftSection={<IconSearch size={16} />} onClick={onOpenSearch}>Search memories</Button><Button variant="default" leftSection={<IconBooks size={16} />} onClick={onOpenSources}>Review Sources</Button></Group></Stack></Alert>}
      {response.sourceEvidence.length ? <ResultSection title="Source evidence" items={response.sourceEvidence} /> : null}
      {response.durableMemory.length ? <ResultSection title="Durable memory context" items={response.durableMemory} /> : null}
      {response.weakContext.length ? <ResultSection title="Weak context" items={response.weakContext} weak /> : null}
    </Stack> : null}
  </Stack>
}

function ResultSection({ title, items, weak = false }: { title: string; items: AskResponse['durableMemory']; weak?: boolean }) {
  return <Stack className={weak ? 'askResultSection isWeak' : 'askResultSection'} gap="sm"><Group gap="xs"><Title order={3}>{title}</Title><IconArrowRight size={18} /></Group><div className="knowledgeResultList">{items.map((item) => <KnowledgeResultCard key={`${item.kind}:${item.id}`} result={item} />)}</div></Stack>
}
