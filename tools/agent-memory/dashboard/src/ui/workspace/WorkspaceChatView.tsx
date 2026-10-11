import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ActionIcon, Alert, Badge, Button, Group, Modal, Paper, Stack, Text, Textarea } from '@mantine/core'
import { IconArrowUp, IconMessageCircle, IconSearch } from '@tabler/icons-react'
import type { AskResponse, KnowledgeGateway, KnowledgeResult, WorkspaceScope } from '../../lib/knowledgeGateway'
import { GraphContext } from './GraphContext'
import { KnowledgeResultCard } from './KnowledgeResultCard'
import { MAX_CHAT_TURNS, newWorkspaceWorkItemId, responseForStorage, titleFromPrompt, type ChatTurn, type ChatWorkItem } from './workspaceRecords'

export function WorkspaceChatView({ gateway, workspaceId, item, onUpdate, onCreateSearch }: {
  gateway: KnowledgeGateway
  workspaceId: string
  item: ChatWorkItem
  onUpdate: (next: ChatWorkItem) => void
  onCreateSearch: (query?: string) => void
}) {
  const [question, setQuestion] = useState('')
  const [busy, setBusy] = useState(false)
  const [errorsByTurn, setErrorsByTurn] = useState<Record<string, string>>({})
  const [selectedEvidence, setSelectedEvidence] = useState<KnowledgeResult | null>(null)
  const [copiedEvidence, setCopiedEvidence] = useState('')
  const [copyStatus, setCopyStatus] = useState('')
  const requestRef = useRef<AbortController | null>(null)
  const latestTurnRef = useRef<HTMLDivElement | null>(null)
  const scope: WorkspaceScope = { workspaceId }
  const canAddTurn = item.turns.length < MAX_CHAT_TURNS

  useEffect(() => () => requestRef.current?.abort(), [])

  useEffect(() => {
    const interrupted = item.turns.find((turn) => !turn.response && !turn.failed)
    if (interrupted) updateTurn({ ...interrupted, failed: true })
  }, [item.id])

  useEffect(() => { latestTurnRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }) }, [item.turns.length])

  function updateTurn(turn: ChatTurn) {
    const turns = item.turns.some((candidate) => candidate.id === turn.id)
      ? item.turns.map((candidate) => candidate.id === turn.id ? turn : candidate)
      : [...item.turns, turn]
    let title = item.title
    if (!item.titleEdited && item.title === 'New chat') title = titleFromPrompt(turn.question, item.title)
    onUpdate({ ...item, title, turns: turns.slice(-MAX_CHAT_TURNS), updatedAt: new Date().toISOString() })
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    const normalized = question.trim()
    if (!normalized || busy || !canAddTurn) return
    requestRef.current?.abort()
    const controller = new AbortController()
    requestRef.current = controller
    const turn: ChatTurn = { id: newWorkspaceWorkItemId(), question: normalized.slice(0, 2000), createdAt: new Date().toISOString() }
    setQuestion('')
    setBusy(true)
    updateTurn(turn)
    try {
      const response = await gateway.ask(scope, turn.question, { mode: 'auto' }, controller.signal)
      if (!controller.signal.aborted) updateTurn({ ...turn, response: responseForStorage(response, workspaceId) })
    } catch (reason) {
      if (!controller.signal.aborted) {
        updateTurn({ ...turn, failed: true })
        const message = reason instanceof Error && reason.message ? reason.message : 'This query could not complete.'
        setErrorsByTurn((current) => ({ ...current, [turn.id]: message.slice(0, 500) }))
      }
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  async function copyEvidence(result: KnowledgeResult) {
    try {
      const operation = navigator.clipboard?.writeText(result.content)
      if (!operation) throw new Error('Clipboard access is unavailable.')
      await operation
      setCopiedEvidence(result.id)
      setCopyStatus('Evidence copied.')
    } catch {
      setCopiedEvidence('')
      setCopyStatus('Evidence could not be copied.')
    }
  }

  return <Stack className="workspaceChatView" gap="md">
    <Stack className="workspaceChatTranscript" gap="lg" aria-live="polite">
      {item.turns.length ? item.turns.map((turn, index) => <div className="workspaceChatTurn" key={turn.id} ref={index === item.turns.length - 1 ? latestTurnRef : undefined}>
        <Group className="workspaceChatQuestionRow" justify="flex-end" align="flex-start" wrap="nowrap">
          <Paper className="workspaceChatQuestion" p="md" radius="lg"><Text lh={1.6} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{turn.question}</Text></Paper>
          <Badge variant="light" size="sm">You</Badge>
        </Group>
        {turn.response ? <StoredAnswer gateway={gateway} workspaceId={workspaceId} response={turn.response} onOpen={setSelectedEvidence} onCopy={(result) => void copyEvidence(result)} copiedId={copiedEvidence} onSearch={() => onCreateSearch(turn.question)} /> : null}
        {turn.failed ? <Alert className="workspaceChatFailure" color="red" title="Query failed"><Text size="sm">{errorsByTurn[turn.id] || 'Run this question again or start a new chat.'}</Text><Button size="xs" variant="light" mt="xs" onClick={() => setQuestion(turn.question)}>Ask again</Button></Alert> : null}
        {!turn.response && !turn.failed && busy ? <Group className="workspaceChatPending" gap="xs" aria-live="polite"><span className="workspaceChatTyping" aria-hidden="true" /><Text size="sm" c="dimmed">Searching…</Text></Group> : null}
      </div>) : <Paper className="workspaceChatWelcome" withBorder p={{ base: 'lg', sm: 'xl' }} radius="lg"><Stack align="center" gap="sm"><IconMessageCircle size={30} /><Text fw={650}>Ask a question</Text><Text c="dimmed" ta="center" size="sm">Retrieved source evidence and durable memories stay clearly labeled.</Text></Stack></Paper>}
      {!canAddTurn ? <Alert color="gray" title="Chat is full">This chat stores up to {MAX_CHAT_TURNS} questions. Start a new chat to continue.</Alert> : null}
    </Stack>
    <Paper className="workspaceChatComposer" component="form" withBorder p="sm" radius="lg" onSubmit={(event) => void submit(event)}>
      <Stack gap="xs">
        <Textarea aria-label="Message this workspace" value={question} onChange={(event) => setQuestion(event.currentTarget.value.slice(0, 2000))} placeholder="Type your message…" autosize minRows={2} maxRows={8} maxLength={2000} disabled={!canAddTurn} />
        <Group justify="space-between"><Text size="xs" c="dimmed">Chats are stored in this browser for this workspace.</Text><ActionIcon type="submit" aria-label="Send message" loading={busy} disabled={!question.trim() || !canAddTurn} variant="filled" color="memory" size="lg" radius="xl"><IconArrowUp size={18} /></ActionIcon></Group>
      </Stack>
    </Paper>
    <Modal opened={Boolean(selectedEvidence)} onClose={() => setSelectedEvidence(null)} title="Evidence detail" size="lg" closeButtonProps={{ 'aria-label': 'Close evidence detail' }}>
      {selectedEvidence ? <Stack gap="md"><Badge variant="light" color={selectedEvidence.kind === 'source-evidence' ? 'blue' : 'memory'}>{selectedEvidence.kind === 'source-evidence' ? 'Source evidence' : selectedEvidence.memoryType || 'Memory'}</Badge>{selectedEvidence.title ? <Text fw={700}>{selectedEvidence.title}</Text> : null}<Text lh={1.7} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{selectedEvidence.content}</Text><Text size="sm" c="dimmed">{selectedEvidence.provenance || 'Workspace retrieval'}</Text></Stack> : null}
    </Modal>
    <Text className="workspaceChatCopyStatus" role="status" aria-live="polite" size="xs" c="dimmed">{copyStatus}</Text>
  </Stack>
}

function StoredAnswer({ gateway, workspaceId, response, onOpen, onCopy, copiedId, onSearch }: {
  gateway: KnowledgeGateway
  workspaceId: string
  response: Pick<AskResponse, 'requestId' | 'answerable' | 'answer' | 'sourceEvidence' | 'durableMemory' | 'weakContext' | 'unavailableReason' | 'graphRoute' | 'graphContext'>
  onOpen: (item: KnowledgeResult) => void
  onCopy: (item: KnowledgeResult) => void
  copiedId: string
  onSearch: () => void
}) {
  return <Stack className="workspaceChatAnswer" gap="sm">
    {response.answerable ? response.answer?.trim() ? <Paper className="workspaceChatAnswerBody" withBorder p="md" radius="lg"><Text lh={1.7} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{response.answer}</Text></Paper> : null : <Alert color="yellow" title="No grounded answer">{response.unavailableReason || 'There is not enough trusted context in this workspace.'}<Button size="xs" variant="light" leftSection={<IconSearch size={14} />} mt="xs" onClick={onSearch}>Search these words in memories</Button></Alert>}
    {response.sourceEvidence.length ? <AnswerSection title="Source evidence" items={response.sourceEvidence} color="blue" onOpen={onOpen} onCopy={onCopy} copiedId={copiedId} /> : null}
    {response.durableMemory.length ? <AnswerSection title="Durable memory" items={response.durableMemory} color="memory" onOpen={onOpen} /> : null}
    {response.weakContext.length ? <AnswerSection title="Weak context" items={response.weakContext} color="yellow" onOpen={onOpen} /> : null}
    {response.graphRoute ? <GraphContext gateway={gateway} workspaceId={workspaceId} response={response} feedbackEnabled={gateway.supports('graph', { workspaceId })} /> : null}
  </Stack>
}

function AnswerSection({ title, items, color, onOpen, onCopy, copiedId }: {
  title: string
  items: KnowledgeResult[]
  color: string
  onOpen: (item: KnowledgeResult) => void
  onCopy?: (item: KnowledgeResult) => void
  copiedId?: string
}) {
  return <Stack className="workspaceChatEvidenceSection" gap="xs"><Text size="xs" tt="uppercase" fw={700} c={color}>{title}</Text>{items.map((item) => <KnowledgeResultCard key={`${item.workspaceId}:${item.id}`} result={item} previewLines={4} onOpen={() => onOpen(item)} onCopy={onCopy ? () => onCopy(item) : undefined} copied={copiedId === item.id} />)}</Stack>
}
