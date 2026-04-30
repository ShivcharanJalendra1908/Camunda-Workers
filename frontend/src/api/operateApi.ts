// api/operateApi.ts
import type {
  DeployedProcess,
  Incident,
  Job,
  ProcessInstance,
  ProcessInstanceFilter,
  ProcessInstanceListResponse,
  Variable,
} from '../types/operate'

const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}/operate${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error ?? `HTTP ${res.status}`)
  }
  return res.json()
}

// ── Processes ─────────────────────────────────────────────────────────────────
export const listProcesses = (): Promise<{ items: DeployedProcess[]; totalCount: number }> =>
  request('/processes')

// ── Instances ─────────────────────────────────────────────────────────────────
export const listInstances = (filter: ProcessInstanceFilter): Promise<ProcessInstanceListResponse> => {
  const params = new URLSearchParams()
  if (filter.bpmnProcessId) params.set('bpmnProcessId', filter.bpmnProcessId)
  if (filter.state)         params.set('state', filter.state)
  if (filter.page)          params.set('page', String(filter.page))
  if (filter.pageSize)      params.set('pageSize', String(filter.pageSize))
  return request(`/instances?${params}`)
}

export const getInstance = (key: number): Promise<ProcessInstance> =>
  request(`/instances/${key}`)

export const cancelInstance = (key: number): Promise<void> =>
  request(`/instances/${key}/cancel`, { method: 'POST' })

export const modifyInstance = (
  key: number,
  activateElementId?: string,
  terminateElementInstanceKey?: number,
): Promise<void> =>
  request(`/instances/${key}/modify`, {
    method: 'POST',
    body: JSON.stringify({ activateElementId, terminateElementInstanceKey }),
  })

// ── Variables ─────────────────────────────────────────────────────────────────
export const listVariables = (instanceKey: number): Promise<{ items: Variable[] }> =>
  request(`/instances/${instanceKey}/variables`)

export const setVariables = (
  instanceKey: number,
  scopeKey: number,
  variables: Record<string, unknown>,
  local = false,
): Promise<void> =>
  request(`/instances/${instanceKey}/variables/${scopeKey}`, {
    method: 'POST',
    body: JSON.stringify({ variables, local }),
  })

// ── Jobs ──────────────────────────────────────────────────────────────────────
export const listJobs = (instanceKey: number): Promise<{ items: Job[] }> =>
  request(`/instances/${instanceKey}/jobs`)

export const listFailedJobs = (): Promise<{ items: Job[] }> =>
  request('/jobs/failed')

export const updateJobRetries = (jobKey: number, retries: number): Promise<void> =>
  request(`/jobs/${jobKey}/retries`, {
    method: 'POST',
    body: JSON.stringify({ retries }),
  })

// ── Incidents ─────────────────────────────────────────────────────────────────
export const listIncidents = (): Promise<{ items: Incident[]; totalCount: number }> =>
  request('/incidents')

export const listInstanceIncidents = (instanceKey: number): Promise<{ items: Incident[]; totalCount: number }> =>
  request(`/instances/${instanceKey}/incidents`)

export const resolveIncident = (incidentKey: number): Promise<void> =>
  request(`/incidents/${incidentKey}/resolve`, { method: 'POST' })
