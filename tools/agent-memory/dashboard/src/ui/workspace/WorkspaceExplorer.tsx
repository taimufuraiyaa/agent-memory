import { useEffect, useState } from 'react'
import { ActionIcon, Badge, Box, Group, Menu, ScrollArea, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { IconActivity, IconBook2, IconChevronDown, IconChevronRight, IconDots, IconFolder, IconHistory, IconMessageCircle, IconNotes, IconPlus, IconSearch, IconSettings, IconWand } from '@tabler/icons-react'
import type { KnowledgeCapability, WorkspaceSummary } from '../../lib/knowledgeGateway'
import type { WorkspaceWorkItem } from './workspaceRecords'

export type WorkspaceAction = 'new-chat' | 'new-search' | 'overview' | 'ask' | 'sources' | 'memories' | 'history' | 'notes' | 'activity' | 'settings' | 'add-source'

export function WorkspaceExplorer({ workspaces, itemsByWorkspace, workspaceId, workItemId, onSelectWorkspace, onSelectWorkItem, onWorkspaceAction, onRecordAction }: {
  workspaces: WorkspaceSummary[]
  itemsByWorkspace: Record<string, WorkspaceWorkItem[]>
  workspaceId: string
  workItemId?: string
  onSelectWorkspace: (workspaceId: string) => void
  onSelectWorkItem: (item: WorkspaceWorkItem) => void
  onWorkspaceAction: (workspaceId: string, action: WorkspaceAction) => void
  onRecordAction: (item: WorkspaceWorkItem, action: 'rename' | 'delete') => void
}) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(workspaceId ? [workspaceId] : []))
  const [openMenu, setOpenMenu] = useState('')
  useEffect(() => { if (workspaceId) setExpanded((current) => new Set(current).add(workspaceId)) }, [workspaceId])

  function openWorkspaceMenu(id: string) {
    onSelectWorkspace(id)
    setExpanded((current) => new Set(current).add(id))
    setOpenMenu(`workspace:${id}`)
  }

  function workspaceMenu(id: string) {
    const workspace = workspaces.find((item) => item.id === id)
    const supports = (capability: KnowledgeCapability) => Boolean(workspace?.capabilities.includes(capability))
    return <Menu opened={openMenu === `workspace:${id}`} onChange={(opened) => setOpenMenu(opened ? `workspace:${id}` : '')} position="right-start" shadow="md" withinPortal>
      <Menu.Target><ActionIcon className="workspaceTreeMore" variant="subtle" size="sm" aria-label={`Workspace actions for ${workspaces.find((item) => item.id === id)?.name || id}`}><IconDots size={17} /></ActionIcon></Menu.Target>
      <Menu.Dropdown aria-label="Workspace actions">
        {supports('ask') ? <Menu.Item leftSection={<IconMessageCircle size={16} />} onClick={() => onWorkspaceAction(id, 'new-chat')}>New chat</Menu.Item> : null}
        {supports('search') ? <Menu.Item leftSection={<IconSearch size={16} />} onClick={() => onWorkspaceAction(id, 'new-search')}>New search</Menu.Item> : null}
        <Menu.Divider />
        <Menu.Item leftSection={<IconFolder size={16} />} onClick={() => onWorkspaceAction(id, 'overview')}>Overview</Menu.Item>
        {supports('ask') ? <Menu.Item leftSection={<IconMessageCircle size={16} />} onClick={() => onWorkspaceAction(id, 'ask')}>Advanced Ask</Menu.Item> : null}
        {supports('source') ? <Menu.Item leftSection={<IconBook2 size={16} />} onClick={() => onWorkspaceAction(id, 'sources')}>Sources and study</Menu.Item> : null}
        {supports('browse') ? <Menu.Item leftSection={<IconSearch size={16} />} onClick={() => onWorkspaceAction(id, 'memories')}>Browse memories</Menu.Item> : null}
        {supports('activity') ? <Menu.Item leftSection={<IconActivity size={16} />} onClick={() => onWorkspaceAction(id, 'activity')}>Activity</Menu.Item> : null}
        {supports('activity') ? <Menu.Item leftSection={<IconHistory size={16} />} onClick={() => onWorkspaceAction(id, 'history')}>How history</Menu.Item> : null}
        <Menu.Divider />
        {supports('note') ? <Menu.Item leftSection={<IconNotes size={16} />} onClick={() => onWorkspaceAction(id, 'notes')}>Notes</Menu.Item> : null}
        {supports('source') ? <Menu.Item leftSection={<IconPlus size={16} />} onClick={() => onWorkspaceAction(id, 'add-source')}>Add source</Menu.Item> : null}
        {supports('settings') ? <Menu.Item leftSection={<IconSettings size={16} />} onClick={() => onWorkspaceAction(id, 'settings')}>Settings</Menu.Item> : null}
      </Menu.Dropdown>
    </Menu>
  }

  function recordMenu(item: WorkspaceWorkItem) {
    return <Menu opened={openMenu === `record:${item.id}`} onChange={(opened) => setOpenMenu(opened ? `record:${item.id}` : '')} position="right-start" shadow="md" withinPortal>
      <Menu.Target><ActionIcon className="workspaceTreeMore" variant="subtle" size="sm" aria-label={`Actions for ${item.title}`}><IconDots size={16} /></ActionIcon></Menu.Target>
      <Menu.Dropdown aria-label={`${item.type === 'chat' ? 'Chat' : 'Search'} actions`}>
        <Menu.Item onClick={() => onRecordAction(item, 'rename')}>Rename</Menu.Item>
        <Menu.Item color="red" onClick={() => onRecordAction(item, 'delete')}>Delete local {item.type}</Menu.Item>
      </Menu.Dropdown>
    </Menu>
  }

  return <Stack className="workspaceExplorer" gap="xs" aria-label="Workspace explorer">
    <Group className="workspaceExplorerHeading" justify="space-between" px="xs">
      <Text size="xs" fw={700} tt="uppercase" c="dimmed">Explorer</Text>
      {workspaceId && workspaces.find((item) => item.id === workspaceId)?.capabilities.includes('ask') ? <Tooltip label="New chat"><ActionIcon variant="subtle" size="sm" aria-label="New chat in selected workspace" onClick={() => onWorkspaceAction(workspaceId, 'new-chat')}><IconPlus size={17} /></ActionIcon></Tooltip> : null}
    </Group>
    <ScrollArea className="workspaceExplorerScroll" type="auto" offsetScrollbars>
      <Stack gap={3}>
        {!workspaces.length ? <Text size="sm" c="dimmed" px="sm" py="md">No workspaces are available.</Text> : null}
        {workspaces.map((workspace) => {
          const isActive = workspaceId === workspace.id
          const isExpanded = expanded.has(workspace.id)
          const items = (itemsByWorkspace[workspace.id] || []).slice().sort((first, second) => second.updatedAt.localeCompare(first.updatedAt))
          const chats = items.filter((item) => item.type === 'chat')
          const searches = items.filter((item) => item.type === 'search')
          return <Box className={isActive ? 'workspaceTreeWorkspace isActive' : 'workspaceTreeWorkspace'} key={workspace.id} onContextMenu={(event) => { event.preventDefault(); openWorkspaceMenu(workspace.id) }} onKeyDown={(event) => { if (event.shiftKey && event.key === 'F10') { event.preventDefault(); openWorkspaceMenu(workspace.id) } }}>
            <Group className="workspaceTreeRootRow" gap={3} wrap="nowrap">
              <ActionIcon variant="subtle" size="sm" aria-label={`${isExpanded ? 'Collapse' : 'Expand'} ${workspace.name}`} onClick={() => setExpanded((current) => { const next = new Set(current); if (next.has(workspace.id)) next.delete(workspace.id); else next.add(workspace.id); return next })}>{isExpanded ? <IconChevronDown size={15} /> : <IconChevronRight size={15} />}</ActionIcon>
              <UnstyledButton className="workspaceTreeWorkspaceButton" aria-current={isActive && !workItemId ? 'page' : undefined} onClick={() => onSelectWorkspace(workspace.id)}>
                <IconFolder size={17} stroke={1.7} />
                <span className="workspaceTreeWorkspaceName" title={workspace.name}>{workspace.name}</span>
                <Badge className="workspaceTreeCount" size="xs" variant="light">{workspace.memoryCount}</Badge>
              </UnstyledButton>
              {workspaceMenu(workspace.id)}
            </Group>
            {isExpanded ? <Stack className="workspaceTreeChildren" gap={2}>
              {chats.length ? <Text className="workspaceTreeSectionLabel" size="xs" c="dimmed">CHATS</Text> : null}
              {chats.map((item) => <Group className={workItemId === item.id ? 'workspaceTreeItem isSelected' : 'workspaceTreeItem'} key={item.id} gap={2} wrap="nowrap">
                <UnstyledButton className="workspaceTreeItemButton" aria-current={workItemId === item.id ? 'page' : undefined} onClick={() => onSelectWorkItem(item)}><IconMessageCircle size={15} /><span title={item.title}>{item.title}</span></UnstyledButton>
                {recordMenu(item)}
              </Group>)}
              {searches.length ? <Text className="workspaceTreeSectionLabel" size="xs" c="dimmed">SEARCHES</Text> : null}
              {searches.map((item) => <Group className={workItemId === item.id ? 'workspaceTreeItem isSelected' : 'workspaceTreeItem'} key={item.id} gap={2} wrap="nowrap">
                <UnstyledButton className="workspaceTreeItemButton" aria-current={workItemId === item.id ? 'page' : undefined} onClick={() => onSelectWorkItem(item)}><IconSearch size={15} /><span title={item.title}>{item.title}</span></UnstyledButton>
                {recordMenu(item)}
              </Group>)}
              {!items.length ? <Group className="workspaceTreeEmpty" gap="xs" px="sm"><IconWand size={15} /><Text size="xs" c="dimmed">No chats or searches yet</Text></Group> : null}
            </Stack> : null}
            <Text className="workspaceTreeStatus" size="xs" c="dimmed" hidden={!isActive}>{workspace.connectionState === 'connected' ? 'Connected' : workspace.connectionState}</Text>
          </Box>
        })}
      </Stack>
    </ScrollArea>
    <Group className="workspaceExplorerFooter" gap="xs" px="xs"><Badge size="xs" variant="light" color="memory">{workspaces.length} workspaces</Badge><Text size="xs" c="dimmed">Right-click a project for actions</Text></Group>
  </Stack>
}
