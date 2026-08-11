export type DashboardRuntimeMode = 'standalone' | 'hosted'

export type DashboardRuntime = {
  schema: 'agent-memory-dashboard-runtime-v1'
  mode: DashboardRuntimeMode
  api_prefix: '/api/v1' | '/v1'
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
  const modeValid = value.mode === 'standalone' || value.mode === 'hosted'
  const prefixValid = value.api_prefix === '/api/v1' || value.api_prefix === '/v1'
  if (value.schema !== 'agent-memory-dashboard-runtime-v1' || !modeValid || !prefixValid || !Array.isArray(value.features)) {
    throw new Error('Runtime discovery returned an unsupported manifest.')
  }
  if ((value.mode === 'standalone' && value.api_prefix !== '/api/v1') || (value.mode === 'hosted' && value.api_prefix !== '/v1')) {
    throw new Error('Runtime discovery returned an inconsistent API boundary.')
  }
  if (!value.features.every((feature) => typeof feature === 'string')) {
    throw new Error('Runtime discovery returned invalid capabilities.')
  }
  return value as DashboardRuntime
}

