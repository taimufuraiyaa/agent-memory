export const workspaceDestinations = ['home', 'ask', 'knowledge', 'activity', 'settings'] as const
export type WorkspaceDestination = typeof workspaceDestinations[number]

export type WorkspaceRoute = {
  workspaceId: string
  destination: WorkspaceDestination
  knowledgeView: 'sources' | 'memories' | 'history' | 'notes'
}

export function readWorkspaceRoute(pathname = window.location.pathname): Partial<WorkspaceRoute> {
  const match = pathname.match(/^\/w\/([^/]+)\/(home|ask|knowledge|activity|settings)(?:\/(sources|memories|history|notes))?\/?$/)
  if (!match) return readLegacyWorkspaceRoute(pathname)
  return {
    workspaceId: decodeURIComponent(match[1]),
    destination: match[2] as WorkspaceDestination,
    knowledgeView: (match[3] || 'sources') as WorkspaceRoute['knowledgeView'],
  }
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

export function workspacePath(workspaceId: string, destination: WorkspaceDestination, knowledgeView: WorkspaceRoute['knowledgeView'] = 'sources'): string {
  const base = `/w/${encodeURIComponent(workspaceId)}/${destination}`
  return destination === 'knowledge' ? `${base}/${knowledgeView}` : base
}

export function pushWorkspaceRoute(route: WorkspaceRoute): void {
  const path = workspacePath(route.workspaceId, route.destination, route.knowledgeView)
  if (window.location.pathname !== path) window.history.pushState(route, '', path)
}

export function replaceWorkspaceRoute(route: WorkspaceRoute): void {
  window.history.replaceState(route, '', workspacePath(route.workspaceId, route.destination, route.knowledgeView))
}
