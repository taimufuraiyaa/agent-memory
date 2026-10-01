import { useEffect, useMemo, useState, type ReactNode } from 'react'
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
  Menu,
  Paper,
  Stack,
  Text,
  ThemeIcon,
  Title,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { IconAdjustments, IconBrain, IconCheck, IconMoon, IconSun } from '@tabler/icons-react'
import type { DashboardRuntime } from '../lib/runtime'
import type { KnowledgeGateway, WorkspaceSummary } from '../lib/knowledgeGateway'
import {
  pushWorkspaceRoute,
  readWorkspaceRoute,
  replaceWorkspaceRoute,
  type WorkspaceRoute,
} from './workspace/workspaceRoute'
import './workspace/workspace.css'
import { AskView } from './workspace/AskView'
import { MemoryExplorer } from './workspace/MemoryExplorer'
import { SourcesView } from './workspace/SourcesView'
import { SourceImportDialog } from './workspace/SourceImportDialog'
import { NotesView } from './workspace/NotesView'
import { ActivityView } from './workspace/ActivityView'
import { SettingsView, WorkspaceSkillsView } from './workspace/SettingsView'
import { HomeView } from './workspace/HomeView'
import { HowHistoryView } from './workspace/HowHistoryView'
import { WorkspaceChatView } from './workspace/WorkspaceChatView'
import { WorkspaceExplorer, type WorkspaceAction } from './workspace/WorkspaceExplorer'
import { WorkspaceOverviewPanel, WorkspaceRecentsPanel } from './workspace/WorkspaceContextViews'
import {
  MAX_WORK_ITEMS_PER_WORKSPACE,
  createChatRecord,
  createSearchRecord,
  clearAllWorkspaceWorkItems,
  deleteWorkspaceWorkItem,
  readWorkspaceWorkItems,
  renameWorkspaceWorkItem,
  saveWorkspaceWorkItem,
  titleFromPrompt,
  type WorkspaceWorkItem,
} from './workspace/workspaceRecords'

export type DashboardColorScheme = 'dark' | 'light'
export type DashboardVisualTheme = 'atlas' | 'classic'
type StoredRoute = WorkspaceRoute
type ContextPanel = NonNullable<WorkspaceRoute['panel']>

function routeForWorkspace(workspaceId: string): StoredRoute {
  return { workspaceId, destination: 'home', knowledgeView: 'sources' }
}

