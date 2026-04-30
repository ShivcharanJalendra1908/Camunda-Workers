// types/operate.ts

export type ProcessInstanceState = 'ACTIVE' | 'COMPLETED' | 'CANCELED'
export type JobState = 'ACTIVATABLE' | 'ACTIVATED' | 'COMPLETED' | 'FAILED'
export type IncidentState = 'ACTIVE' | 'RESOLVED'

export interface ProcessInstance {
  processInstanceKey: number
  processDefinitionKey: number
  bpmnProcessId: string
  version: number
  state: ProcessInstanceState
  startTime: string
  endTime?: string
  parentInstanceKey?: number
  incidentCount?: number
}

export interface ProcessInstanceListResponse {
  items: ProcessInstance[]
  totalCount: number
  page: number
  pageSize: number
}

export interface ProcessInstanceFilter {
  bpmnProcessId?: string
  state?: ProcessInstanceState
  startTimeFrom?: string
  startTimeTo?: string
  page?: number
  pageSize?: number
}

export interface DeployedProcess {
  processDefinitionKey: number
  bpmnProcessId: string
  version: number
  resourceName: string
  activeCount: number
  incidentCount: number
  completedCount: number
}

export interface Job {
  jobKey: number
  processInstanceKey: number
  elementId: string
  jobType: string
  state: JobState
  retries: number
  worker?: string
  errorMessage?: string
  errorCode?: string
}

export interface Incident {
  incidentKey: number
  processInstanceKey: number
  bpmnProcessId: string
  elementId: string
  jobKey?: number
  errorType: string
  errorMessage: string
  state: IncidentState
  createdAt: string
  resolvedAt?: string
}

export interface Variable {
  variableKey: number
  processInstanceKey: number
  scopeKey: number
  name: string
  value: string
  truncated: boolean
}

export type WSEventType =
  | 'INSTANCE_UPDATED'
  | 'INCIDENT_CREATED'
  | 'INCIDENT_RESOLVED'
  | 'JOB_FAILED'
  | 'JOB_COMPLETED'

export interface WSEvent {
  type: WSEventType
  timestamp: string
  payload: unknown
}
