import { useEffect, useRef, useState } from 'react'
import { Alert, Badge, Button, Group, Menu, Modal, NumberInput, Paper, Select, Stack, Switch, Text, Textarea, TextInput, Title } from '@mantine/core'
import { IconArrowRight, IconBooks, IconChevronDown, IconLanguage, IconMessageCircleQuestion, IconSearch, IconSettings, IconSparkles } from '@tabler/icons-react'
import type { AskResponse, KnowledgeGateway, KnowledgeResult, SourceSummary, TranslationResult, WorkspaceScope } from '../../lib/knowledgeGateway'
import { KnowledgeResultCard } from './KnowledgeResultCard'

const translationLanguages = [
  { value: 'en', label: 'English' }, { value: 'vi', label: 'Vietnamese' }, { value: 'zh', label: 'Chinese' },
  { value: 'ja', label: 'Japanese' }, { value: 'ko', label: 'Korean' }, { value: 'th', label: 'Thai' },
  { value: 'es', label: 'Spanish' }, { value: 'fr', label: 'French' }, { value: 'de', label: 'German' }, { value: 'pt', label: 'Portuguese' },
]

function initialTranslationLanguage() {
  const browserLanguage = typeof navigator === 'undefined' ? '' : navigator.language.toLowerCase().split('-')[0]
  return translationLanguages.some((language) => language.value === browserLanguage) ? browserLanguage : 'en'
}

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
  const [translatedAnswer, setTranslatedAnswer] = useState<TranslationResult | null>(null)
  const [showOriginal, setShowOriginal] = useState(false)
  const [translationBusy, setTranslationBusy] = useState(false)
  const [translationError, setTranslationError] = useState('')
  const [translationSuppressed, setTranslationSuppressed] = useState(false)
  const [translationSettingsOpened, setTranslationSettingsOpened] = useState(false)
  const [targetLanguage, setTargetLanguage] = useState(initialTranslationLanguage)
  const controllerRef = useRef<AbortController | null>(null)
  const translationControllerRef = useRef<AbortController | null>(null)
  const scope: WorkspaceScope = { workspaceId, sourceId }

  useEffect(() => {
    controllerRef.current?.abort()
    translationControllerRef.current?.abort()
    setResponse(null)
    setError('')
    setSourceId('')
    setSelectedEvidence(null)
    setCopiedEvidenceId('')
    setCopyStatus('')
    setTranslatedAnswer(null)
    setShowOriginal(false)
    setTranslationBusy(false)
    setTranslationError('')
    setTranslationSuppressed(false)
    const sourceController = new AbortController()
    gateway.listSources({ workspaceId }, sourceController.signal).then((items) => {
      if (!sourceController.signal.aborted) setSources(items)
    }).catch(() => {
      if (!sourceController.signal.aborted) setSources([])
    })
    return () => { sourceController.abort(); controllerRef.current?.abort(); translationControllerRef.current?.abort() }
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
    translationControllerRef.current?.abort()
    setTranslatedAnswer(null)
    setShowOriginal(false)
    setTranslationBusy(false)
    setTranslationError('')
    setTranslationSuppressed(false)
    try {
      const next = await gateway.ask(scope, question, controller.signal)
      if (!controller.signal.aborted) setResponse(next)
    } catch (reason) {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Ask could not complete.')
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  async function translateAnswer() {
    const original = response?.answer
    if (!original?.trim() || translationBusy) return
    translationControllerRef.current?.abort()
    const controller = new AbortController()
    translationControllerRef.current = controller
    setTranslationBusy(true)
    setTranslationError('')
    try {
      const result = await gateway.translateAnswer(scope, original, targetLanguage, controller.signal)
      if (!controller.signal.aborted) { setTranslatedAnswer(result); setShowOriginal(false) }
    } catch (reason) {
      if (!controller.signal.aborted) setTranslationError(reason instanceof Error ? reason.message : 'Local translation could not complete.')
    } finally {
      if (!controller.signal.aborted) setTranslationBusy(false)
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
  const canTranslate = Boolean(response?.answer?.trim() && !translationSuppressed && gateway.supports('translation', scope))
  const displayedAnswer = translatedAnswer && !showOriginal ? translatedAnswer.text : response?.answer

  return <div className="askWorkspace" data-has-evidence={hasEvidence || undefined} aria-busy={busy}>
    <Stack className="askPrimary" gap="xl">
      <Paper component="form" className="askComposer" withBorder p={{ base: 'md', sm: 'xl' }} radius="lg" onSubmit={(event) => { event.preventDefault(); void submit() }}>
        <Stack gap="md">
          <Group justify="space-between" align="flex-start"><div><Text c="memory" size="xs" fw={700} tt="uppercase">Grounded workspace conversation</Text><Title order={2}>Ask this workspace</Title></div><IconMessageCircleQuestion size={28} color="var(--mantine-color-memory-5)" /></Group>
          <Select aria-label="Narrow Ask to source" label="Knowledge scope" data={sourceOptions} value={sourceId} onChange={(value) => { translationControllerRef.current?.abort(); setSourceId(value || ''); setResponse(null); setSelectedEvidence(null); setCopiedEvidenceId(''); setCopyStatus(''); setTranslatedAnswer(null); setTranslationBusy(false); setTranslationError(''); setTranslationSuppressed(false) }} searchable />
          <Textarea id="workspace-question" label="Question" value={question} onChange={(event) => setQuestion(event.currentTarget.value)} placeholder="Ask about decisions, code, documents, or notes…" autosize minRows={4} maxRows={10} />
          <Group justify="flex-end"><Button type="submit" loading={busy} disabled={!question.trim()} leftSection={<IconSparkles size={17} />}>Ask</Button></Group>
        </Stack>
      </Paper>
      {error ? <Alert color="red" title="Ask failed" role="alert">{error}</Alert> : null}
      {response ? response.answerable ? response.answer?.trim() ? <Paper className="askAnswer" withBorder p={{ base: 'md', sm: 'xl' }} radius="lg" aria-live="polite"><Stack gap="md">
      <Group className="askAnswerToolbar" justify="space-between" align="center" wrap="wrap">
        <Group gap="xs">{translatedAnswer ? <Badge variant="light">{translatedAnswer.targetLanguage.toUpperCase()} · Local model {translatedAnswer.model}</Badge> : <Text size="sm" c="dimmed">Grounded answer</Text>}{translatedAnswer ? <Button variant="subtle" size="compact-sm" onClick={() => setShowOriginal((value) => !value)}>{showOriginal ? 'Show translation' : 'Show original'}</Button> : null}</Group>
        {canTranslate ? <Menu position="bottom-end" width={290} shadow="md" closeOnItemClick={false}>
          <Menu.Target><Button className="askTranslationControl" variant="subtle" size="compact-sm" loading={translationBusy} leftSection={<IconLanguage size={18} />} rightSection={<IconChevronDown size={15} />} aria-label="Translate answer">Translate</Button></Menu.Target>
          <Menu.Dropdown><Menu.Label>Translate with local model</Menu.Label><div className="askTranslationMenu"><Select aria-label="Target translation language" data={translationLanguages} value={targetLanguage} onChange={(value) => setTargetLanguage(value || 'en')} searchable /><Button fullWidth loading={translationBusy} leftSection={<IconLanguage size={16} />} onClick={() => void translateAnswer()}>Translate</Button></div><Menu.Divider /><Menu.Item onClick={() => setTranslationSuppressed(true)}>Don’t suggest translation for this answer</Menu.Item><Menu.Item leftSection={<IconSettings size={16} />} onClick={() => setTranslationSettingsOpened(true)}>Translation settings</Menu.Item></Menu.Dropdown>
        </Menu> : null}
      </Group>
      <Text lh={1.7} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{displayedAnswer}</Text>
      {translationError ? <Alert color="orange" title="Local translation unavailable" role="alert"><Stack gap="xs"><Text size="sm">{translationError}</Text><Button variant="light" size="compact-sm" onClick={() => setTranslationSettingsOpened(true)}>Open translation settings</Button></Stack></Alert> : null}
    </Stack></Paper> : null : <Alert color="yellow" title="No grounded answer" aria-live="polite"><Stack gap="md"><Text>{response.unavailableReason || 'The selected workspace does not have enough trusted context.'}</Text><Group><Button variant="light" leftSection={<IconSearch size={16} />} onClick={onOpenSearch}>Search memories</Button><Button variant="default" leftSection={<IconBooks size={16} />} onClick={onOpenSources}>Review Sources</Button></Group></Stack></Alert> : null}
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
    <TranslationSettingsModal gateway={gateway} opened={translationSettingsOpened} onClose={() => setTranslationSettingsOpened(false)} />
  </div>
}

function TranslationSettingsModal({ gateway, opened, onClose }: { gateway: KnowledgeGateway; opened: boolean; onClose: () => void }) {
  const [enabled, setEnabled] = useState(true)
  const [baseURL, setBaseURL] = useState('http://127.0.0.1:11434/v1')
  const [textModel, setTextModel] = useState('qwen3:4b')
  const [visionModel, setVisionModel] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState<number | string>(15)
  const [keyConfigured, setKeyConfigured] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!opened) return
    setBusy(true); setError(''); setStatus('')
    gateway.getTranslationStatus().then((result) => {
      if (result.configured) {
        setEnabled(result.config.enabled); setBaseURL(result.config.base_url); setTextModel(result.config.text_model); setVisionModel(result.config.vision_model || '')
        setTimeoutSeconds(result.config.timeout_seconds || 15); setKeyConfigured(result.config.api_key_configured)
      }
      setStatus(result.reachable && result.text_model_available ? `Ready · ${result.config.text_model}` : result.error || 'Setup required: configure and start a local OpenAI-compatible model server.')
    }).catch((reason) => setError(reason instanceof Error ? reason.message : 'Translation settings could not be loaded.')).finally(() => setBusy(false))
  }, [gateway, opened])

  const input = () => ({ enabled, base_url: baseURL.trim(), text_model: textModel.trim(), vision_model: visionModel || undefined, api_key: apiKey.trim() || undefined, timeout_seconds: Number(timeoutSeconds) || 15 })
  async function apply(action: 'test' | 'save') {
    setBusy(true); setError(''); setStatus('')
    try {
      const result = action === 'test' ? await gateway.testTranslationSettings(input()) : await gateway.saveTranslationSettings(input())
      setKeyConfigured(result.config.api_key_configured); setAPIKey('')
      setStatus(result.reachable && result.text_model_available ? `Ready · ${result.config.text_model}` : result.error || 'The local model is not ready. Start it and confirm the model name.')
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'Translation settings could not be updated.') } finally { setBusy(false) }
  }

  return <Modal opened={opened} onClose={onClose} title="Translation settings" size="md" closeButtonProps={{ 'aria-label': 'Close translation settings' }}><Stack gap="md">
    <Text size="sm" c="dimmed">Answers are sent only to this validated local endpoint. No cloud fallback is used.</Text>
    <Switch label="Enable local translation" checked={enabled} onChange={(event) => setEnabled(event.currentTarget.checked)} />
    <TextInput label="OpenAI-compatible local endpoint" value={baseURL} onChange={(event) => setBaseURL(event.currentTarget.value)} placeholder="http://127.0.0.1:11434/v1" />
    <TextInput label="Text model" value={textModel} onChange={(event) => setTextModel(event.currentTarget.value)} placeholder="qwen3:4b" />
    <TextInput label="API key (optional)" description={keyConfigured ? 'A key is stored. Leave blank to keep it.' : 'Stored write-only when provided.'} type="password" value={apiKey} onChange={(event) => setAPIKey(event.currentTarget.value)} />
    <NumberInput label="Timeout (seconds)" min={1} max={120} value={timeoutSeconds} onChange={setTimeoutSeconds} />
    {status ? <Alert color={status.startsWith('Ready') ? 'green' : 'yellow'} title="Local model status" aria-live="polite">{status}</Alert> : null}
    {error ? <Alert color="red" title="Settings failed" role="alert">{error}</Alert> : null}
    <Group justify="flex-end"><Button variant="default" disabled={busy || !baseURL.trim() || !textModel.trim()} onClick={() => void apply('test')}>Test connection</Button><Button loading={busy} disabled={!baseURL.trim() || !textModel.trim()} onClick={() => void apply('save')}>Save settings</Button></Group>
  </Stack></Modal>
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
