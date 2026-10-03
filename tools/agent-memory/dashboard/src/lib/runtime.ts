export type DashboardRuntimeMode = 'standalone'

export type DashboardRuntime = {
  schema: 'agent-memory-dashboard-runtime-v1'
  mode: DashboardRuntimeMode
  api_prefix: '/api/v1'
  features: string[]
}

export async function loadDashboardRuntime(): Promise<DashboardRuntime> {
  const response = await fetch('/dashboard/runtime.json', {
    method: 'GET',
    headers: { Accept: 'application/json' },
    cache: 'no-store',
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Runtime discovery failed (${response.status}).`)
  const value = await response.json() as Partial<DashboardRuntime>
  if (value.schema !== 'agent-memory-dashboard-runtime-v1' || value.mode !== 'standalone' || value.api_prefix !== '/api/v1' || !Array.isArray(value.features)) {
    throw new Error('Runtime discovery returned an unsupported manifest.')
  }
  if (!value.features.every((feature) => typeof feature === 'string')) {
    throw new Error('Runtime discovery returned invalid capabilities.')
  }
  return value as DashboardRuntime
}
