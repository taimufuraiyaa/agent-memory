export const workspaceDestinations = ['home', 'ask', 'knowledge', 'activity', 'settings'] as const
export type WorkspaceDestination = typeof workspaceDestinations[number]

export type WorkspaceRoute = {
  workspaceId: string
  destination: WorkspaceDestination
  knowledgeView: 'sources' | 'memories' | 'history' | 'notes'
  workItemType?: 'chat' | 'search'
  workItemId?: string
  panel?: 'recents' | 'skills' | 'overview'
}

export function readWorkspaceRoute(pathname = window.location.pathname): Partial<WorkspaceRoute> {
  const workItemMatch = pathname.match(/^\/w\/([^/]+)\/(chat|search)\/([^/]+)\/?$/)
  if (workItemMatch) {
    const params = new URLSearchParams(window.location.search)
    const panel = params.get('panel')
    return {
      workspaceId: safeDecode(workItemMatch[1]),
      destination: workItemMatch[2] === 'chat' ? 'ask' : 'knowledge',
      knowledgeView: 'memories',
      workItemType: workItemMatch[2] as WorkspaceRoute['workItemType'],
      workItemId: safeDecode(workItemMatch[3]),
      panel: panel === 'recents' || panel === 'skills' || panel === 'overview' ? panel : undefined,
    }
  }
  const match = pathname.match(/^\/w\/([^/]+)\/(home|ask|knowledge|activity|settings)(?:\/(sources|memories|history|notes))?\/?$/)
  if (!match) return readLegacyWorkspaceRoute(pathname)
  return {
    workspaceId: safeDecode(match[1]),
    destination: match[2] as WorkspaceDestination,
    knowledgeView: (match[3] || 'sources') as WorkspaceRoute['knowledgeView'],
  }
}

function safeDecode(value: string): string {
  try { return decodeURIComponent(value) } catch { return value }
}

export function readLegacyWorkspaceRoute(pathname: string, search = window.location.search): Partial<WorkspaceRoute> {
  const legacy: Record<string, Pick<WorkspaceRoute, 'destination' | 'knowledgeView'>> = {
    '/library': { destination: 'knowledge', knowledgeView: 'sources' },
    '/study': { destination: 'knowledge', knowledgeView: 'sources' },
    '/memory': { destination: 'knowledge', knowledgeView: 'memories' },
    '/wiki': { destination: 'knowledge', knowledgeView: 'memories' },
    '/notes': { destination: 'knowledge', knowledgeView: 'notes' },
    '/notebook': { destination: 'knowledge', knowledgeView: 'notes' },
    '/processing': { destination: 'activity', knowledgeView: 'sources' },
    '/data': { destination: 'settings', knowledgeView: 'sources' },
    '/settings': { destination: 'settings', knowledgeView: 'sources' },
  }
  const target = legacy[pathname.replace(/\/$/, '')]
  if (!target) return {}
  const workspaceId = new URLSearchParams(search).get('workspace') || ''
  return { ...target, workspaceId }
}

export function workspacePath(workspaceId: string, destination: WorkspaceDestination, knowledgeView: WorkspaceRoute['knowledgeView'] = 'sources', route?: Pick<WorkspaceRoute, 'workItemType' | 'workItemId' | 'panel'>): string {
  if (route?.workItemType && route.workItemId) {
    const base = `/w/${encodeURIComponent(workspaceId)}/${route.workItemType}/${encodeURIComponent(route.workItemId)}`
    return route.panel ? `${base}?panel=${route.panel}` : base
  }
  const base = `/w/${encodeURIComponent(workspaceId)}/${destination}`
  return destination === 'knowledge' ? `${base}/${knowledgeView}` : base
}

export function pushWorkspaceRoute(route: WorkspaceRoute): void {
  const path = route.workItemType && route.workItemId
    ? workspacePath(route.workspaceId, route.destination, route.knowledgeView, route)
    : workspacePath(route.workspaceId, route.destination, route.knowledgeView)
  if (`${window.location.pathname}${window.location.search}` !== path) window.history.pushState(route, '', path)
}

export function replaceWorkspaceRoute(route: WorkspaceRoute): void {
  const path = route.workItemType && route.workItemId
    ? workspacePath(route.workspaceId, route.destination, route.knowledgeView, route)
    : workspacePath(route.workspaceId, route.destination, route.knowledgeView)
  window.history.replaceState(route, '', path)
}
