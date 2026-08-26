import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { Alert, Badge, Button, Card, FileInput, Grid, Group, NavLink, Paper, PasswordInput, Stack, Tabs, Text, Title } from '@mantine/core'
import { IconActivity, IconAdjustments, IconDatabase, IconDownload, IconGauge, IconKey, IconShare, IconServer, IconSettings, IconShieldLock, IconStethoscope, IconUsers, IconWand } from '@tabler/icons-react'
import type { KnowledgeCapability, KnowledgeGateway } from '../../lib/knowledgeGateway'
import { getStats, listBenchmarkRuns, type BenchmarkRun, type DashboardStats, type SchedulerRunHistory, type SchedulerSummary, type SkillInfo } from '../../lib/api'
import { BenchmarkPanel } from '../BenchmarkPanel'
import { ClientsPanel } from '../ClientsPanel'
import { DeploymentPanel } from '../DeploymentPanel'
import { DiagnosticsPanel } from '../DiagnosticsPanel'
import { LifecyclePanel } from '../LifecyclePanel'
import { MigrationPanel } from '../MigrationPanel'
import { SkillsPanel } from '../SkillsPanel'
import { GraphSettings } from './GraphSettings'

type SettingsSection = 'account' | 'data' | 'access' | 'system'
type SystemTool = { id: string; label: string; description: string; runtimes: Array<KnowledgeGateway['runtime']>; capability?: KnowledgeCapability; icon: typeof IconSettings }

export const systemTools: SystemTool[] = [
  { id: 'diagnostics', label: 'Diagnostics', description: 'Workspace health, storage, and retrieval signals.', runtimes: ['standalone', 'hosted'], icon: IconStethoscope },
  { id: 'graph', label: 'Graph index', description: 'Derived-index readiness, processing, provenance, and review.', runtimes: ['standalone', 'hosted'], capability: 'graph', icon: IconShare },
  { id: 'lifecycle', label: 'Lifecycle', description: 'Decay, promotion, and scheduled maintenance history.', runtimes: ['standalone'], capability: 'lifecycle', icon: IconActivity },
  { id: 'benchmark', label: 'Benchmark', description: 'Retrieval quality and economic comparison runs.', runtimes: ['standalone'], icon: IconGauge },
  { id: 'clients', label: 'Clients', description: 'Agent client profiles and tool exposure.', runtimes: ['standalone'], capability: 'clients', icon: IconUsers },
  { id: 'skills', label: 'Skills', description: 'Distilled workspace workflows and instructions.', runtimes: ['standalone'], capability: 'skills', icon: IconWand },
  { id: 'infrastructure', label: 'Infrastructure', description: 'Deployment plan, budget, and runtime status.', runtimes: ['standalone', 'hosted'], icon: IconServer },
  { id: 'migration', label: 'Migration', description: 'Import and migration readiness controls.', runtimes: ['standalone'], icon: IconDownload },
]

function HostedMigrationImport({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [file, setFile] = useState<File | null>(null)
  const [passphrase, setPassphrase] = useState('')
  const [status, setStatus] = useState('')
  const idempotencyKey = useRef(crypto.randomUUID())
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!file) return
    try {
      const result = await gateway.importMigration({ workspaceId }, file, passphrase, idempotencyKey.current)
      setStatus(`Imported ${result.imported}; merged ${result.merged}; skipped ${result.skipped}; failed ${result.failed}.`)
    } catch (cause) { setStatus(cause instanceof Error ? cause.message : 'Migration import failed.') }
  }
  return <Paper withBorder p="lg" radius="lg"><Stack component="form" onSubmit={(event) => void submit(event)}><Title order={3}>Import standalone migration</Title><Text c="dimmed">The passphrase stays in memory and the same idempotency key is reused for a safe retry.</Text><FileInput label="AMPB2 bundle" accept=".ampb2" value={file} onChange={setFile} required /><PasswordInput label="Bundle passphrase" required value={passphrase} onChange={(event) => setPassphrase(event.currentTarget.value)} /><Button type="submit" disabled={!file || !passphrase}>Import copy</Button><Text role="status" size="sm">{status}</Text></Stack></Paper>
}

