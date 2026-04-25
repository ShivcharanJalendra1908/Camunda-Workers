// pages/InstanceDetailPage.tsx
import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  getInstance, listVariables, listJobs,
  listInstanceIncidents, cancelInstance, resolveIncident, updateJobRetries,
} from '../api/operateApi'
import { useOperateWS } from '../hooks/useOperateWS'
import type { ProcessInstance, Variable, Job, Incident } from '../types/operate'

type Tab = 'variables' | 'jobs' | 'incidents'

export default function InstanceDetailPage() {
  const { key } = useParams<{ key: string }>()
  const instanceKey = Number(key)
  const navigate = useNavigate()

  const [instance,  setInstance]  = useState<ProcessInstance | null>(null)
  const [variables, setVariables] = useState<Variable[]>([])
  const [jobs,      setJobs]      = useState<Job[]>([])
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [tab,       setTab]       = useState<Tab>('variables')
  const [loading,   setLoading]   = useState(true)
  const [actionMsg, setActionMsg] = useState<string | null>(null)

  useEffect(() => {
    Promise.all([
      getInstance(instanceKey),
      listVariables(instanceKey),
      listJobs(instanceKey),
      listInstanceIncidents(instanceKey),
    ]).then(([inst, vars, jobsRes, incRes]) => {
      setInstance(inst)
      setVariables(vars.items)
      setJobs(jobsRes.items)
      setIncidents(incRes.items)
    }).finally(() => setLoading(false))
  }, [instanceKey])

  // Refresh incidents on WS event
  useOperateWS({
    INCIDENT_CREATED:  () => listInstanceIncidents(instanceKey).then(r => setIncidents(r.items)),
    INCIDENT_RESOLVED: () => listInstanceIncidents(instanceKey).then(r => setIncidents(r.items)),
    INSTANCE_UPDATED:  () => getInstance(instanceKey).then(setInstance),
  })

  const handleCancel = async () => {
    if (!confirm('Cancel this process instance?')) return
    await cancelInstance(instanceKey)
    setActionMsg('Instance cancellation requested.')
    setInstance(prev => prev ? { ...prev, state: 'CANCELED' } : prev)
  }

  const handleResolve = async (incidentKey: number) => {
    await resolveIncident(incidentKey)
    setIncidents(prev => prev.filter(i => i.incidentKey !== incidentKey))
    setActionMsg('Incident resolved.')
  }

  const handleRetries = async (jobKey: number) => {
    const n = parseInt(prompt('Set retries to:') ?? '0', 10)
    if (isNaN(n) || n < 1) return
    await updateJobRetries(jobKey, n)
    setJobs(prev => prev.map(j => j.jobKey === jobKey ? { ...j, retries: n } : j))
    setActionMsg(`Retries updated to ${n}.`)
  }

  if (loading)   return <div className="operate-loading">Loading instance...</div>
  if (!instance) return <div className="operate-error">Instance not found.</div>

  return (
    <div className="operate-page">
      {/* Header */}
      <div className="operate-page-header">
        <div>
          <button className="operate-back" onClick={() => navigate(-1)}>← Back</button>
          <h1>
            {instance.bpmnProcessId}
            <span className="operate-subtitle"> #{instanceKey}</span>
          </h1>
        </div>
        <div className="operate-header-actions">
          <StateTag state={instance.state} />
          {instance.state === 'ACTIVE' && (
            <button className="operate-btn operate-btn--danger" onClick={handleCancel}>
              Cancel instance
            </button>
          )}
        </div>
      </div>

      {actionMsg && (
        <div className="operate-toast" onClick={() => setActionMsg(null)}>{actionMsg} ✕</div>
      )}

      {/* Meta */}
      <div className="operate-meta-row">
        <MetaItem label="Started"    value={new Date(instance.startTime).toLocaleString()} />
        <MetaItem label="Version"    value={`v${instance.version}`} />
        {instance.endTime && <MetaItem label="Ended" value={new Date(instance.endTime).toLocaleString()} />}
        <MetaItem label="Incidents"  value={String(incidents.length)} danger={incidents.length > 0} />
      </div>

      {/* Tabs */}
      <div className="operate-tabs">
        {(['variables', 'jobs', 'incidents'] as Tab[]).map(t => (
          <button
            key={t}
            className={`operate-tab ${tab === t ? 'operate-tab--active' : ''}`}
            onClick={() => setTab(t)}
          >
            {t.charAt(0).toUpperCase() + t.slice(1)}
            {t === 'incidents' && incidents.length > 0 && (
              <span className="operate-badge operate-badge--red operate-badge--sm">{incidents.length}</span>
            )}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === 'variables' && (
        <VariablesTab variables={variables} />
      )}
      {tab === 'jobs' && (
        <JobsTab jobs={jobs} onRetries={handleRetries} />
      )}
      {tab === 'incidents' && (
        <IncidentsTab incidents={incidents} onResolve={handleResolve} />
      )}
    </div>
  )
}

// ── Sub-components ────────────────────────────────────────────────────────────

function MetaItem({ label, value, danger }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="operate-meta-item">
      <span className="operate-meta-label">{label}</span>
      <span className={`operate-meta-value ${danger ? 'operate-meta-value--danger' : ''}`}>{value}</span>
    </div>
  )
}

function VariablesTab({ variables }: { variables: Variable[] }) {
  if (variables.length === 0) return <div className="operate-empty">No variables</div>
  return (
    <table className="operate-table">
      <thead><tr><th>Name</th><th>Value</th><th>Scope</th></tr></thead>
      <tbody>
        {variables.map(v => (
          <tr key={v.variableKey}>
            <td><code>{v.name}</code></td>
            <td><code className="operate-value">{v.value}{v.truncated && '…'}</code></td>
            <td><code>{v.scopeKey}</code></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function JobsTab({ jobs, onRetries }: { jobs: Job[]; onRetries: (key: number) => void }) {
  if (jobs.length === 0) return <div className="operate-empty">No jobs</div>
  return (
    <table className="operate-table">
      <thead><tr><th>Element</th><th>Type</th><th>State</th><th>Retries</th><th>Error</th><th></th></tr></thead>
      <tbody>
        {jobs.map(j => (
          <tr key={j.jobKey}>
            <td>{j.elementId}</td>
            <td><code>{j.jobType}</code></td>
            <td><JobStateTag state={j.state} /></td>
            <td>{j.retries}</td>
            <td className="operate-error-msg">{j.errorMessage}</td>
            <td>
              {j.state === 'FAILED' && (
                <button className="operate-btn operate-btn--sm" onClick={() => onRetries(j.jobKey)}>
                  Set retries
                </button>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function IncidentsTab({ incidents, onResolve }: { incidents: Incident[]; onResolve: (key: number) => void }) {
  if (incidents.length === 0) return <div className="operate-empty">No active incidents ✓</div>
  return (
    <table className="operate-table">
      <thead><tr><th>Element</th><th>Error Type</th><th>Message</th><th>Created</th><th></th></tr></thead>
      <tbody>
        {incidents.map(inc => (
          <tr key={inc.incidentKey}>
            <td>{inc.elementId}</td>
            <td><span className="operate-badge operate-badge--red">{inc.errorType}</span></td>
            <td className="operate-error-msg">{inc.errorMessage}</td>
            <td>{new Date(inc.createdAt).toLocaleString()}</td>
            <td>
              <button className="operate-btn operate-btn--sm operate-btn--success" onClick={() => onResolve(inc.incidentKey)}>
                Resolve
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function StateTag({ state }: { state: string }) {
  const map: Record<string, string> = { ACTIVE: 'blue', COMPLETED: 'green', CANCELED: 'gray' }
  return <span className={`operate-badge operate-badge--${map[state] ?? 'gray'}`}>{state}</span>
}

function JobStateTag({ state }: { state: string }) {
  const map: Record<string, string> = { FAILED: 'red', COMPLETED: 'green', ACTIVATED: 'blue', ACTIVATABLE: 'gray' }
  return <span className={`operate-badge operate-badge--${map[state] ?? 'gray'}`}>{state}</span>
}
