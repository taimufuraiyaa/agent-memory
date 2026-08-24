import { useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  AppShell,
  Badge,
  Box,
  Burger,
  Button,
  Drawer,
  Group,
  Loader,
  NavLink,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Title,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import {
  IconActivity,
  IconBook2,
  IconBrain,
  IconHome2,
  IconMessageCircleQuestion,
  IconPlus,
  IconSearch,
  IconSettings,
  IconMoon,
  IconSun,
  IconUserCircle,
  type Icon,
} from '@tabler/icons-react'
import type { DashboardRuntime } from '../lib/runtime'
import type { KnowledgeGateway, WorkspaceSummary } from '../lib/knowledgeGateway'
import {
  pushWorkspaceRoute,
  readWorkspaceRoute,
  replaceWorkspaceRoute,
  type WorkspaceDestination,
  type WorkspaceRoute,
} from './workspace/workspaceRoute'
import './workspace/workspace.css'
import { AskView } from './workspace/AskView'
import { MemoryExplorer } from './workspace/MemoryExplorer'
import { SourcesView } from './workspace/SourcesView'
import { SourceImportDialog } from './workspace/SourceImportDialog'
import { NotesView } from './workspace/NotesView'
import { ActivityView } from './workspace/ActivityView'
import { SettingsView } from './workspace/SettingsView'
import { HomeView } from './workspace/HomeView'
import { HowHistoryView } from './workspace/HowHistoryView'

const primaryDestinations: Array<{ id: WorkspaceDestination; label: string; description: string; icon: Icon }> = [
  { id: 'home', label: 'Home', description: 'Workspace overview', icon: IconHome2 },
  { id: 'ask', label: 'Ask', description: 'Grounded answers', icon: IconMessageCircleQuestion },
  { id: 'knowledge', label: 'Knowledge', description: 'Sources and memories', icon: IconBook2 },
  { id: 'activity', label: 'Activity', description: 'Processing and feedback', icon: IconActivity },
  { id: 'settings', label: 'Settings', description: 'Data, access, and system', icon: IconSettings },
]

export type DashboardColorScheme = 'dark' | 'light'

export function WorkspaceApp({ runtime, gateway, colorScheme, onColorSchemeChange }: { runtime: DashboardRuntime; gateway: KnowledgeGateway; colorScheme: DashboardColorScheme; onColorSchemeChange: (value: DashboardColorScheme) => void }) {
  const initial = useMemo(() => readWorkspaceRoute(), [])
  const [workspaces, setWorkspaces] = useState<WorkspaceSummary[]>([])
  const [workspaceId, setWorkspaceId] = useState(initial.workspaceId || '')
  const [destination, setDestination] = useState<WorkspaceDestination>(initial.destination || 'home')
  const [knowledgeView, setKnowledgeView] = useState<WorkspaceRoute['knowledgeView']>(initial.knowledgeView || 'sources')
  const [error, setError] = useState('')
  const [importOpen, setImportOpen] = useState(false)
  const [importedSource, setImportedSource] = useState<import('../lib/knowledgeGateway').SourceSummary | null>(null)
  const [memoryInitialView, setMemoryInitialView] = useState<'search' | 'browse'>('search')
  const [mobileNavigationOpen, mobileNavigation] = useDisclosure(false)

  useEffect(() => {
    const controller = new AbortController()
    gateway.listWorkspaces(controller.signal).then((items) => {
      setWorkspaces(items)
      const routeWorkspace = items.find((workspace) => workspace.id === workspaceId)
      const nextWorkspace = routeWorkspace?.id || items[0]?.id || ''
      setWorkspaceId(nextWorkspace)
      if (nextWorkspace) replaceWorkspaceRoute({ workspaceId: nextWorkspace, destination, knowledgeView })
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Workspaces could not be loaded.')
    })
    return () => controller.abort()
  }, [gateway])

  useEffect(() => {
    const onPopState = () => {
      const route = readWorkspaceRoute()
      if (route.workspaceId) setWorkspaceId(route.workspaceId)
      if (route.destination) setDestination(route.destination)
      if (route.knowledgeView) setKnowledgeView(route.knowledgeView)
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  function navigate(nextDestination: WorkspaceDestination, nextKnowledgeView = knowledgeView) {
    setDestination(nextDestination)
    setKnowledgeView(nextKnowledgeView)
    if (workspaceId) pushWorkspaceRoute({ workspaceId, destination: nextDestination, knowledgeView: nextKnowledgeView })
    mobileNavigation.close()
  }

  function changeWorkspace(nextWorkspaceId: string) {
    setWorkspaceId(nextWorkspaceId)
    setError('')
    pushWorkspaceRoute({ workspaceId: nextWorkspaceId, destination, knowledgeView })
  }

  const workspace = workspaces.find((item) => item.id === workspaceId)
  const workspaceReady = Boolean(workspace)
  const activeDestination = primaryDestinations.find((item) => item.id === destination)
  const workspaceOptions = workspaces.map((item) => ({ value: item.id, label: `${item.name} · ${item.memoryCount} memories` }))

  const navigation = <Stack gap={4} className="workspaceNavigation">
    {primaryDestinations.map((item) => {
      const DestinationIcon = item.icon
      return <NavLink
        key={item.id}
        label={item.label}
        description={item.description}
        leftSection={<DestinationIcon size={19} stroke={1.8} />}
        active={destination === item.id}
        aria-current={destination === item.id ? 'page' : undefined}
        onClick={() => navigate(item.id)}
      />
    })}
  </Stack>

  return <AppShell
    className="workspaceApp"
    data-runtime={runtime.mode}
    header={{ height: { base: 132, sm: 72 } }}
    navbar={{ width: 248, breakpoint: 'sm', collapsed: { mobile: true } }}
    padding={0}
    transitionDuration={160}
  >
    <AppShell.Header className="workspaceHeader">
      <Group className="workspaceHeaderRow" wrap="nowrap">
        <Burger opened={mobileNavigationOpen} onClick={mobileNavigation.toggle} hiddenFrom="sm" size="sm" aria-label="Open primary navigation" />
        <Group className="workspaceBrand" gap="sm" wrap="nowrap">
          <ThemeIcon size={36} radius="md" variant="gradient" gradient={{ from: 'memory.5', to: 'memory.8', deg: 145 }}>
            <IconBrain size={22} stroke={1.8} />
          </ThemeIcon>
          <Box visibleFrom="md"><Text fw={750} lh={1.1}>Agent Memory</Text><Text size="xs" c="dimmed">Trusted knowledge</Text></Box>
        </Group>
        <Select
          className="workspacePicker"
          data-workspace-picker
          aria-label="Workspace"
          value={workspaceId || null}
          data={workspaceOptions}
          searchable
          nothingFoundMessage="No workspace found"
          onChange={(value) => { if (value) changeWorkspace(value) }}
        />
        <TextInput className="workspaceGlobalSearch" type="search" aria-label="Global workspace search" placeholder="Search this workspace…" leftSection={<IconSearch size={17} />} />
        {gateway.capabilities.has('source') ? <Button className="workspaceAddSource" aria-label="Add source" leftSection={<IconPlus size={17} />} disabled={!workspaceReady} onClick={() => { navigate('knowledge', 'sources'); setImportOpen(true) }}>Add source</Button> : null}
        <ActionIcon
          className="workspaceThemeToggle"
          variant="subtle"
          color="gray"
          size="lg"
          aria-label={`Switch to ${colorScheme === 'dark' ? 'light' : 'dark'} theme`}
          onClick={() => onColorSchemeChange(colorScheme === 'dark' ? 'light' : 'dark')}
        >
          {colorScheme === 'dark' ? <IconSun size={21} /> : <IconMoon size={21} />}
        </ActionIcon>
        <ActionIcon className="workspaceAccount" variant="subtle" color="gray" size="lg" aria-label={runtime.mode === 'hosted' ? 'Account' : 'Local owner'}>
          <IconUserCircle size={23} />
        </ActionIcon>
      </Group>
      <Group className="workspaceMobileScope" hiddenFrom="sm" gap="xs" wrap="nowrap">
        <Badge variant="light" color="memory" size="sm">{runtime.mode === 'hosted' ? 'Hosted' : 'Local'}</Badge>
        <Text size="xs" c="dimmed" truncate>{workspace?.name || 'Select a workspace'}</Text>
      </Group>
    </AppShell.Header>
    <AppShell.Navbar className="workspaceRail" p="md" aria-label="Primary navigation">
      <AppShell.Section grow>{navigation}</AppShell.Section>
      <AppShell.Section><Text size="xs" c="dimmed" px="sm" pb="xs">{runtime.mode === 'hosted' ? 'Hosted workspace' : 'Private local workspace'}</Text></AppShell.Section>
    </AppShell.Navbar>
    <Drawer opened={mobileNavigationOpen} onClose={mobileNavigation.close} title="Agent Memory" size="xs" hiddenFrom="sm" className="workspaceMobileDrawer">
      {navigation}
    </Drawer>
    <AppShell.Main className="workspaceMain" id="workspace-main">
      <Box className="workspaceCanvas">
        <Group className="workspacePageHeader" justify="space-between" align="flex-start">
          <Box>
            <Text className="workspaceEyebrow" size="xs" fw={700} tt="uppercase">{activeDestination?.description}</Text>
            <Title order={1}>{activeDestination?.label}</Title>
            <Text c="dimmed" size="sm">{workspace?.name || 'Select a workspace'} · one trusted knowledge scope</Text>
          </Box>
          {destination === 'knowledge' ? <SegmentedControl
            aria-label="Knowledge views"
            value={knowledgeView}
            onChange={(value) => navigate('knowledge', value as WorkspaceRoute['knowledgeView'])}
            data={[
              { value: 'sources', label: 'Sources' },
              { value: 'memories', label: 'Memories' },
              { value: 'history', label: 'How History' },
              { value: 'notes', label: 'Notes' },
            ]}
          /> : null}
        </Group>
        {error ? <Alert className="workspaceError" color="red" title="Workspace unavailable" role="alert">{error}</Alert> : null}
        {!error && workspaceId && !workspaceReady ? <Paper withBorder p="xl" radius="lg"><Group justify="center"><Loader size="sm" /><Text c="dimmed">Loading workspace…</Text></Group></Paper> : null}
        {destination === 'home' && workspace ? <HomeView workspace={workspace} onAddSource={() => { navigate('knowledge', 'sources'); setImportOpen(true) }} onNavigate={(target) => { if (target === 'ask') navigate('ask'); else if (target === 'sources') navigate('knowledge', 'sources'); else if (target === 'activity') navigate('activity'); else { setMemoryInitialView(target); navigate('knowledge', 'memories') } }} /> : null}
        {destination === 'ask' && workspaceReady ? <AskView gateway={gateway} workspaceId={workspaceId} onOpenSearch={() => navigate('knowledge', 'memories')} onOpenSources={() => navigate('knowledge', 'sources')} /> : null}
        {destination === 'knowledge' && knowledgeView === 'memories' && workspaceReady ? <MemoryExplorer gateway={gateway} workspaceId={workspaceId} initialView={memoryInitialView} /> : null}
        {destination === 'knowledge' && knowledgeView === 'history' && workspaceReady ? <HowHistoryView gateway={gateway} workspaceId={workspaceId} /> : null}
        {destination === 'knowledge' && knowledgeView === 'sources' && workspaceReady ? <SourcesView gateway={gateway} workspaceId={workspaceId} importedSource={importedSource} onNavigate={(target) => { if (target === 'ask') navigate('ask'); else { setMemoryInitialView(target); navigate('knowledge', 'memories') } }} /> : null}
        {destination === 'knowledge' && knowledgeView === 'notes' && workspaceReady ? <NotesView gateway={gateway} workspaceId={workspaceId} /> : null}
        {destination === 'activity' && workspaceReady ? <ActivityView gateway={gateway} workspaceId={workspaceId} /> : null}
        {destination === 'settings' && workspaceReady ? <SettingsView gateway={gateway} workspaceId={workspaceId} /> : null}
        {destination !== 'home' && destination !== 'ask' && destination !== 'activity' && destination !== 'settings' && !(destination === 'knowledge' && (knowledgeView === 'memories' || knowledgeView === 'history' || knowledgeView === 'sources' || knowledgeView === 'notes')) ? <section className="workspacePlaceholder"><p>{destination === 'knowledge' ? `${knowledgeView} in ${workspace?.name || 'this workspace'}` : `${destination} for ${workspace?.name || 'this workspace'}`}</p></section> : null}
      </Box>
    </AppShell.Main>
    {workspaceReady ? <SourceImportDialog gateway={gateway} workspaceId={workspaceId} open={importOpen} onClose={() => setImportOpen(false)} onImported={setImportedSource} onCreateNote={() => { setImportOpen(false); navigate('knowledge', 'notes') }} /> : null}
  </AppShell>
}