function SystemToolPanel({ id, workspaceId, gateway }: { id: string; workspaceId: string; gateway: KnowledgeGateway }) {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [scheduler, setScheduler] = useState<SchedulerSummary | undefined>()
  const [history, setHistory] = useState<SchedulerRunHistory[]>([])
  const [runs, setRuns] = useState<BenchmarkRun[]>([])
  const [skills, setSkills] = useState<SkillInfo[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [rawOpen, setRawOpen] = useState(false)

  useEffect(() => {
    let current = true
    setBusy(true)
    setError('')
    const request = id === 'lifecycle' ? gateway.listLifecycle({ workspaceId }).then((result) => { setScheduler(result.scheduler); setHistory(result.history || []) })
      : id === 'benchmark' ? listBenchmarkRuns({ workspace: workspaceId, limit: 100 }).then((result) => setRuns(result.runs || []))
        : id === 'skills' ? gateway.listSkills({ workspaceId }).then(setSkills)
          : id === 'diagnostics' ? getStats(workspaceId).then(setStats)
            : Promise.resolve()
    request.catch((cause) => { if (current) setError(cause instanceof Error ? cause.message : String(cause)) }).finally(() => { if (current) setBusy(false) })
    return () => { current = false }
  }, [gateway, id, workspaceId])

  if (id === 'diagnostics') return <>{rawOpen ? <Paper withBorder p="md"><Button variant="default" onClick={() => setRawOpen(false)}>Close raw payload</Button><pre>{JSON.stringify(stats, null, 2)}</pre></Paper> : <DiagnosticsPanel workspaceLabel={workspaceId} stats={stats} statsErr={error} healthState={{ tone: error ? 'bad' : stats ? 'good' : 'warn', label: error ? 'Unavailable' : stats ? 'Healthy' : 'Loading', detail: error || 'Workspace diagnostics' }} onOpenRaw={() => setRawOpen(true)} />}</>
  if (id === 'graph') return <GraphSettings gateway={gateway} workspaceId={workspaceId} />
  if (id === 'lifecycle') return <LifecyclePanel workspace={workspaceId} scheduler={scheduler} history={history} busy={busy} error={error} />
  if (id === 'benchmark') return <BenchmarkPanel workspace={workspaceId} runs={runs} busy={busy} error={error} />
  if (id === 'clients') return <ClientsPanel clientProfiles={gateway} />
  if (id === 'skills') return <SkillsPanel theme="dark" workspace={workspaceId} skills={skills} busy={busy} error={error} />
  if (id === 'infrastructure') return <DeploymentPanel />
  if (id === 'migration') return <MigrationPanel workspace={workspaceId} />
  return null
}

export function SettingsView({ gateway, workspaceId }: { gateway: KnowledgeGateway; workspaceId: string }) {
  const [section, setSection] = useState<SettingsSection>('account')
  const [selectedTool, setSelectedTool] = useState('diagnostics')
  const [settings, setSettings] = useState<Record<string, unknown>>({})
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    gateway.getSettings({ workspaceId }, controller.signal).then(setSettings).catch((cause) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Settings could not be loaded.') })
    return () => controller.abort()
  }, [gateway, workspaceId])

  const tool = useMemo(() => systemTools.find((item) => item.id === selectedTool) || systemTools[0], [selectedTool])
  const available = tool.capability ? gateway.supports(tool.capability, { workspaceId }) : tool.runtimes.includes(gateway.runtime)
  const settingsText = JSON.stringify(settings, null, 2)

  return <Stack className="settingsView" gap="md" aria-label="Workspace settings">
    <Tabs value={section} onChange={(value) => value && setSection(value as SettingsSection)}>
      <Tabs.List>{(['account', 'data', 'access', 'system'] as SettingsSection[]).map((item) => <Tabs.Tab key={item} value={item} leftSection={item === 'account' ? <IconUsers size={15} /> : item === 'data' ? <IconDatabase size={15} /> : item === 'access' ? <IconShieldLock size={15} /> : <IconSettings size={15} />}>{item[0].toUpperCase() + item.slice(1)}</Tabs.Tab>)}</Tabs.List>
    </Tabs>
    {error ? <Alert color="red" title="Settings unavailable" role="alert">{error}</Alert> : null}
    {section === 'account' ? <Card withBorder radius="lg" padding="lg"><Stack><Title order={2}>Account</Title><Text c="dimmed">{gateway.runtime === 'hosted' ? 'Hosted account and billing identity for this workspace.' : 'This private workspace is controlled by the local operating-system owner.'}</Text><Group><Badge variant="light">Runtime: {gateway.runtime}</Badge><Badge variant="outline">Workspace: {workspaceId}</Badge></Group></Stack></Card> : null}
    {section === 'data' ? <Card withBorder radius="lg" padding="lg"><Stack><Title order={2}>Data and privacy</Title><Text c="dimmed">Inspect retention, privacy, storage, and billing-related configuration returned for this workspace.</Text><Paper bg="dark.8" p="md" radius="md"><pre>{settingsText || '{}'}</pre></Paper>{gateway.runtime === 'hosted' ? <HostedMigrationImport gateway={gateway} workspaceId={workspaceId} /> : null}</Stack></Card> : null}
    {section === 'access' ? <Card withBorder radius="lg" padding="lg"><Stack><Title order={2}>Access</Title><Text c="dimmed">Workspace scope is enforced by the active connection. File-system paths are never accepted as workspace selectors.</Text><Group><Badge variant="light" leftSection={<IconKey size={13} />}>Scoped to {workspaceId}</Badge><Badge variant="outline">{[...gateway.capabilities].length} knowledge capabilities</Badge></Group><Text size="sm">{[...gateway.capabilities].join(', ')}</Text></Stack></Card> : null}
    {section === 'system' ? <Grid className="settingsSystemGrid" gutter="md"><Grid.Col span={{ base: 12, md: 4, lg: 3, xl: 2 }}><Paper withBorder p="xs" radius="lg" aria-label="System tools"><Stack gap={4}>{systemTools.map((item) => { const ToolIcon = item.icon; return <NavLink key={item.id} label={item.label} description={item.description} leftSection={<ToolIcon size={17} />} active={selectedTool === item.id} aria-current={selectedTool === item.id ? 'page' : undefined} onClick={() => setSelectedTool(item.id)} /> })}</Stack></Paper></Grid.Col><Grid.Col className="settingsSystemContent" span={{ base: 12, md: 8, lg: 9, xl: 10 }}><Paper withBorder p={{ base: 'md', md: 'lg' }} radius="lg"><Stack><Text c="memory" size="xs" fw={700} tt="uppercase">Advanced system tool</Text><Group justify="space-between"><div><Title order={2}>{tool.label}</Title><Text c="dimmed">{tool.description}</Text></div><Badge variant="light" color={available ? 'memory' : 'gray'}>{available ? `Available in ${gateway.runtime}` : `Unavailable in ${gateway.runtime}`}</Badge></Group>{available ? gateway.runtime === 'standalone' || tool.capability ? <SystemToolPanel id={tool.id} workspaceId={workspaceId} gateway={gateway} /> : <Paper bg="dark.8" p="md"><pre>{settingsText || '{}'}</pre></Paper> : <Alert color="gray" title={`Unavailable in ${gateway.runtime}`}>This tool is unavailable for the current runtime or workspace. The primary workspace navigation stays unchanged.</Alert>}</Stack></Paper></Grid.Col></Grid> : null}
  </Stack>
}