export function WorkspaceApp({ runtime, gateway, colorScheme, onColorSchemeChange, visualTheme, onVisualThemeChange }: { runtime: DashboardRuntime; gateway: KnowledgeGateway; colorScheme: DashboardColorScheme; onColorSchemeChange: (value: DashboardColorScheme) => void; visualTheme: DashboardVisualTheme; onVisualThemeChange: (value: DashboardVisualTheme) => void }) {
  const initialRoute = useMemo(() => readWorkspaceRoute(), [])
  const [workspaces, setWorkspaces] = useState<WorkspaceSummary[]>([])
  const [route, setRoute] = useState<StoredRoute>(() => ({
    workspaceId: initialRoute.workspaceId || '',
    destination: initialRoute.destination || 'home',
    knowledgeView: initialRoute.knowledgeView || 'sources',
    workItemType: initialRoute.workItemType,
    workItemId: initialRoute.workItemId,
    panel: initialRoute.panel,
  }))
  const [itemsByWorkspace, setItemsByWorkspace] = useState<Record<string, WorkspaceWorkItem[]>>({})
  const [historyIssues, setHistoryIssues] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [importOpen, setImportOpen] = useState(false)
  const [importedSource, setImportedSource] = useState<import('../lib/knowledgeGateway').SourceSummary | null>(null)
  const [memoryInitialView, setMemoryInitialView] = useState<'search' | 'browse'>('search')
  const [mobileExplorerOpen, mobileExplorer] = useDisclosure(false)
  const [contextDrawerOpen, setContextDrawerOpen] = useState(false)
  const workspaceId = route.workspaceId

  useEffect(() => {
    const controller = new AbortController()
    gateway.listWorkspaces(controller.signal).then((items) => {
      if (controller.signal.aborted) return
      const loadedRecords: Record<string, WorkspaceWorkItem[]> = {}
      const issues: Record<string, string> = {}
      for (const item of items) {
        const state = readWorkspaceWorkItems(item.id)
        loadedRecords[item.id] = state.items
        if (state.error) issues[item.id] = state.error
      }
      const nextWorkspace = items.find((item) => item.id === initialRoute.workspaceId)?.id || items[0]?.id || ''
      let nextRoute: StoredRoute = nextWorkspace ? { ...routeForWorkspace(nextWorkspace), ...initialRoute, workspaceId: nextWorkspace } : routeForWorkspace('')
      const initialWorkItem = nextRoute.workItemId ? loadedRecords[nextWorkspace]?.find((item) => item.id === nextRoute.workItemId && item.type === nextRoute.workItemType) : undefined
      if (nextRoute.workItemId && !initialWorkItem) nextRoute = routeForWorkspace(nextWorkspace)
      setWorkspaces(items)
      setItemsByWorkspace(loadedRecords)
      setHistoryIssues(issues)
      setRoute(nextRoute)
      if (nextWorkspace) replaceWorkspaceRoute(nextRoute)
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Workspaces could not be loaded.')
    })
    return () => controller.abort()
  }, [gateway])

  useEffect(() => {
    const onPopState = () => {
      const next = readWorkspaceRoute()
      setRoute((current) => ({ ...current, ...next, workspaceId: next.workspaceId || current.workspaceId }))
      setContextDrawerOpen(false)
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  useEffect(() => {
    if (route.workItemType === 'chat' && route.panel) setContextDrawerOpen(true)
  }, [route.workItemType, route.workItemId, route.panel])

  function navigate(next: StoredRoute) {
    setRoute(next)
    if (next.workspaceId) pushWorkspaceRoute(next)
    setContextDrawerOpen(false)
    mobileExplorer.close()
  }

  function selectWorkspace(nextWorkspaceId: string) {
    setError('')
    navigate(routeForWorkspace(nextWorkspaceId))
  }

  function selectWorkItem(item: WorkspaceWorkItem) {
    navigate({
      workspaceId: item.workspaceId,
      destination: item.type === 'chat' ? 'ask' : 'knowledge',
      knowledgeView: 'memories',
      workItemType: item.type,
      workItemId: item.id,
      panel: undefined,
    })
  }

  function updateLocalRecord(item: WorkspaceWorkItem) {
    setItemsByWorkspace((current) => {
      const records = current[item.workspaceId] || []
      const exists = records.some((record) => record.id === item.id)
      return { ...current, [item.workspaceId]: exists ? records.map((record) => record.id === item.id ? item : record) : [...records, item] }
    })
    try {
      saveWorkspaceWorkItem(item)
      setHistoryIssues((current) => { const next = { ...current }; delete next[item.workspaceId]; return next })
    } catch (reason) {
      setHistoryIssues((current) => ({ ...current, [item.workspaceId]: reason instanceof Error ? reason.message : 'Local history could not be saved.' }))
    }
  }

  function createWorkItem(targetWorkspaceId: string, type: 'chat' | 'search', initialQuery = '') {
    const existing = itemsByWorkspace[targetWorkspaceId] || []
    if (existing.length >= MAX_WORK_ITEMS_PER_WORKSPACE) {
      setHistoryIssues((current) => ({ ...current, [targetWorkspaceId]: `This workspace can store up to ${MAX_WORK_ITEMS_PER_WORKSPACE} chats and searches. Delete a local record before creating another.` }))
      return
    }
    const item = type === 'chat' ? createChatRecord(targetWorkspaceId) : createSearchRecord(targetWorkspaceId)
    const createdItem = item.type === 'search' && initialQuery.trim()
      ? { ...item, query: initialQuery, title: titleFromPrompt(initialQuery, item.title) }
      : item
    updateLocalRecord(createdItem)
    navigate({ workspaceId: targetWorkspaceId, destination: type === 'chat' ? 'ask' : 'knowledge', knowledgeView: 'memories', workItemType: type, workItemId: createdItem.id })
  }

  function handleWorkspaceAction(targetWorkspaceId: string, action: WorkspaceAction) {
    if (action === 'new-chat') { createWorkItem(targetWorkspaceId, 'chat'); return }
    if (action === 'new-search') { createWorkItem(targetWorkspaceId, 'search'); return }
    setRoute((current) => ({ ...current, workspaceId: targetWorkspaceId }))
    const base = routeForWorkspace(targetWorkspaceId)
    if (action === 'overview') { navigate(base); return }
    if (action === 'add-source') {
      navigate({ ...base, destination: 'knowledge', knowledgeView: 'sources' })
      setImportOpen(true)
      return
    }
    if (action === 'ask') { navigate({ ...base, destination: 'ask' }); return }
    if (action === 'sources') { navigate({ ...base, destination: 'knowledge', knowledgeView: 'sources' }); return }
    if (action === 'memories') { setMemoryInitialView('browse'); navigate({ ...base, destination: 'knowledge', knowledgeView: 'memories' }); return }
    if (action === 'history') { navigate({ ...base, destination: 'knowledge', knowledgeView: 'history' }); return }
    if (action === 'notes') { navigate({ ...base, destination: 'knowledge', knowledgeView: 'notes' }); return }
    if (action === 'activity') { navigate({ ...base, destination: 'activity' }); return }
    navigate({ ...base, destination: 'settings' })
  }

  function handleRecordAction(item: WorkspaceWorkItem, action: 'rename' | 'delete') {
    if (action === 'rename') {
      const title = window.prompt(`Rename ${item.type}:`, item.title)
      if (!title?.trim()) return
      try { updateLocalRecord(renameWorkspaceWorkItem(item, title)) } catch (reason) { setHistoryIssues((current) => ({ ...current, [item.workspaceId]: reason instanceof Error ? reason.message : 'This record could not be renamed.' })) }
      return
    }
    if (!window.confirm(`Delete this local ${item.type} from this browser? Remote memories and sources will not be deleted.`)) return
    try { deleteWorkspaceWorkItem(item.workspaceId, item.id) } catch (reason) { setHistoryIssues((current) => ({ ...current, [item.workspaceId]: reason instanceof Error ? reason.message : 'This local record could not be deleted.' })); return }
    setItemsByWorkspace((current) => ({ ...current, [item.workspaceId]: (current[item.workspaceId] || []).filter((record) => record.id !== item.id) }))
    if (route.workItemId === item.id) navigate(routeForWorkspace(item.workspaceId))
  }

  function recoverUnreadableHistory() {
    if (!window.confirm('Clear all local chats and searches from this browser? This will not delete remote memories or sources.')) return
    try {
      clearAllWorkspaceWorkItems()
      setItemsByWorkspace({})
      setHistoryIssues({})
      if (route.workItemId) navigate(routeForWorkspace(workspaceId))
    } catch (reason) {
      setHistoryIssues((current) => ({ ...current, [workspaceId]: reason instanceof Error ? reason.message : 'Local history could not be cleared.' }))
    }
  }

  const workspace = workspaces.find((item) => item.id === workspaceId)
  const workspaceReady = Boolean(workspace)
  const workspaceItems = itemsByWorkspace[workspaceId] || []
  const activeWorkItem = route.workItemId ? workspaceItems.find((item) => item.id === route.workItemId && item.type === route.workItemType) : undefined
  const activeChat = activeWorkItem?.type === 'chat' ? activeWorkItem : undefined
  const activeSearch = activeWorkItem?.type === 'search' ? activeWorkItem : undefined
  const currentPanel: ContextPanel = route.panel || 'overview'
  const pageTitle = activeWorkItem?.title || (route.destination === 'home' ? workspace?.name : route.destination === 'knowledge' ? route.knowledgeView[0].toUpperCase() + route.knowledgeView.slice(1) : route.destination[0].toUpperCase() + route.destination.slice(1))
  const activeHistoryIssue = historyIssues[workspaceId]

  const explorer = <WorkspaceExplorer
    workspaces={workspaces}
    itemsByWorkspace={itemsByWorkspace}
    workspaceId={workspaceId}
    workItemId={route.workItemId}
    onSelectWorkspace={selectWorkspace}
    onSelectWorkItem={selectWorkItem}
    onWorkspaceAction={handleWorkspaceAction}
    onRecordAction={handleRecordAction}
  />

  function renderContextPanel(): ReactNode {
    if (!workspace) return null
    if (currentPanel === 'recents') return <WorkspaceRecentsPanel gateway={gateway} workspaceId={workspace.id} onBrowseAll={() => handleWorkspaceAction(workspace.id, 'memories')} />
    if (currentPanel === 'skills') return <WorkspaceSkillsView gateway={gateway} workspaceId={workspace.id} />
    return <WorkspaceOverviewPanel workspace={workspace} onAction={(action) => handleWorkspaceAction(workspace.id, action)} />
  }

  function openContextPanel(panel: ContextPanel) {
    navigate({ ...route, panel })
    setContextDrawerOpen(true)
  }

  function onHomeNavigate(target: 'ask' | 'sources' | 'search' | 'browse' | 'activity') {
    if (target === 'ask') handleWorkspaceAction(workspaceId, 'new-chat')
    else if (target === 'sources') handleWorkspaceAction(workspaceId, 'sources')
    else if (target === 'activity') handleWorkspaceAction(workspaceId, 'activity')
    else handleWorkspaceAction(workspaceId, target === 'browse' ? 'memories' : 'new-search')
  }

  return <AppShell className="workspaceApp" data-shell="vscode" data-runtime={runtime.mode} data-visual-theme={visualTheme} header={{ height: 68 }} navbar={{ width: 288, breakpoint: 'sm', collapsed: { mobile: true } }} padding={0} transitionDuration={160}>
    <AppShell.Header className="workspaceHeader">
      <Group className="workspaceHeaderRow" justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" miw={0}>
          <Burger opened={mobileExplorerOpen} onClick={mobileExplorer.toggle} hiddenFrom="sm" size="sm" aria-label="Open workspace explorer" />
          <Group className="workspaceBrand" gap="sm" wrap="nowrap">
            <ThemeIcon size={34} radius="md" variant="gradient" gradient={{ from: 'memory.5', to: 'memory.8', deg: 145 }}><IconBrain size={21} stroke={1.8} /></ThemeIcon>
            <Text fw={750} lh={1.1} visibleFrom="sm">Agent Memory</Text>
          </Group>
          <Text className="workspaceHeaderContext" size="sm" c="dimmed" truncate>{workspace?.name || 'Loading workspace'}</Text>
        </Group>
        <Group gap="xs" wrap="nowrap">
          <Menu position="bottom-end" shadow="md">
            <Menu.Target><ActionIcon variant="subtle" size="lg" aria-label="Appearance options"><IconAdjustments size={18} /></ActionIcon></Menu.Target>
            <Menu.Dropdown aria-label="Appearance options">
              <Menu.Label>Visual style</Menu.Label>
              <Menu.Item leftSection={visualTheme === 'atlas' ? <IconCheck size={15} /> : null} onClick={() => onVisualThemeChange('atlas')}>Living Memory Atlas</Menu.Item>
              <Menu.Item leftSection={visualTheme === 'classic' ? <IconCheck size={15} /> : null} onClick={() => onVisualThemeChange('classic')}>Classic Workspace</Menu.Item>
            </Menu.Dropdown>
          </Menu>
          <ActionIcon className="workspaceThemeToggle" variant="subtle" size="lg" aria-label={`Switch to ${colorScheme === 'dark' ? 'light' : 'dark'} theme`} onClick={() => onColorSchemeChange(colorScheme === 'dark' ? 'light' : 'dark')}>
            {colorScheme === 'dark' ? <IconSun size={20} /> : <IconMoon size={20} />}
          </ActionIcon>
        </Group>
      </Group>
    </AppShell.Header>
    <AppShell.Navbar className="workspaceRail" p="sm" aria-label="Workspace explorer">{explorer}</AppShell.Navbar>
    <Drawer opened={mobileExplorerOpen} onClose={mobileExplorer.close} title="Workspaces" size="xs" hiddenFrom="sm" className="workspaceMobileDrawer">{explorer}</Drawer>
    <AppShell.Main className={`workspaceMain${activeChat ? ' workspaceMain--chat' : ''}`} id="workspace-main">
      <Box className="workspaceCanvas">
        {error ? <Alert className="workspaceError" color="red" title="Workspace unavailable" role="alert">{error}</Alert> : null}
        {activeHistoryIssue ? <Alert className="workspaceHistoryIssue" color="yellow" title="Local history status" role="status"><Stack gap="xs"><Text size="sm">{activeHistoryIssue}</Text>{/unreadable|unsupported format|exceeds its (size|item) limit|invalid (workspace )?record/i.test(activeHistoryIssue) ? <Button size="xs" variant="light" color="orange" onClick={recoverUnreadableHistory}>Clear all local chats and searches</Button> : null}</Stack></Alert> : null}
        {!error && workspaceId && !workspaceReady ? <Paper withBorder p="xl" radius="lg"><Group justify="center"><Loader size="sm" /><Text c="dimmed">Loading workspace…</Text></Group></Paper> : null}
        {workspace && activeChat && gateway.supports('ask', { workspaceId: workspace.id }) ? <Stack className="workspaceChatPage" gap="md">
          <Group className="workspaceChatPageHeader" justify="space-between" align="center" wrap="wrap">
            <Title order={1}>{activeChat.title}</Title>
            <Group className="workspaceChatPanelPicker" gap={4} role="group" aria-label="Chat workspace views">
              {(['recents', 'skills', 'overview'] as const).map((panel) => <Button key={panel} className="workspaceChatPanelOption" size="xs" variant={contextDrawerOpen && currentPanel === panel ? 'light' : 'subtle'} aria-pressed={contextDrawerOpen && currentPanel === panel} onClick={() => openContextPanel(panel)}>{panel[0].toUpperCase() + panel.slice(1)}</Button>)}
            </Group>
          </Group>
          <div className="workspaceChatLayout">
            <WorkspaceChatView key={`${workspace.id}:${activeChat.id}`} gateway={gateway} workspaceId={workspace.id} item={activeChat} onUpdate={updateLocalRecord} onCreateSearch={(query) => createWorkItem(workspace.id, 'search', query || '')} />
          </div>
          <Drawer opened={contextDrawerOpen} onClose={() => setContextDrawerOpen(false)} position="right" size="min(480px, 92vw)" title={currentPanel[0].toUpperCase() + currentPanel.slice(1)} className="workspaceContextDrawer">{renderContextPanel()}</Drawer>
        </Stack> : null}
        {workspace && route.workItemType === 'search' && activeSearch && gateway.supports('search', { workspaceId: workspace.id }) ? <Stack gap="md"><Group justify="space-between"><div><Text className="workspaceEyebrow" size="xs" fw={700} tt="uppercase">{workspace.name} · Saved search</Text><Title order={1}>{activeSearch.title}</Title></div><Badge variant="light">Local to this browser</Badge></Group><MemoryExplorer key={`${workspace.id}:${activeSearch.id}`} gateway={gateway} workspaceId={workspace.id} initialView="search" initialQuery={activeSearch.query} onQuerySubmitted={(query) => updateLocalRecord({ ...activeSearch, query, title: activeSearch.titleEdited ? activeSearch.title : query.slice(0, 80) || 'New search', updatedAt: new Date().toISOString() })} /></Stack> : null}
        {workspace && activeChat && !gateway.supports('ask', { workspaceId: workspace.id }) ? <Alert color="gray" title="Chat unavailable">This runtime does not support questions for the selected workspace.</Alert> : null}
        {workspace && activeSearch && !gateway.supports('search', { workspaceId: workspace.id }) ? <Alert color="gray" title="Search unavailable">This runtime does not support memory search for the selected workspace.</Alert> : null}
        {workspace && !activeWorkItem && route.destination === 'home' ? <><Group className="workspacePageHeader" justify="space-between" align="flex-start"><Box><Text className="workspaceEyebrow" size="xs" fw={700} tt="uppercase">Workspace overview</Text><Title order={1}>{workspace.name}</Title><Text c="dimmed" size="sm">Choose a project action or start a natural-language chat.</Text></Box><Badge variant="light" color={workspace.connectionState === 'connected' ? 'green' : 'gray'}>{workspace.connectionState}</Badge></Group><HomeView workspace={workspace} onAddSource={() => handleWorkspaceAction(workspace.id, 'add-source')} onNavigate={onHomeNavigate} /></> : null}
        {workspace && !activeWorkItem && route.destination === 'ask' ? <Stack gap="md"><Group justify="space-between"><div><Text className="workspaceEyebrow" size="xs" fw={700} tt="uppercase">Advanced retrieval</Text><Title order={1}>Ask this workspace</Title></div></Group><AskView gateway={gateway} workspaceId={workspace.id} onOpenSearch={() => handleWorkspaceAction(workspace.id, 'memories')} onOpenSources={() => handleWorkspaceAction(workspace.id, 'sources')} /></Stack> : null}
        {workspace && !activeWorkItem && route.destination === 'knowledge' && route.knowledgeView === 'memories' ? <Stack gap="md"><Group justify="space-between"><div><Text className="workspaceEyebrow" size="xs" fw={700} tt="uppercase">{workspace.name}</Text><Title order={1}>Memories</Title></div></Group><MemoryExplorer gateway={gateway} workspaceId={workspace.id} initialView={memoryInitialView} /></Stack> : null}
        {workspace && !activeWorkItem && route.destination === 'knowledge' && route.knowledgeView === 'history' ? <HowHistoryView gateway={gateway} workspaceId={workspace.id} /> : null}
        {workspace && !activeWorkItem && route.destination === 'knowledge' && route.knowledgeView === 'sources' ? <SourcesView gateway={gateway} workspaceId={workspace.id} importedSource={importedSource} onNavigate={(target) => target === 'ask' ? handleWorkspaceAction(workspace.id, 'new-chat') : handleWorkspaceAction(workspace.id, 'memories')} /> : null}
        {workspace && !activeWorkItem && route.destination === 'knowledge' && route.knowledgeView === 'notes' ? <NotesView gateway={gateway} workspaceId={workspace.id} /> : null}
        {workspace && !activeWorkItem && route.destination === 'activity' ? <ActivityView gateway={gateway} workspaceId={workspace.id} /> : null}
        {workspace && !activeWorkItem && route.destination === 'settings' ? <SettingsView gateway={gateway} workspaceId={workspace.id} /> : null}
        {workspace && route.workItemId && !activeWorkItem ? <Alert color="yellow" title="Local work item unavailable">This chat or search is missing from this browser. Select its workspace to start a new item.</Alert> : null}
        {workspace ? <SourceImportDialog gateway={gateway} workspaceId={workspace.id} open={importOpen} onClose={() => setImportOpen(false)} onImported={setImportedSource} onCreateNote={() => { setImportOpen(false); handleWorkspaceAction(workspace.id, 'notes') }} /> : null}
      </Box>
    </AppShell.Main>
  </AppShell>
}
